package metrics

import (
	"sync"

	pb "github.com/lazzerex/aegis/control-plane/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Collector struct {
	mu sync.RWMutex

	// Prometheus metrics
	activeConnections  prometheus.Gauge
	totalConnections   prometheus.Counter
	bytesSent          prometheus.Counter
	bytesReceived      prometheus.Counter
	avgLatency         prometheus.Gauge
	p99Latency         prometheus.Gauge
	backendConnections *prometheus.GaugeVec
	backendRequests    *prometheus.CounterVec
	backendFailures    *prometheus.CounterVec
	backendLatency     *prometheus.GaugeVec

	// Track last reported values to avoid double-counting streamed totals
	lastTotalConnections float64
	lastBytesSent        float64
	lastBytesReceived    float64
	lastBackendRequests  map[string]float64
	lastBackendFailures  map[string]float64
}

func NewCollector() *Collector {
	return &Collector{
		activeConnections: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "proxy_active_connections",
			Help: "Current number of active connections",
		}),
		totalConnections: promauto.NewCounter(prometheus.CounterOpts{
			Name: "proxy_total_connections",
			Help: "Total number of connections handled",
		}),
		bytesSent: promauto.NewCounter(prometheus.CounterOpts{
			Name: "proxy_bytes_sent_total",
			Help: "Total bytes sent to backends",
		}),
		bytesReceived: promauto.NewCounter(prometheus.CounterOpts{
			Name: "proxy_bytes_received_total",
			Help: "Total bytes received from backends",
		}),
		avgLatency: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "proxy_latency_avg_ms",
			Help: "Average latency in milliseconds",
		}),
		p99Latency: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "proxy_latency_p99_ms",
			Help: "P99 latency in milliseconds",
		}),
		backendConnections: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "proxy_backend_connections",
				Help: "Active connections per backend",
			},
			[]string{"backend"},
		),
		backendRequests: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "proxy_backend_requests_total",
				Help: "Total requests per backend",
			},
			[]string{"backend"},
		),
		backendFailures: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "proxy_backend_failures_total",
				Help: "Total failures per backend",
			},
			[]string{"backend"},
		),
		backendLatency: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "proxy_backend_latency_avg_ms",
				Help: "Average latency per backend in milliseconds",
			},
			[]string{"backend"},
		),

		lastBackendRequests: make(map[string]float64),
		lastBackendFailures: make(map[string]float64),
	}
}

func (c *Collector) UpdateFromProto(data *pb.MetricsData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update global metrics
	c.activeConnections.Set(float64(data.ActiveConnections))

	// Convert cumulative counts to increments before adding to counters
	if delta := float64(data.TotalConnections) - c.lastTotalConnections; delta > 0 {
		c.totalConnections.Add(delta)
		c.lastTotalConnections = float64(data.TotalConnections)
	}

	if delta := float64(data.BytesSent) - c.lastBytesSent; delta > 0 {
		c.bytesSent.Add(delta)
		c.lastBytesSent = float64(data.BytesSent)
	}

	if delta := float64(data.BytesReceived) - c.lastBytesReceived; delta > 0 {
		c.bytesReceived.Add(delta)
		c.lastBytesReceived = float64(data.BytesReceived)
	}
	c.avgLatency.Set(data.AvgLatencyMs)
	c.p99Latency.Set(data.P99LatencyMs)

	// Update backend metrics
	for _, backend := range data.BackendMetrics {
		addr := backend.Address

		c.backendConnections.WithLabelValues(addr).Set(float64(backend.ActiveConnections))
		c.backendLatency.WithLabelValues(addr).Set(backend.AvgLatencyMs)

		if delta := float64(backend.TotalRequests) - c.lastBackendRequests[addr]; delta > 0 {
			c.backendRequests.WithLabelValues(addr).Add(delta)
			c.lastBackendRequests[addr] = float64(backend.TotalRequests)
		}

		if delta := float64(backend.FailedRequests) - c.lastBackendFailures[addr]; delta > 0 {
			c.backendFailures.WithLabelValues(addr).Add(delta)
			c.lastBackendFailures[addr] = float64(backend.FailedRequests)
		}
	}
}
