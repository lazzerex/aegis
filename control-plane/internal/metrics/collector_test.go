package metrics

import (
	"sync"
	"testing"

	pb "github.com/lazzerex/aegis/control-plane/proto"
)

var (
	testCollectorOnce sync.Once
	testCollectorInst *Collector
)

func sharedTestCollector(t *testing.T) *Collector {
	t.Helper()
	testCollectorOnce.Do(func() {
		testCollectorInst = NewCollector()
	})
	return testCollectorInst
}

func TestUpdateFromProto_BackendCircuitStatesAndStats(t *testing.T) {
	c := sharedTestCollector(t)

	c.UpdateFromProto(&pb.MetricsData{
		ActiveConnections: 5,
		TotalConnections:  10,
		BackendMetrics: []*pb.BackendMetrics{
			{
				Address:           "collector-test-a:3000",
				ActiveConnections: 2,
				TotalRequests:     42,
				FailedRequests:    1,
				AvgLatencyMs:      3.5,
				CircuitState:      "Closed",
			},
			{
				Address:           "collector-test-a:3001",
				ActiveConnections: 0,
				TotalRequests:     0,
				FailedRequests:    5,
				AvgLatencyMs:      0,
				CircuitState:      "Open",
			},
		},
	})

	states := c.BackendCircuitStates()
	if states["collector-test-a:3000"] != "Closed" {
		t.Errorf("circuit state: got %q, want Closed", states["collector-test-a:3000"])
	}
	if states["collector-test-a:3001"] != "Open" {
		t.Errorf("circuit state: got %q, want Open", states["collector-test-a:3001"])
	}

	stats := c.BackendStats()
	got, ok := stats["collector-test-a:3000"]
	if !ok {
		t.Fatal("collector-test-a:3000 missing from BackendStats")
	}
	want := BackendStat{ActiveConnections: 2, TotalRequests: 42, FailedRequests: 1, AvgLatencyMs: 3.5}
	if got != want {
		t.Errorf("stats: got %+v, want %+v", got, want)
	}

	if _, ok := stats["collector-test-a:9999"]; ok {
		t.Error("unreported backend should be absent from BackendStats")
	}
}

func TestUpdateFromProto_StatsOverwriteOnSubsequentCalls(t *testing.T) {
	c := sharedTestCollector(t)

	c.UpdateFromProto(&pb.MetricsData{
		BackendMetrics: []*pb.BackendMetrics{
			{Address: "collector-test-b:3000", TotalRequests: 10, CircuitState: "Closed"},
		},
	})
	c.UpdateFromProto(&pb.MetricsData{
		BackendMetrics: []*pb.BackendMetrics{
			{Address: "collector-test-b:3000", TotalRequests: 20, CircuitState: "Open"},
		},
	})

	stats := c.BackendStats()
	if stats["collector-test-b:3000"].TotalRequests != 20 {
		t.Errorf("total requests: got %d, want 20 (latest snapshot, not cumulative)", stats["collector-test-b:3000"].TotalRequests)
	}
	states := c.BackendCircuitStates()
	if states["collector-test-b:3000"] != "Open" {
		t.Errorf("circuit state: got %q, want Open (latest snapshot)", states["collector-test-b:3000"])
	}
}
