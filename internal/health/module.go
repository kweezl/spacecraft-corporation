package health

import (
	"github.com/caarlos0/env/v11"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"go.uber.org/fx"
)

// Module provides the readiness tracker, Prometheus registry, and ops HTTP
// server. Core module; placed early so probes respond during startup.
func Module() fx.Option {
	return fx.Module("health",
		logger.Decorate("health"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newReadiness),
		fx.Provide(newRegistry),
		fx.Provide(newServer),
		fx.Invoke(func(lc fx.Lifecycle, s *Server) { s.register(lc) }),
	)
}
