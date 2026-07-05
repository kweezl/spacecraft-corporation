package gamedata

import (
	"context"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
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
		fx.Provide(provideSearcher),
		// Build the Registry eagerly even before a feature consumes it, so the
		// loaded-versions log and any unknown-version warnings surface at startup
		// (and an undefined parent fails fast) rather than on first use.
		fx.Invoke(func(*Registry) {}),
	)
}

// provideSearcher constructs the Searcher, pre-warms its indexes in the
// background at startup, and registers index cleanup on shutdown. Warmup covers
// the server-pickable (renderable) languages — the only ones a server can search
// in — so the first autocomplete hits a ready index. It runs in a goroutine so
// it never blocks fx startup; until it finishes, queries just build lazily as
// before.
func provideSearcher(lc fx.Lifecycle, log *zap.Logger, reg *Registry, tr *i18n.Translator) *Searcher {
	s := newSearcher(reg)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			langs := tr.Languages()
			go func() {
				if err := s.Warm(langs); err != nil {
					log.Warn("gamedata search index warmup failed", zap.Error(err))
					return
				}
				log.Info("gamedata search indexes warmed", zap.Int("languages", len(langs)))
			}()
			return nil
		},
		OnStop: func(context.Context) error { return s.Close() },
	})
	return s
}
