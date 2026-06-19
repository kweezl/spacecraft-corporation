package ping

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module contributes the /ping command into the registry's "commands" group.
// Whether it loads at all is decided by the composition root (internal/app)
// from the FEATURES env var. /ping is a stateless latency probe, so the module
// has no repository or database dependency.
func Module() fx.Option {
	return fx.Module("ping",
		logger.Decorate("ping"),
		fx.Provide(fx.Annotate(
			NewCommand,
			fx.ResultTags(`group:"commands"`),
		)),
	)
}
