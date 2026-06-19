package ping

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the /ping repository and contributes the command into the
// registry's "commands" group. Whether it loads at all is decided by the
// composition root (internal/app) from the FEATURES env var.
func Module() fx.Option {
	return fx.Module("ping",
		logger.Decorate("ping"),
		fx.Provide(newRepository),
		fx.Provide(fx.Annotate(
			NewCommand,
			fx.ResultTags(`group:"commands"`),
		)),
	)
}
