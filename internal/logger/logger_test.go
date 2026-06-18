package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestNew_DefaultLevel(t *testing.T) {
	log, err := New(Config{Level: "info"})
	require.NoError(t, err)
	require.NotNil(t, log)
	assert.True(t, log.Core().Enabled(zapcore.InfoLevel))
	assert.False(t, log.Core().Enabled(zapcore.DebugLevel))
}

func TestNew_DebugLevel(t *testing.T) {
	log, err := New(Config{Level: "debug"})
	require.NoError(t, err)
	assert.True(t, log.Core().Enabled(zapcore.DebugLevel))
}

func TestNew_InvalidLevel(t *testing.T) {
	_, err := New(Config{Level: "not-a-level"})
	require.Error(t, err)
}
