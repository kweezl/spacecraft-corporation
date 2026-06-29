package gamedata

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the game-data Registry. Core module: the data is compiled in
// (generated pure-Go literals under db/v*), so there is no database, no I/O, and
// no readiness probe — the Registry is ready the moment it is built. Which
// versions it loads is decided by GAMEDATA_VERSIONS (default: all defined).
func Module() fx.Option {
	return fx.Module("gamedata",
		logger.Decorate("gamedata"),
		fx.Provide(newRegistry),
		// Build the Registry eagerly even before a feature consumes it, so the
		// loaded-versions log and any unknown-version warnings surface at startup
		// (and an undefined parent fails fast) rather than on first use.
		fx.Invoke(func(*Registry) {}),
	)
}
