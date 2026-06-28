package emoji

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStoreFormatAndHas(t *testing.T) {
	s := newStore()
	assert.Equal(t, "", s.Format("missing"), "unknown name renders as empty")
	assert.False(t, s.Has("missing"))

	s.replace(map[string]string{"iron_bar": "<:iron_bar:1>", "credits": "<:credits:2>"})

	assert.Equal(t, "<:iron_bar:1>", s.Format("iron_bar"))
	assert.True(t, s.Has("iron_bar"))
	assert.Equal(t, "", s.Format("unknown"), "still empty for an unknown name")
	assert.False(t, s.Has("unknown"))
}

func TestStoreReplaceSwapsWholeMap(t *testing.T) {
	s := newStore()
	s.replace(map[string]string{"a": "<:a:1>"})
	s.replace(map[string]string{"b": "<:b:2>"})

	assert.False(t, s.Has("a"), "replace swaps the map, not merges")
	assert.True(t, s.Has("b"))
}
