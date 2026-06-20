package bases

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the /base command and its pagination component. It is a
// feature module (loaded only when "bases" is in FEATURES); it requires the
// permissions feature so the per-tier subcommand gating actually takes effect
// (see internal/feature). Without gating every /base subcommand would be open.
func Module() fx.Option {
	return fx.Module("bases",
		logger.Decorate("bases"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(newRepository),
		fx.Provide(New),
		// Contribute the /base command into the registry's command group.
		fx.Provide(fx.Annotate(
			func(f *Feature) *registry.Command { return f.Command() },
			fx.ResultTags(`group:"commands"`),
		)),
		// Contribute the pagination component handler into the component group.
		fx.Provide(fx.Annotate(
			func(f *Feature) *registry.Component { return f.Component() },
			fx.ResultTags(`group:"components"`),
		)),
	)
}
