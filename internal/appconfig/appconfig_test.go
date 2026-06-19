package appconfig

import (
	"testing"
	"time"
	// Embed zoneinfo so named-zone tests pass without OS tzdata (mirrors cmd/bot).
	_ "time/tzdata"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_DefaultName(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "spacecraft-corporation", cfg.Name)
}

func TestLoad_OverrideName(t *testing.T) {
	t.Setenv("APP_NAME", "custom-bot")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "custom-bot", cfg.Name)
}

func TestLoad_VersionDefaultsToUnspecified(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "0.0.0-unspecified", cfg.Version)
}

func TestLoad_OwnerDiscordID_DefaultsEmpty(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.OwnerDiscordID)
}

func TestLoad_OwnerDiscordID_AcceptsSnowflake(t *testing.T) {
	t.Setenv("APP_OWNER_DISCORD_ID", "  1517458460923662397  ") // surrounding space trimmed
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "1517458460923662397", cfg.OwnerDiscordID)
}

func TestLoad_OwnerDiscordID_RejectsNonNumeric(t *testing.T) {
	t.Setenv("APP_OWNER_DISCORD_ID", "kweezl#7278")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_OWNER_DISCORD_ID")
}

func TestLoad_DefaultsTimezoneToUTC(t *testing.T) {
	restore := time.Local
	t.Cleanup(func() { time.Local = restore })

	_, err := Load()
	require.NoError(t, err)
	assert.Equal(t, time.UTC, time.Local)
}

func TestLoad_AppliesConfiguredTimezone(t *testing.T) {
	restore := time.Local
	t.Cleanup(func() { time.Local = restore })

	t.Setenv("APP_TIMEZONE", "Europe/Berlin")
	_, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "Europe/Berlin", time.Local.String())
}

func TestLoad_InvalidTimezoneFails(t *testing.T) {
	restore := time.Local
	t.Cleanup(func() { time.Local = restore })

	t.Setenv("APP_TIMEZONE", "Mars/Olympus_Mons")
	_, err := Load()
	require.Error(t, err)
}
