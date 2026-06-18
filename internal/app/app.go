// Package app is the composition root: it assembles the fx option list from the
// always-on core modules plus the feature modules selected once from FEATURES.
package app

import (
	"fmt"

	"github.com/kweezl/spacecraft-cadet/internal/appconfig"
	"github.com/kweezl/spacecraft-cadet/internal/crypto"
	"github.com/kweezl/spacecraft-cadet/internal/db"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"github.com/kweezl/spacecraft-cadet/internal/discord/session"
	"github.com/kweezl/spacecraft-cadet/internal/feature"
	"github.com/kweezl/spacecraft-cadet/internal/features/ping"
	"github.com/kweezl/spacecraft-cadet/internal/logger"
	"github.com/kweezl/spacecraft-cadet/internal/migrator"
	"github.com/kweezl/spacecraft-cadet/internal/token"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

// featureModules maps each optional feature to its fx module constructor.
// Adding a feature = implement feature.Name + register it here.
var featureModules = map[feature.Name]func() fx.Option{
	feature.Ping: ping.Module,
}

// coreModules are always loaded.
func coreModules() []fx.Option {
	return []fx.Option{
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
		appconfig.Module(),
		logger.Module(),
		db.Module(),
		migrator.Module(),
		crypto.Module(),
		token.Module(),
		registry.Module(),
		session.Module(),
	}
}

// selectFeatures maps enabled feature names to their fx options.
func selectFeatures(names []feature.Name) ([]fx.Option, error) {
	opts := make([]fx.Option, 0, len(names))
	for _, name := range names {
		ctor, ok := featureModules[name]
		if !ok {
			return nil, fmt.Errorf("no module registered for feature %q", name)
		}
		opts = append(opts, ctor())
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
	return append(coreModules(), features...), nil
}
