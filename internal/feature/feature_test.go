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
