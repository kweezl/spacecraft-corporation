package settings

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides per-server settings: the Store (cache + persistence), the
// i18n.Resolver the Localizer reads, and the /settings panel. Core module,
// always loaded — i18n's Localizer depends on the Resolver it provides.
func Module() fx.Option {
	return fx.Module("settings",
		logger.Decorate("settings"),
		fx.Provide(newRepository),
		fx.Provide(NewStore),
		// Expose the store as the i18n resolver (per-server theme/language).
		fx.Provide(func(s *Store) i18n.Resolver { return s }),
		// The panel needs the access gate to re-authorize its mutating component
		// interactions; it is optional (nil when the permissions feature is off).
		fx.Provide(fx.Annotate(
			newPanel,
			fx.ParamTags(``, ``, ``, `optional:"true"`),
		)),
		// Contribute the /settings command and its component into the registry's groups.
		fx.Provide(fx.Annotate(
			func(p *panel) *registry.Command { return p.command() },
			fx.ResultTags(`group:"commands"`),
		)),
		fx.Provide(fx.Annotate(
			func(p *panel) *registry.Component { return p.component() },
			fx.ResultTags(`group:"components"`),
		)),
	)
}
