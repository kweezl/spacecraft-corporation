package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

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

// TestOptions_GraphValidates resolves the full dependency graph (without
// starting it), so a broken wiring — e.g. the instrumentation readiness group,
// or a subsystem's ReadinessCheck provider — fails the build, not production.
func TestOptions_GraphValidates(t *testing.T) {
	t.Setenv("FEATURES", "ping")
	opts, err := Options(false)
	require.NoError(t, err)
	require.NoError(t, fx.ValidateApp(opts...))
}

func TestOptions_MigrateGraphValidates(t *testing.T) {
	opts, err := Options(true)
	require.NoError(t, err)
	require.NoError(t, fx.ValidateApp(opts...))
}

// TestOptions_BasesGraphValidates resolves the graph with the bases feature,
// which pulls in permissions (its Requires) and exercises the registry's
// components fx group — so a mis-wired component provider fails here.
func TestOptions_BasesGraphValidates(t *testing.T) {
	t.Setenv("FEATURES", "bases")
	opts, err := Options(false)
	require.NoError(t, err)
	require.NoError(t, fx.ValidateApp(opts...))
}

// TestOptions_ContractsGraphValidates resolves the graph with the contracts
// feature, which consumes core services across module boundaries (the session
// Manager as its Discord Gateway, the settings Store as its ForumConfig) and
// registers the expiry sweeper's lifecycle hook — so a mis-wired provider or a
// missing dependency fails here, not in production.
func TestOptions_ContractsGraphValidates(t *testing.T) {
	t.Setenv("FEATURES", "contracts")
	opts, err := Options(false)
	require.NoError(t, err)
	require.NoError(t, fx.ValidateApp(opts...))
}
