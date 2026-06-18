package logger

import (
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestNew_DefaultLevel(t *testing.T) {
	log, err := New(Config{Level: zapcore.InfoLevel})
	require.NoError(t, err)
	require.NotNil(t, log)
	assert.True(t, log.Core().Enabled(zapcore.InfoLevel))
	assert.False(t, log.Core().Enabled(zapcore.DebugLevel))
}

func TestNew_DebugLevel(t *testing.T) {
	log, err := New(Config{Level: zapcore.DebugLevel})
	require.NoError(t, err)
	assert.True(t, log.Core().Enabled(zapcore.DebugLevel))
}

func TestConfig_DefaultsToInfo(t *testing.T) {
	cfg, err := env.ParseAs[Config]()
	require.NoError(t, err)
	assert.Equal(t, zapcore.InfoLevel, cfg.Level)
}

func TestConfig_ParsesLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err := env.ParseAs[Config]()
	require.NoError(t, err)
	assert.Equal(t, zapcore.DebugLevel, cfg.Level)
}

func TestConfig_InvalidLevelFails(t *testing.T) {
	t.Setenv("LOG_LEVEL", "not-a-level")
	_, err := env.ParseAs[Config]()
	require.Error(t, err)
}
