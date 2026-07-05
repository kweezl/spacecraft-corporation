package emoji

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the emoji Store and runs the startup Syncer. Core module:
// the Store gives every feature fast, name-keyed access to the bot's emojis, and
// the Syncer contributes a readiness probe so the bot is not reported ready until
// the emojis are loaded. Its background sync waits for the gateway itself, so it
// needs no startup-order dependency on the session module.
func Module() fx.Option {
	return fx.Module("emoji",
		logger.Decorate("emoji"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newStore),
		fx.Provide(newSyncer),
		// Contribute an "emoji" readiness probe to the instrumentation group.
		fx.Provide(fx.Annotate(
			newReadinessCheck,
			fx.ResultTags(`group:"readiness_checks"`),
		)),
		fx.Invoke(register),
	)
}

func register(lc fx.Lifecycle, s *Syncer) {
	lc.Append(fx.Hook{OnStart: s.Start, OnStop: s.Stop})
}
