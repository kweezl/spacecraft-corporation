package migrator

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module runs migrations as an fx invoke. Invokes execute during fx
// construction, before any lifecycle OnStart hook (e.g. the session manager),
// guaranteeing the schema exists before sessions load tokens.
func Module() fx.Option {
	return fx.Module("migrator",
		logger.Decorate("migrator"),
		fx.Invoke(Run),
	)
}
