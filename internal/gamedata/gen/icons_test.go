package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lfsPointer = "version https://git-lfs.github.com/spec/v1\n" +
	"oid sha256:66764a4076981356e6290f899f878fba8c222ad42ad67afc1474750615389f77\n" +
	"size 1584\n"

func TestCopyIconsCopiesRealBytes(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "icons"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "icons", "Iron.webp"), []byte("RIFF....WEBP"), 0o644))

	copied, present, err := copyIcons(map[string]string{"iron": "Iron.webp"}, src, dst)
	require.NoError(t, err)
	assert.Equal(t, 1, copied)
	assert.Equal(t, 0, present)

	got, err := os.ReadFile(filepath.Join(dst, "Iron.webp"))
	require.NoError(t, err)
	assert.Equal(t, "RIFF....WEBP", string(got))
}

func TestCopyIconsRejectsLFSPointerSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "icons"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "icons", "Iron.webp"), []byte(lfsPointer), 0o644))

	_, _, err := copyIcons(map[string]string{"iron": "Iron.webp"}, src, dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Git LFS pointer")
	assert.Contains(t, err.Error(), "Iron.webp")

	// Nothing was written, so a fixed source heals on the next run.
	_, statErr := os.Stat(filepath.Join(dst, "Iron.webp"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestCopyIconsRejectsLFSPointerAlreadyPresent(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "icons"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "icons", "Iron.webp"), []byte("RIFF....WEBP"), 0o644))
	// A pointer stub committed by an earlier unsmudged run must not survive the
	// additive skip-if-present.
	require.NoError(t, os.WriteFile(filepath.Join(dst, "Iron.webp"), []byte(lfsPointer), 0o644))

	_, _, err := copyIcons(map[string]string{"iron": "Iron.webp"}, src, dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Git LFS pointer")
	assert.Contains(t, err.Error(), "Iron.webp")
}
