package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sample struct {
	Name string `env:"SAMPLE_NAME" envDefault:"fallback"`
}

func TestParse_UsesDefault(t *testing.T) {
	got, err := Parse[sample]()
	require.NoError(t, err)
	assert.Equal(t, "fallback", got.Name)
}

func TestParse_ReadsEnv(t *testing.T) {
	t.Setenv("SAMPLE_NAME", "explicit")
	got, err := Parse[sample]()
	require.NoError(t, err)
	assert.Equal(t, "explicit", got.Name)
}
