package gamedata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
)

func fakeDefined() map[string]source {
	return map[string]source{
		"v1": {version: "v1", parent: "", items: map[schema.GDID]schema.Item{"A": {ID: "A"}}},
		"v2": {version: "v2", parent: "v1", items: map[schema.GDID]schema.Item{"B": {ID: "B"}}},
		"v3": {version: "v3", parent: "v2", items: map[schema.GDID]schema.Item{"C": {ID: "C"}}},
	}
}

func TestRegistryLoadsAncestors(t *testing.T) {
	// Requesting only v3 must pull in v2 and v1 (the chain it overlays).
	reg, err := buildRegistry([]string{"v3"}, fakeDefined(), zap.NewNop())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"v1", "v2", "v3"}, reg.Loaded())
}

func TestRegistryLatestIsNewest(t *testing.T) {
	reg, err := buildRegistry([]string{"v1", "v2", "v3"}, fakeDefined(), zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, reg.Latest())
	assert.Equal(t, "v3", reg.Latest().Version())
}

func TestRegistrySkipsUnknownVersion(t *testing.T) {
	// v9 is undefined: it is warned and skipped, v1 still loads.
	reg, err := buildRegistry([]string{"v1", "v9"}, fakeDefined(), zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, []string{"v1"}, reg.Loaded())
	_, ok := reg.Version("v9")
	assert.False(t, ok)
}

func TestRegistryEmptyRequestLoadsNothing(t *testing.T) {
	reg, err := buildRegistry(nil, fakeDefined(), zap.NewNop())
	require.NoError(t, err)
	assert.Empty(t, reg.Loaded())
	assert.Nil(t, reg.Latest())
}

func TestParseRequestedVersions(t *testing.T) {
	t.Setenv(versionsEnv, " v1 , v2 ,")
	got, present := loadRequestedVersions()
	assert.True(t, present)
	assert.Equal(t, []string{"v1", "v2"}, got)
}

func TestParseRequestedVersionsUnset(t *testing.T) {
	// t.Setenv then unset isn't available; rely on the var being absent.
	got, present := loadRequestedVersionsFrom("", false)
	assert.False(t, present)
	assert.Nil(t, got)
}
