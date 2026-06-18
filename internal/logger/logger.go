// Package logger provides a JSON zap logger that writes to stderr and attaches
// stacktraces at error level and above.
package logger

import (
	"context"

	"github.com/kweezl/spacecraft-cadet/internal/appconfig"
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
// Every log line carries app_name and app_version from AppConfig.
func New(cfg Config, app appconfig.AppConfig) (*zap.Logger, error) {
	zcfg := zap.NewProductionConfig()
	zcfg.Encoding = "json"
	zcfg.OutputPaths = []string{"stderr"}
	zcfg.ErrorOutputPaths = []string{"stderr"}
	zcfg.Level = zap.NewAtomicLevelAt(cfg.Level)
	// Disable zap's built-in stacktrace policy; we set our own threshold below.
	zcfg.DisableStacktrace = true

	log, err := zcfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		return nil, err
	}
	return withAppFields(log, app), nil
}

// withAppFields attaches app_name and app_version to every log line.
func withAppFields(log *zap.Logger, app appconfig.AppConfig) *zap.Logger {
	return log.With(
		zap.String("app_name", app.Name),
		zap.String("app_version", app.Version),
	)
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
