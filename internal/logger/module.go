package logger

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"
)

// Module provides the logger and flushes it on shutdown. Core module.
func Module() fx.Option {
	return fx.Module("logger",
		Decorate("logger"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(New),
		fx.Invoke(registerSync),
	)
}
