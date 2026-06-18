package token

import (
	"github.com/kweezl/spacecraft-cadet/internal/logger"
	"go.uber.org/fx"
)

// Module provides the token Repository. Core module.
func Module() fx.Option {
	return fx.Module("token",
		logger.Decorate("token"),
		fx.Provide(newRepository),
	)
}
