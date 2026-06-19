package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_DatabaseURL_PrefersFile(t *testing.T) {
	// URLFile holds the file's contents (env resolves the ,file option).
	got, err := Config{URL: "from-env", URLFile: "from-file\n"}.databaseURL()
	require.NoError(t, err)
	assert.Equal(t, "from-file", got) // trimmed
}

func TestConfig_DatabaseURL_FallsBackToEnv(t *testing.T) {
	got, err := Config{URL: "from-env"}.databaseURL()
	require.NoError(t, err)
	assert.Equal(t, "from-env", got)
}

func TestConfig_DatabaseURL_Missing(t *testing.T) {
	_, err := Config{}.databaseURL()
	require.Error(t, err)
}
