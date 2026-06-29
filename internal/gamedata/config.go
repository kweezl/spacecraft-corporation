package gamedata

import (
	"os"
	"strings"
)

// versionsEnv is the allowlist of versions to load. Comma-separated (e.g.
// "v1,v2"); a version's ancestors load automatically. Unset = load every
// defined version; set-but-empty = load none.
const versionsEnv = "GAMEDATA_VERSIONS"

// loadRequestedVersions parses GAMEDATA_VERSIONS. present is false when the var
// is unset (distinct from set-but-empty), so the caller can default to all.
func loadRequestedVersions() (versions []string, present bool) {
	raw, ok := os.LookupEnv(versionsEnv)
	return loadRequestedVersionsFrom(raw, ok)
}

// loadRequestedVersionsFrom is the env-free core, split out for testing.
func loadRequestedVersionsFrom(raw string, present bool) ([]string, bool) {
	if !present {
		return nil, false
	}
	var versions []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			versions = append(versions, p)
		}
	}
	return versions, true
}
