package token

import "go.uber.org/fx"

// Module provides the token Repository. Core module.
func Module() fx.Option {
	return fx.Module("token", fx.Provide(newRepository))
}
