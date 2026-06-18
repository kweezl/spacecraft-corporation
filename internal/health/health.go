// Package health serves Docker/Kubernetes liveness and readiness probes plus a
// Prometheus metrics endpoint over a small ops HTTP server. Readiness flips to
// true only once the whole fx app has finished starting (see MarkReady).
package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is this module's env config.
type Config struct {
	Addr string `env:"HEALTH_ADDR" envDefault:":8080"`
}

// Readiness tracks whether the application has finished starting.
type Readiness struct {
	ready atomic.Bool
}

func newReadiness() *Readiness { return &Readiness{} }

// SetReady marks the application ready.
func (r *Readiness) SetReady() { r.ready.Store(true) }

// Ready reports whether the application has finished starting.
func (r *Readiness) Ready() bool { return r.ready.Load() }

// newRegistry builds a dedicated Prometheus registry (no global default state),
// pre-loaded with the standard Go runtime collectors.
func newRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	return reg
}

// newHandler builds the ops HTTP handler: liveness, readiness, metrics.
func newHandler(ready *Readiness, reg *prometheus.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("starting"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return mux
}

// Server is the ops HTTP server.
type Server struct {
	httpServer *http.Server
	log        *zap.Logger
}

func newServer(cfg Config, ready *Readiness, reg *prometheus.Registry, log *zap.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:    cfg.Addr,
			Handler: newHandler(ready, reg),
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
				return fmt.Errorf("health: listen %s: %w", s.httpServer.Addr, err)
			}
			go func() {
				if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.log.Error("health server stopped", zap.Error(err))
				}
			}()
			s.log.Info("health server listening", zap.String("addr", s.httpServer.Addr))
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return s.httpServer.Shutdown(ctx)
		},
	})
}

// MarkReady appends a lifecycle hook that flips readiness to true. The
// composition root adds it as the LAST option so this OnStart runs after every
// other module's — readiness goes green only once all modules have started.
func MarkReady(lc fx.Lifecycle, ready *Readiness, log *zap.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ready.SetReady()
			log.Info("application ready")
			return nil
		},
	})
}
