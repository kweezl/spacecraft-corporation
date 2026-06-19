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

// coreModules are always loaded.
func coreModules() []fx.Option {
	return []fx.Option{
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
		appconfig.Module(),
		logger.Module(),
		// health starts early so probes answer (503) while later modules start.
		health.Module(),
		db.Module(),
		migrator.Module(),
		registry.Module(),
		// servers must load before session: it provides the approval gate the
		// session injects (fx resolves order, this is just for readability).
		servers.Module(),
		session.Module(),
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

// Options builds the full fx option list: core modules plus the features
// selected once from FEATURES.
func Options() ([]fx.Option, error) {
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
