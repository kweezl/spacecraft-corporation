// Package appconfig provides the shared application identity (name + version)
// and pins the process-wide timezone. It is injected into any module that wants
// it and knows nothing about other modules' settings.
package appconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// version is injected at build time via:
//
//	-ldflags "-X github.com/kweezl/spacecraft-corporation/internal/appconfig.version=<ver>"
//
// It is intentionally a package var, not an env var.
var version = "0.0.0-unspecified"

// AppConfig is the shared, read-only application identity. Name comes from env;
// Version is not env-sourced (env:"-") and is filled from the build-time
// `version` var, since ldflags -X can only target a package var, not a field.
// OwnerDiscordID is the bot owner's Discord user ID (optional); modules use it to
// point users at who can approve a server.
type AppConfig struct {
	Name           string `env:"APP_NAME" envDefault:"spacecraft-corporation"`
	Version        string `env:"-"`
	OwnerDiscordID string `env:"APP_OWNER_DISCORD_ID"`
}

// tzConfig is parsed alongside AppConfig to fix the process-wide timezone.
// APP_TIMEZONE is an IANA name (e.g. "UTC", "Europe/Berlin"); it defaults to UTC
// (GMT+0) so the app does not inherit the host's local time. The zoneinfo
// database is embedded in the binary (see the time/tzdata import in cmd/bot), so
// named zones resolve even in a minimal container without OS tzdata.
type tzConfig struct {
	TimeZone string `env:"APP_TIMEZONE" envDefault:"UTC"`
}

// Load builds AppConfig from APP_NAME (env) and the build-time version, and pins
// time.Local to APP_TIMEZONE. Because the logger depends on AppConfig, fx runs
// Load before the logger is built, so timestamps render in the chosen zone.
func Load() (AppConfig, error) {
	c, err := env.ParseAs[AppConfig]()
	if err != nil {
		return AppConfig{}, err
	}
	c.Version = version
	if err := applyTimeZone(); err != nil {
		return AppConfig{}, err
	}
	if err := c.validateOwnerDiscordID(); err != nil {
		return AppConfig{}, err
	}
	return c, nil
}

// validateOwnerDiscordID trims and checks APP_OWNER_DISCORD_ID. It is optional
// (empty is fine, the feature is just off); when set it must be a Discord
// snowflake — a positive integer — since it is rendered as a `<@id>` mention.
func (c *AppConfig) validateOwnerDiscordID() error {
	c.OwnerDiscordID = strings.TrimSpace(c.OwnerDiscordID)
	if c.OwnerDiscordID == "" {
		return nil
	}
	if _, err := strconv.ParseUint(c.OwnerDiscordID, 10, 64); err != nil {
		return fmt.Errorf("appconfig: invalid APP_OWNER_DISCORD_ID %q: "+
			"must be a numeric Discord user ID (snowflake)", c.OwnerDiscordID)
	}
	return nil
}

// applyTimeZone sets time.Local from APP_TIMEZONE (default UTC).
func applyTimeZone() error {
	cfg, err := env.ParseAs[tzConfig]()
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return fmt.Errorf("appconfig: invalid APP_TIMEZONE %q: %w", cfg.TimeZone, err)
	}
	time.Local = loc
	return nil
}
