package appconfig

import "go.uber.org/fx"

// Module exposes AppConfig to the fx graph. Core module: always loaded.
// appconfig is exempt from logger.Decorate — it is a dependency of the logger
// itself (so importing logger here would be an import cycle) and it does not log.
func Module() fx.Option {
	return fx.Module("appconfig", fx.Provide(Load))
}
