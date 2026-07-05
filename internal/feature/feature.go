// Package feature enumerates the optional feature modules and parses which are
// enabled from a single env var (FEATURES), instead of a bool per feature.
// A feature may declare other features it Requires; enabling it transitively
// enables those (order is irrelevant — fx resolves construction order).
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
	// Permissions is the role-based command access-control feature: it gates
	// commands behind per-server role mappings and adds /permissions to manage
	// them. Disabled = no gating (every command open).
	Permissions Name = "permissions"
	// Bases is the member-bases feature: /base register/unregister, equipment
	// management, and a paginated listing. Requires Permissions, since its
	// per-tier (own/corp/member) authorization is enforced by the role gate.
	Bases Name = "bases"
	// Contracts is the corporation-contracts feature: /contract create/participate/
	// deliver/release and a paginated listing, each contract a forum thread, with a
	// background sweeper closing expired ones. Requires Permissions, since its
	// per-leaf authorization is enforced by the role gate.
	Contracts Name = "contracts"
	// Supply is the member-supply-requests feature: /supply lets a member post a
	// personal "I need these items" request as a forum thread that any member can
	// reserve/deliver/release via buttons. Strictly self-scoped (ownership enforced
	// in SQL) with /supply access Discord-managed, so it needs no Permissions
	// feature — it works with FEATURES=supply alone.
	Supply Name = "supply"
)

// Feature describes an optional feature module and the features it requires.
type Feature struct {
	Name     Name
	Requires []Name
}

// catalog is the single source of truth for known features. It is a function
// (not a package var) so there is no global state; it is consulted only a
// handful of times at startup.
func catalog() []Feature {
	return []Feature{
		{Name: Ping},
		{Name: Permissions},
		{Name: Bases, Requires: []Name{Permissions}},
		{Name: Contracts, Requires: []Name{Permissions}},
		{Name: Supply}, // self-scoped + Discord-managed access: no Requires
	}
}

// lookup returns the Feature for a name.
func lookup(n Name) (Feature, bool) {
	for _, f := range catalog() {
		if f.Name == n {
			return f, true
		}
	}
	return Feature{}, false
}

// All returns every known feature name.
func All() []Name {
	cat := catalog()
	out := make([]Name, len(cat))
	for i, f := range cat {
		out[i] = f.Name
	}
	return out
}

// valid reports whether n is a known feature.
func valid(n Name) bool {
	_, ok := lookup(n)
	return ok
}

// resolve expands selected names to include all transitive required features,
// deduplicated. Dependencies appear before the features that pull them in;
// cycles are tolerated (each feature appears once).
func resolve(selected []Name) ([]Name, error) {
	return resolveWith(selected, lookup)
}

func resolveWith(selected []Name, lookupFn func(Name) (Feature, bool)) ([]Name, error) {
	seen := make(map[Name]bool)
	var out []Name

	var visit func(n Name) error
	visit = func(n Name) error {
		if seen[n] {
			return nil
		}
		f, ok := lookupFn(n)
		if !ok {
			return fmt.Errorf("unknown feature %q", n)
		}
		seen[n] = true
		for _, dep := range f.Requires {
			if err := visit(dep); err != nil {
				return err
			}
		}
		out = append(out, n)
		return nil
	}

	for _, n := range selected {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return out, nil
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
	if !valid(candidate) {
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

// Load returns the enabled feature names, including transitive dependencies.
// FEATURES unset enables all known features; FEATURES set enables exactly the
// listed (validated) names plus their requirements; an empty value enables none.
func Load() ([]Name, error) {
	if _, ok := os.LookupEnv("FEATURES"); !ok {
		return resolve(All())
	}
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, err
	}
	return resolve(cfg.Selected())
}
