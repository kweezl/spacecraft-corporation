package migrator

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/logger"
)

// Module applies the migrations once, then asks fx to stop the app so the
// process exits. It is loaded ONLY in one-shot migrate mode (the `--migrate`
// flag); the long-running bot never includes it and therefore never migrates.
func Module() fx.Option {
	return fx.Module("migrator",
		logger.Decorate("migrator"),
		fx.Invoke(runAndShutdown),
	)
}

// runAndShutdown applies the embedded migrations and then triggers fx shutdown.
// Run executes during fx construction; Shutdown sends a buffered signal that
// App.Run picks up once Start returns, so the process exits cleanly (running
// each module's OnStop, e.g. closing the pool) with status 0.
func runAndShutdown(pool *pgxpool.Pool, log *zap.Logger, sd fx.Shutdowner) error {
	if err := Run(pool, log); err != nil {
		return err
	}
	return sd.Shutdown()
}
