package feature

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_UnsetEnablesAll(t *testing.T) {
	os.Unsetenv("FEATURES")
	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, All(), got)
}

func TestLoad_ParsesSubset(t *testing.T) {
	t.Setenv("FEATURES", "ping")
	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, []Name{Ping}, got)
}

func TestLoad_EmptyDisablesAll(t *testing.T) {
	t.Setenv("FEATURES", "")
	got, err := Load()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLoad_RejectsUnknownFeature(t *testing.T) {
	t.Setenv("FEATURES", "ping,bogus")
	_, err := Load()
	require.Error(t, err)
}

// fakeCatalog builds a lookup over a synthetic dependency graph so the resolver
// can be tested independently of the real (currently dep-free) catalog.
func fakeCatalog(features ...Feature) func(Name) (Feature, bool) {
	index := make(map[Name]Feature, len(features))
	for _, f := range features {
		index[f.Name] = f
	}
	return func(n Name) (Feature, bool) {
		f, ok := index[n]
		return f, ok
	}
}

func TestResolveWith_PullsInTransitiveDeps(t *testing.T) {
	lookup := fakeCatalog(
		Feature{Name: "a", Requires: []Name{"b"}},
		Feature{Name: "b", Requires: []Name{"c"}},
		Feature{Name: "c"},
	)
	got, err := resolveWith([]Name{"a"}, lookup)
	require.NoError(t, err)
	// Dependencies come before the feature that pulls them in.
	assert.Equal(t, []Name{"c", "b", "a"}, got)
}

func TestResolveWith_Deduplicates(t *testing.T) {
	lookup := fakeCatalog(
		Feature{Name: "a", Requires: []Name{"c"}},
		Feature{Name: "b", Requires: []Name{"c"}},
		Feature{Name: "c"},
	)
	got, err := resolveWith([]Name{"a", "b"}, lookup)
	require.NoError(t, err)
	assert.Equal(t, []Name{"c", "a", "b"}, got)
}

func TestResolveWith_UnknownDepErrors(t *testing.T) {
	lookup := fakeCatalog(
		Feature{Name: "a", Requires: []Name{"missing"}},
	)
	_, err := resolveWith([]Name{"a"}, lookup)
	require.Error(t, err)
}

func TestResolveWith_CycleTolerated(t *testing.T) {
	lookup := fakeCatalog(
		Feature{Name: "a", Requires: []Name{"b"}},
		Feature{Name: "b", Requires: []Name{"a"}},
	)
	got, err := resolveWith([]Name{"a"}, lookup)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Name{"a", "b"}, got)
}
