// Package config provides a small helper for loading per-module env config.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Parse loads a config struct of type T from environment variables using
// caarlos0/env struct tags. Each module calls Parse[ItsOwnConfig] so that no
// module is aware of any other module's env keys.
func Parse[T any]() (T, error) {
	var c T
	if err := env.Parse(&c); err != nil {
		var zero T
		return zero, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}
