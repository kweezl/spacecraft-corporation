package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// lfsPointerPrefix is the first line of a Git LFS pointer file. The resources
// repo tracks icons via git-lfs; if the source was cloned without git-lfs the
// icons are these text stubs, not images. Copying one into the emoji assets
// bakes a stub the bot later fails to upload ("Invalid Asset", 50046), so we
// refuse to copy — or to silently keep an already-present — pointer here.
var lfsPointerPrefix = []byte("version https://git-lfs.github.com/spec/v1")

func isLFSPointer(b []byte) bool { return bytes.HasPrefix(b, lfsPointerPrefix) }

// copyIcons copies each referenced canonical icon webp from the source into the
// emoji assets dir. It is ADDITIVE and never overwrites or deletes: an icon used
// by an older version must survive even after a newer version drops the item, so
// the asset set is the union across every version. Returns counts of newly
// copied vs already-present files.
func copyIcons(icons map[string]string, srcDir, assetsDir string) (copied, present int, err error) {
	if err = os.MkdirAll(assetsDir, 0o755); err != nil {
		return 0, 0, err
	}
	files := make([]string, 0, len(icons))
	for _, file := range icons {
		files = append(files, file)
	}
	sort.Strings(files)

	seen := map[string]bool{}
	for _, file := range files {
		if seen[file] {
			continue
		}
		seen[file] = true
		dst := filepath.Join(assetsDir, file)
		if existing, statErr := os.ReadFile(dst); statErr == nil {
			// Present already (additive union across versions): leave it, but a
			// pointer stub committed by an earlier unsmudged run would silently
			// survive the skip, so surface it instead of trusting it.
			if isLFSPointer(existing) {
				return copied, present, fmt.Errorf("asset %s is a Git LFS pointer, not an image"+
					" (re-copy from a git-lfs-smudged source): %s", file, dst)
			}
			present++
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(srcDir, "icons", file))
		if readErr != nil {
			return copied, present, fmt.Errorf("read icon %s: %w", file, readErr)
		}
		if isLFSPointer(b) {
			return copied, present, fmt.Errorf("source icon %s is a Git LFS pointer, not an image"+
				" — run `git lfs pull` in the resources repo before regenerating", file)
		}
		if writeErr := os.WriteFile(dst, b, 0o644); writeErr != nil {
			return copied, present, fmt.Errorf("write icon %s: %w", dst, writeErr)
		}
		copied++
	}
	return copied, present, nil
}
