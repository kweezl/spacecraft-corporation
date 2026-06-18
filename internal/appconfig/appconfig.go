// Package appconfig provides the shared application identity (name + version).
// It is injected into any module that wants it and knows nothing about other
// modules' settings.
package appconfig

import (
	"github.com/kweezl/spacecraft-cadet/internal/config"
	"go.uber.org/fx"
)

// version is injected at build time via:
//
//	-ldflags "-X github.com/kweezl/spacecraft-cadet/internal/appconfig.version=<ver>"
//
// It is intentionally a package var, not an env var.
var version = "dev"

// AppConfig is the shared, read-only application identity. Name comes from env;
// Version is not env-sourced (env:"-") and is filled from the build-time
// `version` var, since ldflags -X can only target a package var, not a field.
type AppConfig struct {
	Name    string `env:"APP_NAME" envDefault:"spacecraft-cadet"`
	Version string `env:"-"`
}

// Load builds AppConfig from APP_NAME (env) and the build-time version.
func Load() (AppConfig, error) {
	c, err := config.Parse[AppConfig]()
	if err != nil {
		return AppConfig{}, err
	}
	c.Version = version
	return c, nil
}

// Module exposes AppConfig to the fx graph.
var Module = fx.Module("appconfig", fx.Provide(Load))
