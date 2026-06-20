package permissions

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the role-based access gate and the /permissions panel. The
// gate is exposed as the session's CommandAccess; the session injects it
// optionally, so when this feature is disabled the session allows every command.
// Whether it loads is decided by the composition root from the FEATURES env var.
func Module() fx.Option {
	return fx.Module("permissions",
		logger.Decorate("permissions"),
		fx.Provide(newRepository),
		// Store caches per-server role maps for the gate; the gate and the panel
		// both use it so writes invalidate the cache.
		fx.Provide(NewStore),
		fx.Provide(NewGate),
		// Expose the gate as the session's command-access check.
		fx.Provide(func(g *Gate) session.CommandAccess { return g }),
		// Catalog enumerates the commands shown in the panel. It is provided
		// dependency-free and bound to the registry below, breaking the construction
		// cycle (the /permissions command is itself a member of the command group).
		fx.Provide(newCatalog),
		fx.Provide(newPanel),
		// Contribute the /permissions command and its component handler.
		fx.Provide(fx.Annotate(
			func(p *panel) *registry.Command { return p.command() },
			fx.ResultTags(`group:"commands"`),
		)),
		fx.Provide(fx.Annotate(
			func(p *panel) *registry.Component { return p.component() },
			fx.ResultTags(`group:"components"`),
		)),
		// Bind the catalog to the registry once both are built (post-construction,
		// so it does not feed back into the command group above).
		fx.Invoke(bindCatalog),
	)
}
