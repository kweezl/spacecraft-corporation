package supply

import (
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
)

// Module provides the /supply self-scoped console, its component + modal
// handlers, the public reserve/deliver/release panel, and the outbox task
// handlers that perform every Discord side effect. Feature module (loaded only
// when "supply" is in FEATURES). It needs no permissions feature: access to
// running /supply is Discord-managed and every mutation is ownership-scoped in
// SQL. It consumes core services — the session Live as its Discord Gateway
// (forum thread ops), the settings Store for the per-server forum channel and
// open-request limit, the gamedata search + emoji store for the item picker, and
// the outbox Enqueuer (in the repository). It contributes two /settings sections
// (supply forum channel, request limit) via the settings_sections group.
func Module() fx.Option {
	return fx.Module("supply",
		logger.Decorate("supply"),
		fx.Provide(newRepository),
		fx.Provide(New),
		fx.Provide(func(l *session.Live) Gateway { return l }),
		// The settings store fills several narrow supply-local views (get-only at
		// runtime; get+set for the two /settings sections). Distinct interface types
		// so there is no fx collision with contracts providing the same shared type.
		fx.Provide(func(s *settings.Store) ForumConfig { return s }),
		fx.Provide(func(s *settings.Store) LimitConfig { return s }),
		fx.Provide(func(s *settings.Store) forumSettings { return s }),
		fx.Provide(func(s *settings.Store) limitSettings { return s }),
		fx.Provide(func(s *gamedata.Searcher) GameSearch { return s }),
		fx.Provide(func(s *settings.Store) LangResolver { return s }),
		// Contribute the supply forum-channel and request-limit controls to /settings.
		fx.Provide(fx.Annotate(
			newForumSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		fx.Provide(fx.Annotate(
			newLimitSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		// Contribute the /supply command and its component/modal handler.
		fx.Provide(fx.Annotate(
			func(f *Feature) *registry.Command { return f.Command() },
			fx.ResultTags(`group:"commands"`),
		)),
		fx.Provide(fx.Annotate(
			func(f *Feature) *registry.Component { return f.Component() },
			fx.ResultTags(`group:"components"`),
		)),
		// Contribute the outbox task handlers (create/refresh/close).
		fx.Provide(fx.Annotate(
			func(f *Feature) []outbox.Registration { return f.Registrations() },
			fx.ResultTags(`group:"outbox_handlers,flatten"`),
		)),
	)
}
