package logger

import (
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Decorate scopes, to the enclosing fx.Module, a child logger tagged with the
// module name, so its log lines carry a "module" field and it is easy to see
// where a log came from. The decorator is lazy: it only runs if the module
// actually consumes *zap.Logger.
func Decorate(module string) fx.Option {
	return fx.Decorate(func(log *zap.Logger) *zap.Logger {
		return log.With(zap.String("module", module))
	})
}
