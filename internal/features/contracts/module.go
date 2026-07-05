package contracts

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
)

// Module provides the /contracts console command, its component + modal handlers,
// the public panel, the expiry sweeper, and the outbox task handlers that perform
// every Discord side effect. Feature module (loaded only when "contracts" is in
// FEATURES); it requires the permissions feature so the access gating takes effect
// (see internal/feature). It consumes three core services: the session Live as
// its Discord Gateway (forum thread ops), the settings Store as its ForumConfig
// (the per-server forum channel), and the outbox Enqueuer (in the repository, for
// the transactional outbox). It also contributes the forum-channel section to the
// /settings panel via the settings_sections group.
func Module() fx.Option {
	return fx.Module("contracts",
		logger.Decorate("contracts"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newRepository),
		fx.Provide(newTemplateRepository),
		fx.Provide(New),
		fx.Provide(func(l *session.Live) Gateway { return l }),
		fx.Provide(func(s *settings.Store) ForumConfig { return s }),
		fx.Provide(func(s *settings.Store) ReportsConfig { return s }),
		fx.Provide(func(s *settings.Store) RewardDefaults { return s }),
		fx.Provide(func(s *settings.Store) ItemCap { return s }),
		fx.Provide(func(s *settings.Store) ReportCSVConfig { return s }),
		// The gamedata picker's narrow views of two core services: the bleve
		// catalog search and the per-server language resolution (the same Resolve
		// the Localizer renders through). Registry + emoji Store are consumed
		// concretely in New.
		fx.Provide(func(s *gamedata.Searcher) GameSearch { return s }),
		fx.Provide(func(s *settings.Store) LangResolver { return s }),
		// Contribute the forum-channel and default-reward-factor controls to the
		// /settings panel.
		fx.Provide(fx.Annotate(
			newForumSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		fx.Provide(fx.Annotate(
			newFactorSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		fx.Provide(fx.Annotate(
			newReportsSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		fx.Provide(fx.Annotate(
			newMaxItemsSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		fx.Provide(fx.Annotate(
			newReportCSVSection,
			fx.ResultTags(`group:"settings_sections"`),
		)),
		fx.Provide(newSweeper),
		fx.Invoke(func(lc fx.Lifecycle, s *Sweeper) {
			lc.Append(fx.StartHook(s.Start))
			lc.Append(fx.StopHook(s.Stop))
		}),
		// Contribute the /contract command and its pagination component.
		fx.Provide(fx.Annotate(
			func(f *Feature) *registry.Command { return f.Command() },
			fx.ResultTags(`group:"commands"`),
		)),
		fx.Provide(fx.Annotate(
			func(f *Feature) *registry.Component { return f.Component() },
			fx.ResultTags(`group:"components"`),
		)),
		// Contribute the outbox task handlers (create/refresh/close) — flattened
		// into the worker's "outbox_handlers" group.
		fx.Provide(fx.Annotate(
			func(f *Feature) []outbox.Registration { return f.Registrations() },
			fx.ResultTags(`group:"outbox_handlers,flatten"`),
		)),
	)
}
