package emoji

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

//go:embed assets
var assetsFS embed.FS

const assetsRoot = "assets"

// loadAssets reads every image under root into a name→data-URI map, keyed by the
// filename without extension. The data URI ("data:image/png;base64,...") is the
// form ApplicationEmojiCreate expects. Non-image files are skipped; an invalid
// emoji name fails loudly so a bad asset is caught at startup, not at upload.
func loadAssets(fsys fs.FS, root string) (map[string]string, error) {
	// imageMIME maps the emoji image extensions Discord accepts to their MIME
	// type. Files with any other extension (e.g. the assets README) are ignored.
	imageMIME := map[string]string{
		".png":  "image/png",
		".gif":  "image/gif",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".webp": "image/webp",
	}
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("emoji: read assets: %w", err)
	}
	out := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(path.Ext(e.Name()))
		mime, ok := imageMIME[ext]
		if !ok {
			continue // not an emoji image (e.g. README.md)
		}
		name := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		if !validEmojiName(name) {
			return nil, fmt.Errorf("emoji: invalid emoji name %q (must be 2-32 chars of [A-Za-z0-9_])", name)
		}
		raw, err := fs.ReadFile(fsys, path.Join(root, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("emoji: read %s: %w", e.Name(), err)
		}
		if isLFSPointer(raw) {
			return nil, fmt.Errorf("emoji: %s is a Git LFS pointer, not image content"+
				" — the asset was embedded unsmudged; run `git lfs pull` at the source"+
				" and regenerate (make gamedata.gen)", e.Name())
		}
		out[name] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
	}
	return out, nil
}

// lfsPointerPrefix is the first line of a Git LFS pointer file. An asset checked
// out (or copied from a source) without git-lfs is this short text stub, not the
// image — base64-encoding one and sending it to Discord fails at upload with a
// cryptic "Invalid Asset" (50046), so we reject it loudly at load instead.
var lfsPointerPrefix = []byte("version https://git-lfs.github.com/spec/v1")

// isLFSPointer reports whether b is a Git LFS pointer stub rather than real
// image bytes.
func isLFSPointer(b []byte) bool { return bytes.HasPrefix(b, lfsPointerPrefix) }

// validEmojiName reports whether name satisfies Discord's emoji-name rule: 2–32
// characters, each a letter, digit, or underscore. The allowed set is ASCII, so
// byte length equals character count for any valid name.
func validEmojiName(name string) bool {
	if len(name) < 2 || len(name) > 32 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
		default:
			return false
		}
	}
	return true
}
