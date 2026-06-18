package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestDecorate_TagsLoggerWithModule(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	root := zap.New(core)

	var got *zap.Logger
	app := fxtest.New(t,
		fx.Provide(func() *zap.Logger { return root }),
		fx.Module("sample",
			Decorate("sample"),
			fx.Invoke(func(l *zap.Logger) { got = l }),
		),
	)
	app.RequireStart()
	defer app.RequireStop()

	require.NotNil(t, got)
	got.Info("hello")

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "sample", entries[0].ContextMap()["module"])
}
