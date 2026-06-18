package registry

import "go.uber.org/fx"

// Module provides the Registry built from the fx command group. Core module.
func Module() fx.Option {
	return fx.Module("registry",
		fx.Provide(newCommandCounter),
		fx.Provide(New),
	)
}
