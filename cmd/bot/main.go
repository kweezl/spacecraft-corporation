// Command bot is the SpaceCraft Discord bot entrypoint.
package main

import (
	"github.com/kweezl/spacecraft-cadet/internal/appconfig"
	mycrypto "github.com/kweezl/spacecraft-cadet/internal/crypto"
	"github.com/kweezl/spacecraft-cadet/internal/db"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"github.com/kweezl/spacecraft-cadet/internal/discord/session"
	"github.com/kweezl/spacecraft-cadet/internal/features/ping"
	"github.com/kweezl/spacecraft-cadet/internal/logger"
	"github.com/kweezl/spacecraft-cadet/internal/migrator"
	"github.com/kweezl/spacecraft-cadet/internal/token"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

func main() {
	fx.New(
		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),
		appconfig.Module,
		logger.Module,
		db.Module,
		migrator.Module,
		mycrypto.Module,
		token.Module,
		registry.Module,
		ping.Module,
		session.Module,
	).Run()
}
