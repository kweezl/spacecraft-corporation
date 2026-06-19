package session

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the Manager and runs it via the fx lifecycle. Its OnStart
// hook runs after the migrator invoke, so the schema already exists. Core module.
func Module() fx.Option {
	return fx.Module("session",
		logger.Decorate("session"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(NewFactory),
		fx.Provide(fx.Annotate(
			newManager,
			// access is optional: the permissions feature provides it when enabled;
			// otherwise it is nil and the gate allows every command. loc renders the
			// approval/denied replies (required, from the i18n module).
			fx.ParamTags(``, ``, ``, ``, `optional:"true"`, ``, `group:"guild_create"`, `group:"guild_delete"`, ``, ``),
		)),
		// Contribute a "discord" readiness probe (gateway connected) to the
		// instrumentation group.
		fx.Provide(fx.Annotate(
			newReadinessCheck,
			fx.ResultTags(`group:"readiness_checks"`),
		)),
		fx.Invoke(register),
	)
}
