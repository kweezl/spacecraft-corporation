package logger

import (
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/kweezl/spacecraft-corporation/internal/appconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

var testApp = appconfig.AppConfig{Name: "spacecraft-corporation", Version: "1.2.3"}

func TestNew_DefaultLevel(t *testing.T) {
	log, err := New(Config{Level: zapcore.InfoLevel}, testApp)
	require.NoError(t, err)
	require.NotNil(t, log)
	assert.True(t, log.Core().Enabled(zapcore.InfoLevel))
	assert.False(t, log.Core().Enabled(zapcore.DebugLevel))
}

func TestNew_DebugLevel(t *testing.T) {
	log, err := New(Config{Level: zapcore.DebugLevel}, testApp)
	require.NoError(t, err)
	assert.True(t, log.Core().Enabled(zapcore.DebugLevel))
}

// TestWithAppFields confirms every log line carries app_name and app_version.
func TestWithAppFields(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	log := withAppFields(zap.New(core), testApp)
	log.Info("hello")

	entries := logs.All()
	require.Len(t, entries, 1)
	m := entries[0].ContextMap()
	assert.Equal(t, "spacecraft-corporation", m["app_name"])
	assert.Equal(t, "1.2.3", m["app_version"])
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
