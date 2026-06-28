package emoji

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// present reports whether the store has an emoji by that name (test helper that
// discards the token from Format's comma-ok return).
func present(s *Store, name string) bool {
	_, ok := s.Format(name)
	return ok
}

// formatted returns just the token for name (test helper that discards ok).
func formatted(s *Store, name string) string {
	tok, _ := s.Format(name)
	return tok
}

func TestStoreFormat(t *testing.T) {
	s := newStore()

	tok, ok := s.Format("missing")
	assert.False(t, ok, "unknown name reports not found")
	assert.Equal(t, "", tok, "token is empty when not found")

	s.replace(map[string]string{"iron_bar": "<:iron_bar:1>", "credits": "<:credits:2>"})

	tok, ok = s.Format("iron_bar")
	assert.True(t, ok)
	assert.Equal(t, "<:iron_bar:1>", tok)

	tok, ok = s.Format("unknown")
	assert.False(t, ok)
	assert.Equal(t, "", tok)
}

func TestStoreReplaceSwapsWholeMap(t *testing.T) {
	s := newStore()
	s.replace(map[string]string{"a": "<:a:1>"})
	s.replace(map[string]string{"b": "<:b:2>"})

	assert.False(t, present(s, "a"), "replace swaps the map, not merges")
	assert.True(t, present(s, "b"))
}
