package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	collector *Collector
	server    *http.Server
}

func NewServer(collector *Collector) *Server {
	return &Server{
		collector: collector,
	}
}

func (s *Server) Start(address string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	s.server = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
