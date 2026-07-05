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
	"github.com/kweezl/spacecraft-corporation/internal/emoji"
	"github.com/kweezl/spacecraft-corporation/internal/feature"
	"github.com/kweezl/spacecraft-corporation/internal/features/bases"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/permissions"
	"github.com/kweezl/spacecraft-corporation/internal/features/ping"
	"github.com/kweezl/spacecraft-corporation/internal/features/supply"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/instrumentation"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"github.com/kweezl/spacecraft-corporation/internal/migrator"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
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
		// instrumentation starts early so probes answer (503) while later modules
		// start; readiness goes green only once every contributed check passes.
		instrumentation.Module(),
		db.Module(),
		// outbox: transactional-outbox queue + worker. Core so the Enqueuer is
		// available to feature repositories (e.g. contracts) and the worker runs.
		outbox.Module(),
		registry.Module(),
		// i18n + settings render all user-facing messages: settings provides the
		// per-server theme/language resolver that i18n's Localizer reads.
		i18n.Module(),
		settings.Module(),
		// servers must load before session: it provides the approval gate the
		// session injects (fx resolves order, this is just for readability).
		servers.Module(),
		session.Module(),
		// emoji: name-keyed access to the bot's application emojis, synced at
		// startup (depends on session's Live for the gateway).
		emoji.Module(),
		// gamedata: compiled-in, versioned game reference data (items, contract
		// templates, ...). No I/O — the Registry is ready as soon as it is built.
		gamedata.Module(),
	}
}

// migrateOptions is the slim graph for one-shot --migrate mode: connect the
// pool, apply the embedded migrations, then shut down (see migrator.Module).
// No Discord session, instrumentation server, or features are wired.
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
		case feature.Permissions:
			opts = append(opts, permissions.Module())
		case feature.Bases:
			opts = append(opts, bases.Module())
		case feature.Contracts:
			opts = append(opts, contracts.Module())
		case feature.Supply:
			opts = append(opts, supply.Module())
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
	// Readiness is no longer flipped by a final invoke: each subsystem (db,
	// session) contributes a live ReadinessCheck into the instrumentation group,
	// so /readyz reflects actual dependency health rather than startup ordering.
	return append(coreModules(), features...), nil
}
