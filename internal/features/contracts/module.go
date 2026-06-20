package contracts

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
)

// Module provides the /contract command, its list-pagination component, the
// expiry sweeper, and the outbox task handlers that perform every Discord side
// effect. Feature module (loaded only when "contracts" is in FEATURES); it
// requires the permissions feature so the per-leaf subcommand gating takes effect
// (see internal/feature). It consumes three core services: the session Live as
// its Discord Gateway (forum thread ops), the settings Store as its ForumConfig
// (the per-server forum channel), and the outbox Enqueuer (in the repository, for
// the transactional outbox).
func Module() fx.Option {
	return fx.Module("contracts",
		logger.Decorate("contracts"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newRepository),
		fx.Provide(New),
		fx.Provide(func(l *session.Live) Gateway { return l }),
		fx.Provide(func(s *settings.Store) ForumConfig { return s }),
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
