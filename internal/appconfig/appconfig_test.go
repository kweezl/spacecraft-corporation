package appconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_DefaultName(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "spacecraft-cadet", cfg.Name)
}

func TestLoad_OverrideName(t *testing.T) {
	t.Setenv("APP_NAME", "custom-bot")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "custom-bot", cfg.Name)
}

func TestLoad_VersionDefaultsToDev(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "dev", cfg.Version)
}
