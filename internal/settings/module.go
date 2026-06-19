package settings

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides per-server settings: the Store (cache + persistence), the
// i18n.Resolver the Localizer reads, and the /settings command. Core module,
// always loaded — i18n's Localizer depends on the Resolver it provides.
func Module() fx.Option {
	return fx.Module("settings",
		logger.Decorate("settings"),
		fx.Provide(newRepository),
		fx.Provide(NewStore),
		// Expose the store as the i18n resolver (per-server theme/language).
		fx.Provide(func(s *Store) i18n.Resolver { return s }),
		// Contribute the /settings command into the registry's group.
		fx.Provide(fx.Annotate(
			NewCommand,
			fx.ResultTags(`group:"commands"`),
		)),
	)
}
