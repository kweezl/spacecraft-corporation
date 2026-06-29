package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

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
		if _, statErr := os.Stat(dst); statErr == nil {
			present++
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(srcDir, "icons", file))
		if readErr != nil {
			return copied, present, fmt.Errorf("read icon %s: %w", file, readErr)
		}
		if writeErr := os.WriteFile(dst, b, 0o644); writeErr != nil {
			return copied, present, fmt.Errorf("write icon %s: %w", dst, writeErr)
		}
		copied++
	}
	return copied, present, nil
}
