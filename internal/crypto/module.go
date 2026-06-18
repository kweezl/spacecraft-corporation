package crypto

import (
	"github.com/caarlos0/env/v11"
	"github.com/kweezl/spacecraft-cadet/internal/logger"
	"go.uber.org/fx"
)

// Module provides a *Cipher built from ENCRYPTION_KEY. Core module.
func Module() fx.Option {
	return fx.Module("crypto",
		logger.Decorate("crypto"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(provide),
	)
}
