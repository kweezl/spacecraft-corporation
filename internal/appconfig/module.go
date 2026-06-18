package appconfig

import (
	"github.com/kweezl/spacecraft-cadet/internal/logger"
	"go.uber.org/fx"
)

// Module exposes AppConfig to the fx graph. Core module: always loaded.
func Module() fx.Option {
	return fx.Module("appconfig",
		logger.Decorate("appconfig"),
		fx.Provide(Load),
	)
}
