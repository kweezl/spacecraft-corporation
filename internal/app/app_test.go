package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/feature"
)

func TestSelectFeatures_Known(t *testing.T) {
	opts, err := selectFeatures([]feature.Name{feature.Ping})
	require.NoError(t, err)
	assert.Len(t, opts, 1)
}

func TestSelectFeatures_None(t *testing.T) {
	opts, err := selectFeatures(nil)
	require.NoError(t, err)
	assert.Empty(t, opts)
}

func TestSelectFeatures_UnregisteredFails(t *testing.T) {
	_, err := selectFeatures([]feature.Name{feature.Name("ghost")})
	require.Error(t, err)
}

func TestOptions_DefaultBuilds(t *testing.T) {
	t.Setenv("FEATURES", "ping")
	opts, err := Options(false)
	require.NoError(t, err)
	assert.NotEmpty(t, opts)
}

func TestOptions_MigrateBuilds(t *testing.T) {
	// Migrate mode ignores FEATURES entirely and never fails on them.
	t.Setenv("FEATURES", "ghost")
	opts, err := Options(true)
	require.NoError(t, err)
	assert.NotEmpty(t, opts)
}
