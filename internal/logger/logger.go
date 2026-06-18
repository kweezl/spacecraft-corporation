// Package logger provides a JSON zap logger that writes to stderr and attaches
// stacktraces at error level and above.
package logger

import (
	"context"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config is this module's env config. zapcore.Level implements TextUnmarshaler,
// so caarlos0/env parses LOG_LEVEL directly (and rejects invalid levels).
type Config struct {
	Level zapcore.Level `env:"LOG_LEVEL" envDefault:"info"`
}

// New builds a *zap.Logger: JSON encoder, stderr output, stacktraces at error+.
func New(cfg Config) (*zap.Logger, error) {
	zcfg := zap.NewProductionConfig()
	zcfg.Encoding = "json"
	zcfg.OutputPaths = []string{"stderr"}
	zcfg.ErrorOutputPaths = []string{"stderr"}
	zcfg.Level = zap.NewAtomicLevelAt(cfg.Level)
	// Disable zap's built-in stacktrace policy; we set our own threshold below.
	zcfg.DisableStacktrace = true

	return zcfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
}

func registerSync(lc fx.Lifecycle, log *zap.Logger) {
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			// Sync errors on stderr are expected on some platforms; ignore.
			_ = log.Sync()
			return nil
		},
	})
}
