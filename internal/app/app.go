// Package app is the composition root: it assembles the fx option list from the
// always-on core modules plus the feature modules selected once from FEATURES.
package app

import (
	"fmt"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/appconfig"
	"github.com/kweezl/spacecraft-corporation/internal/db"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/servers"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/feature"
	"github.com/kweezl/spacecraft-corporation/internal/features/ping"
	"github.com/kweezl/spacecraft-corporation/internal/health"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"github.com/kweezl/spacecraft-corporation/internal/migrator"
)

// fxLogger routes fx's own wiring logs through zap.
func fxLogger() fx.Option {
	return fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
		return &fxevent.ZapLogger{Logger: log}
	})
}

// coreModules are always loaded in normal (bot) mode. Note migrator is NOT here:
// the running bot never applies migrations — that is the one-shot --migrate mode
// (migrateOptions). Schema changes are an explicit, separate step.
func coreModules() []fx.Option {
	return []fx.Option{
		fxLogger(),
		appconfig.Module(),
		logger.Module(),
		// health starts early so probes answer (503) while later modules start.
		health.Module(),
		db.Module(),
		registry.Module(),
		// servers must load before session: it provides the approval gate the
		// session injects (fx resolves order, this is just for readability).
		servers.Module(),
		session.Module(),
	}
}

// migrateOptions is the slim graph for one-shot --migrate mode: connect the
// pool, apply the embedded migrations, then shut down (see migrator.Module).
// No Discord session, health server, or features are wired.
func migrateOptions() []fx.Option {
	return []fx.Option{
		fxLogger(),
		appconfig.Module(),
		logger.Module(),
		db.Module(),
		migrator.Module(),
	}
}

// selectFeatures maps enabled feature names to their fx options. Adding a
// feature = add a case here (plus its feature.Name and Module()).
func selectFeatures(names []feature.Name) ([]fx.Option, error) {
	opts := make([]fx.Option, 0, len(names))
	for _, name := range names {
		switch name {
		case feature.Ping:
			opts = append(opts, ping.Module())
		default:
			return nil, fmt.Errorf("no module registered for feature %q", name)
		}
	}
	return opts, nil
}

// Options builds the fx option list. In migrate mode it returns the slim
// migrate-and-exit graph; otherwise the core bot modules plus the features
// selected once from FEATURES.
func Options(migrate bool) ([]fx.Option, error) {
	if migrate {
		return migrateOptions(), nil
	}
	names, err := feature.Load()
	if err != nil {
		return nil, err
	}
	features, err := selectFeatures(names)
	if err != nil {
		return nil, err
	}
	opts := append(coreModules(), features...)
	// MarkReady is appended LAST so its OnStart hook runs after every other
	// module's: readiness goes green only once all modules have started.
	opts = append(opts, fx.Invoke(health.MarkReady))
	return opts, nil
}
