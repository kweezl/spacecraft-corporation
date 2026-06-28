package emoji

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidEmojiName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"simple", "credits", true},
		{"with_underscore", "iron_bar", true},
		{"digits", "h2o", true},
		{"min length two", "ab", true},
		{"max length 32", strings.Repeat("a", 32), true},
		{"too short one char", "a", false},
		{"empty", "", false},
		{"too long 33", strings.Repeat("a", 33), false},
		{"space", "iron bar", false},
		{"hyphen", "iron-bar", false},
		{"punctuation", "bad!", false},
		{"non-ascii letter", "café", false},
		{"emoji rune", "fire🔥", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, validEmojiName(tc.in))
		})
	}
}

func TestLoadAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"a/iron_bar.png": {Data: []byte{0x89, 0x50}}, // bytes are opaque to loader
		"a/spin.gif":     {Data: []byte{0x47, 0x49}},
		"a/photo.JPG":    {Data: []byte{0xff, 0xd8}}, // upper-case ext still matched
		"a/README.md":    {Data: []byte("docs")},     // non-image: skipped
		"a/notes.txt":    {Data: []byte("nope")},     // non-image: skipped
	}

	out, err := loadAssets(fsys, "a")
	require.NoError(t, err)

	require.Len(t, out, 3)
	assert.Contains(t, out, "iron_bar")
	assert.Contains(t, out, "spin")
	assert.Contains(t, out, "photo")
	assert.NotContains(t, out, "README")
	assert.NotContains(t, out, "notes")

	// PNG bytes 0x89 0x50 base64-encode to "iVA".
	assert.Equal(t, "data:image/png;base64,iVA=", out["iron_bar"])
	assert.True(t, len(out["spin"]) > len("data:image/gif;base64,"))
	assert.Contains(t, out["photo"], "data:image/jpeg;base64,")
}

func TestLoadAssetsRejectsInvalidName(t *testing.T) {
	fsys := fstest.MapFS{"a/bad name!.png": {Data: []byte{0x1}}}
	_, err := loadAssets(fsys, "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid emoji name")
}

func TestLoadAssetsEmptyDir(t *testing.T) {
	fsys := fstest.MapFS{"a/README.md": {Data: []byte("only docs")}}
	out, err := loadAssets(fsys, "a")
	require.NoError(t, err)
	assert.Empty(t, out)
}

// The embedded assets directory must always load (it backs the production path).
func TestEmbeddedAssetsLoad(t *testing.T) {
	out, err := loadAssets(assetsFS, assetsRoot)
	require.NoError(t, err)
	assert.NotNil(t, out)
}
