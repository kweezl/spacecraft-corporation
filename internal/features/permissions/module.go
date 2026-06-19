package permissions

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the role-based access gate and the /permissions management
// command. The gate is exposed as the session's CommandAccess; the session
// injects it optionally, so when this feature is disabled the session allows
// every command. Whether it loads is decided by the composition root from the
// FEATURES env var.
func Module() fx.Option {
	return fx.Module("permissions",
		logger.Decorate("permissions"),
		fx.Provide(newRepository),
		// Store caches per-server role maps for the gate; the gate and the
		// /permissions command both use it so writes invalidate the cache.
		fx.Provide(NewStore),
		fx.Provide(NewGate),
		// Expose the gate as the session's command-access check.
		fx.Provide(func(g *Gate) session.CommandAccess { return g }),
		// Contribute the /permissions command into the registry's group.
		fx.Provide(fx.Annotate(
			NewCommand,
			fx.ResultTags(`group:"commands"`),
		)),
	)
}
