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

// envConfig holds only env-sourced fields.
type envConfig struct {
	Name string `env:"APP_NAME" envDefault:"spacecraft-cadet"`
}

// AppConfig is the shared, read-only application identity.
type AppConfig struct {
	Name    string
	Version string
}

// Load builds AppConfig from APP_NAME (env) and the build-time version.
func Load() (AppConfig, error) {
	c, err := config.Parse[envConfig]()
	if err != nil {
		return AppConfig{}, err
	}
	return AppConfig{Name: c.Name, Version: version}, nil
}

// Module exposes AppConfig to the fx graph.
var Module = fx.Module("appconfig", fx.Provide(Load))
