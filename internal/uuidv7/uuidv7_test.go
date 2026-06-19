package uuidv7_test

import (
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/uuidv7"
)

func TestNew_ReturnsParsableV7(t *testing.T) {
	s, err := uuidv7.New()
	require.NoError(t, err)

	id, err := uuid.Parse(s)
	require.NoError(t, err, "result must be a valid UUID string")
	assert.Equal(t, uuid.Version(7), id.Version(), "must be a UUIDv7")
	assert.Equal(t, uuid.RFC4122, id.Variant())
}

func TestNew_Unique(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for range n {
		s, err := uuidv7.New()
		require.NoError(t, err)
		_, dup := seen[s]
		require.False(t, dup, "ids must be unique")
		seen[s] = struct{}{}
	}
}

// TestNew_SortsByCreationOrder verifies the v7 time-ordering the package relies
// on for index locality: ids generated in sequence sort into that same order.
func TestNew_SortsByCreationOrder(t *testing.T) {
	const n = 50
	ids := make([]string, n)
	for i := range ids {
		s, err := uuidv7.New()
		require.NoError(t, err)
		ids[i] = s
	}

	assert.True(t, sort.StringsAreSorted(ids),
		"sequentially generated v7 ids should already be in ascending order")
}
