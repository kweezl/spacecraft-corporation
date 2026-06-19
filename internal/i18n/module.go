package i18n

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the Translator and the Localizer. Core module, always loaded —
// every user-facing message renders through it. The Localizer needs a Resolver
// (per-server theme/language), provided by the settings module.
func Module() fx.Option {
	return fx.Module("i18n",
		logger.Decorate("i18n"),
		fx.Provide(loadConfig),
		fx.Provide(New),
		fx.Provide(NewLocalizer),
	)
}
