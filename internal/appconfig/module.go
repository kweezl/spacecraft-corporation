package appconfig

import "go.uber.org/fx"

// Module exposes AppConfig to the fx graph. Core module: always loaded.
func Module() fx.Option {
	return fx.Module("appconfig", fx.Provide(Load))
}
