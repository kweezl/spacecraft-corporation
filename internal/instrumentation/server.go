package instrumentation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config.
type Config struct {
	Addr string `env:"INSTRUMENTATION_ADDR" envDefault:":9464"`
}

// Server is the ops HTTP server exposing the liveness, readiness, and metrics
// endpoints. It composes the per-endpoint handlers; the handlers know nothing
// about the server.
type Server struct {
	httpServer *http.Server
	log        *zap.Logger
}

func newServer(cfg Config, ready *Readiness, reg *prometheus.Registry, log *zap.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("/healthz", healthzHandler())
	mux.Handle("/readyz", readyzHandler(ready, log))
	mux.Handle("/metrics", metricsHandler(reg))
	return &Server{
		httpServer: &http.Server{
			Addr:    cfg.Addr,
			Handler: mux,
		},
		log: log,
	}
}

func (s *Server) register(lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// Bind synchronously so a port conflict fails startup, but serve in
			// the background so probes are reachable while later modules start.
			ln, err := net.Listen("tcp", s.httpServer.Addr)
			if err != nil {
				return fmt.Errorf("instrumentation: listen %s: %w", s.httpServer.Addr, err)
			}
			go func() {
				if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.log.Error("instrumentation server stopped", zap.Error(err))
				}
			}()
			s.log.Info("instrumentation server listening", zap.String("addr", s.httpServer.Addr))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return s.httpServer.Shutdown(ctx)
		},
	})
}
