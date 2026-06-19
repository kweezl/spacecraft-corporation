// Package instrumentation serves Docker/Kubernetes liveness and readiness probes
// plus a Prometheus metrics endpoint over a small ops HTTP server. Readiness
// reflects live dependency health: subsystems (db, session) contribute probes
// into the "readiness_checks" fx group, and /readyz reports ready only when
// every probe passes.
package instrumentation

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the readiness aggregator, Prometheus registry, and ops HTTP
// server. Core module; placed early so probes respond during startup.
func Module() fx.Option {
	return fx.Module("instrumentation",
		logger.Decorate("instrumentation"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newReadiness),
		fx.Provide(newRegistry),
		fx.Provide(newServer),
		fx.Invoke(func(lc fx.Lifecycle, s *Server) { s.register(lc) }),
	)
}
