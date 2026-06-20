package outbox

import (
	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module provides the transactional-outbox Enqueuer and runs the background
// worker. Core module (always loaded) so the Enqueuer is available to any feature
// repository and the table exists; with no registered handlers the worker simply
// polls an empty queue. Handlers are contributed by features into the
// "outbox_handlers" fx group.
func Module() fx.Option {
	return fx.Module("outbox",
		logger.Decorate("outbox"),
		fx.Provide(env.ParseAs[Config]),
		fx.Provide(NewEnqueuer),
		fx.Provide(fx.Annotate(
			newWorker,
			fx.ParamTags(``, `group:"outbox_handlers"`, ``, ``),
		)),
		fx.Invoke(func(lc fx.Lifecycle, w *Worker) {
			lc.Append(fx.Hook{OnStart: w.Start, OnStop: w.Stop})
		}),
	)
}
