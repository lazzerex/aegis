package grpc

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazzerex/aegis/control-plane/internal/config"
	"github.com/lazzerex/aegis/control-plane/internal/metrics"
	pb "github.com/lazzerex/aegis/control-plane/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeServer struct {
	pb.UnimplementedProxyControlServer

	updateConfigCalls atomic.Int64
	failUpdateConfig  atomic.Bool

	streamOpens    atomic.Int64
	streamBehavior func(stream grpc.ServerStreamingServer[pb.MetricsData]) error
}

func (f *fakeServer) UpdateConfig(_ context.Context, _ *pb.ProxyConfig) (*pb.ConfigAck, error) {
	f.updateConfigCalls.Add(1)
	if f.failUpdateConfig.Load() {
		return &pb.ConfigAck{Success: false, Message: "rejected"}, nil
	}
	return &pb.ConfigAck{Success: true, Message: "ok"}, nil
}

func (f *fakeServer) StreamMetrics(_ *emptypb.Empty, stream grpc.ServerStreamingServer[pb.MetricsData]) error {
	f.streamOpens.Add(1)
	if f.streamBehavior != nil {
		return f.streamBehavior(stream)
	}
	return nil
}

type dialerSwitch struct {
	mu  sync.Mutex
	lis *bufconn.Listener
}

func (d *dialerSwitch) dial(ctx context.Context, _ string) (net.Conn, error) {
	d.mu.Lock()
	lis := d.lis
	d.mu.Unlock()
	return lis.DialContext(ctx)
}

func (d *dialerSwitch) set(lis *bufconn.Listener) {
	d.mu.Lock()
	d.lis = lis
	d.mu.Unlock()
}

func newFakeConn(t *testing.T, srv pb.ProxyControlServer, ds *dialerSwitch) (*Client, *grpc.Server, *bufconn.Listener) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	if ds != nil {
		ds.set(lis)
	}

	grpcSrv := grpc.NewServer()
	pb.RegisterProxyControlServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	dial := func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }
	if ds != nil {
		dial = ds.dial
	}

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  10 * time.Millisecond,
				Multiplier: 1.2,
				MaxDelay:   100 * time.Millisecond,
			},
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &Client{
		conn:   conn,
		client: pb.NewProxyControlClient(conn),
		logger: zap.NewNop(),
	}, grpcSrv, lis
}

var (
	testCollectorOnce sync.Once
	testCollector     *metrics.Collector
)

func newTestCollector() *metrics.Collector {
	testCollectorOnce.Do(func() { testCollector = metrics.NewCollector() })
	return testCollector
}

func testConfig() *config.Config {
	return &config.Config{
		Proxy: config.ProxyConfig{
			Listen: config.ListenConfig{TCP: ":8080"},
			Backends: []config.Backend{
				{Address: "localhost:3000", Weight: 100},
			},
		},
	}
}

func TestUpdateConfig_StoresLastCfgOnSuccess(t *testing.T) {
	srv := &fakeServer{}
	c, _, _ := newFakeConn(t, srv, nil)

	cfg := testConfig()
	if err := c.UpdateConfig(cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	c.cfgMu.Lock()
	got := c.lastCfg
	c.cfgMu.Unlock()

	if got != cfg {
		t.Error("lastCfg not set to the config passed to UpdateConfig")
	}
}

func TestUpdateConfig_LeavesLastCfgUnsetOnRejection(t *testing.T) {
	srv := &fakeServer{}
	srv.failUpdateConfig.Store(true)
	c, _, _ := newFakeConn(t, srv, nil)

	if err := c.UpdateConfig(testConfig()); err == nil {
		t.Fatal("expected error when server rejects config, got nil")
	}

	c.cfgMu.Lock()
	got := c.lastCfg
	c.cfgMu.Unlock()

	if got != nil {
		t.Error("lastCfg should remain unset after a rejected UpdateConfig")
	}
}

func TestWatchReconnect_RepushesConfigAfterReconnect(t *testing.T) {
	ds := &dialerSwitch{}
	srv1 := &fakeServer{}
	c, grpcSrv1, _ := newFakeConn(t, srv1, ds)

	cfg := testConfig()
	if err := c.UpdateConfig(cfg); err != nil {
		t.Fatalf("initial UpdateConfig: %v", err)
	}
	if srv1.updateConfigCalls.Load() != 1 {
		t.Fatalf("expected 1 UpdateConfig call on srv1, got %d", srv1.updateConfigCalls.Load())
	}

	c.WatchReconnect()

	grpcSrv1.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for c.conn.GetState().String() == "READY" {
		if time.Now().After(deadline) {
			t.Fatal("connection never left READY after server stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}

	srv2 := &fakeServer{}
	lis2 := bufconn.Listen(1024 * 1024)
	grpcSrv2 := grpc.NewServer()
	pb.RegisterProxyControlServer(grpcSrv2, srv2)
	go func() { _ = grpcSrv2.Serve(lis2) }()
	t.Cleanup(grpcSrv2.Stop)
	ds.set(lis2)

	deadline = time.Now().Add(5 * time.Second)
	for srv2.updateConfigCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("WatchReconnect did not re-push config to the new server in time (state=%s)", c.conn.GetState())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := srv2.updateConfigCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 re-pushed UpdateConfig on reconnect, got %d", got)
	}
}

func TestStreamMetrics_ReconnectsOnStreamError(t *testing.T) {
	var streamCount atomic.Int64
	done := make(chan struct{})

	srv := &fakeServer{
		streamBehavior: func(stream grpc.ServerStreamingServer[pb.MetricsData]) error {
			n := streamCount.Add(1)
			if n == 1 {
				_ = stream.Send(&pb.MetricsData{ActiveConnections: 1})
				return nil
			}
			_ = stream.Send(&pb.MetricsData{ActiveConnections: 2})
			close(done)
			<-stream.Context().Done()
			return nil
		},
	}
	c, _, _ := newFakeConn(t, srv, nil)

	c.StreamMetrics(newTestCollector())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("StreamMetrics did not reconnect and open a second stream in time (streamOpens=%d)", srv.streamOpens.Load())
	}

	if got := srv.streamOpens.Load(); got < 2 {
		t.Errorf("expected at least 2 StreamMetrics calls (initial + reconnect), got %d", got)
	}
}
