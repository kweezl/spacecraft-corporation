// Package feature enumerates the optional feature modules and parses which are
// enabled from a single env var (FEATURES), instead of a bool per feature.
package feature

import (
	"fmt"
	"os"

	"github.com/caarlos0/env/v11"
)

// Name identifies an optional feature module.
type Name string

const (
	// Ping is the /ping feature.
	Ping Name = "ping"
)

// allNames is the ordered list of known features; it is also the default set
// when FEATURES is unset.
var allNames = []Name{Ping}

// known indexes allNames for validation.
var known = func() map[Name]struct{} {
	m := make(map[Name]struct{}, len(allNames))
	for _, n := range allNames {
		m[n] = struct{}{}
	}
	return m
}()

// All returns a copy of every known feature name.
func All() []Name {
	out := make([]Name, len(allNames))
	copy(out, allNames)
	return out
}

// UnmarshalText lets caarlos0/env parse []Name from a comma-separated list and
// rejects unknown names at parse time. Empty entries are tolerated (an empty
// FEATURES yields one empty element) and dropped by Selected.
func (n *Name) UnmarshalText(text []byte) error {
	candidate := Name(text)
	if candidate == "" {
		*n = ""
		return nil
	}
	if _, ok := known[candidate]; !ok {
		return fmt.Errorf("unknown feature %q", string(text))
	}
	*n = candidate
	return nil
}

// Config is the parsed FEATURES env var.
type Config struct {
	Enabled []Name `env:"FEATURES" envSeparator:","`
}

// Selected returns the enabled feature names with empty entries removed.
func (c Config) Selected() []Name {
	out := make([]Name, 0, len(c.Enabled))
	for _, n := range c.Enabled {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Load returns the enabled feature names. FEATURES unset enables all known
// features; FEATURES set enables exactly the listed (validated) names, with an
// empty value enabling none.
func Load() ([]Name, error) {
	if _, ok := os.LookupEnv("FEATURES"); !ok {
		return All(), nil
	}
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, err
	}
	return cfg.Selected(), nil
}
