package crypto

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"
)

// Module provides a *Cipher built from ENCRYPTION_KEY. Core module.
func Module() fx.Option {
	return fx.Module("crypto",
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(provide),
	)
}
