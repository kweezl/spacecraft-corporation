package servers

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the server-tracking Manager: it records servers on join,
// logs membership events, and acts as the session's approval gate. Core module,
// always loaded — server approval is not optional. Its OnStart-time work is
// driven by Discord guild events wired through the session.
func Module() fx.Option {
	return fx.Module("servers",
		logger.Decorate("servers"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newRepository),
		fx.Provide(newManager),
		// Expose the manager as the session's approval gate.
		fx.Provide(func(m *Manager) session.ServerApproval { return m }),
		// Contribute guild lifecycle reactions into the session's groups.
		fx.Provide(fx.Annotate(
			func(m *Manager) session.GuildCreateFunc { return m.OnGuildCreate },
			fx.ResultTags(`group:"guild_create"`),
		)),
		fx.Provide(fx.Annotate(
			func(m *Manager) session.GuildDeleteFunc { return m.OnGuildDelete },
			fx.ResultTags(`group:"guild_delete"`),
		)),
	)
}
