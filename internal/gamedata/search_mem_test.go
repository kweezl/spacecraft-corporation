package gamedata

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// retainedMB returns the heap still held after build() runs and a GC — i.e. the
// resident cost of whatever build() returns, in MB. The returned object is kept
// alive across the measurement so it isn't collected early.
func retainedMB(t *testing.T, build func() any) float64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	obj := build()

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(obj)

	var d uint64
	if after.HeapAlloc > before.HeapAlloc {
		d = after.HeapAlloc - before.HeapAlloc
	}
	return float64(d) / (1024 * 1024)
}

// TestSearchIndexMemoryFootprint measures the resident memory of holding the
// search indexes, to answer "how much do we need to keep all-language indexes?"
// It is a measurement (logged), with a loose upper bound to catch gross
// regressions — heap residency is noisy, so the bound is generous.
func TestSearchIndexMemoryFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("memory footprint measurement (slow: builds many indexes)")
	}
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	require.NoError(t, err)
	cat, ok := reg.Version("v1")
	require.True(t, ok)

	// Per-kind, one language (en) — where the cost lives.
	for _, k := range searchKinds {
		entries := catalogEntries(cat, k, i18n.LanguageEN)
		mb := retainedMB(t, func() any {
			idx, berr := buildIndex(entries)
			require.NoError(t, berr)
			return idx
		})
		t.Logf("kind=%-13s entries=%-4d resident≈%6.2f MB", kindName(k), len(entries), mb)
	}

	// All kinds, for increasing language sets.
	warm := func(langs []i18n.Language) func() any {
		return func() any {
			s := newSearcher(reg)
			require.NoError(t, s.Warm(langs))
			return s
		}
	}
	renderable := []i18n.Language{i18n.LanguageEN, i18n.LanguageRU} // the server-pickable set today
	all := i18n.KnownLanguages()                                    // all eight game languages

	en1 := retainedMB(t, warm([]i18n.Language{i18n.LanguageEN}))
	rend := retainedMB(t, warm(renderable))
	allMB := retainedMB(t, warm(all))

	t.Logf("all kinds — en(1 lang)≈%.1f MB | renderable(%d langs)≈%.1f MB | all-known(%d langs)≈%.1f MB",
		en1, len(renderable), rend, len(all), allMB)

	// Generous guard: even all eight languages should stay well under this.
	assert.Lessf(t, allMB, 512.0, "all-language search indexes resident ≈%.1f MB, expected < 512 MB", allMB)
}

func kindName(k Kind) string {
	switch k {
	case KindItem:
		return "item"
	case KindContract:
		return "contract"
	case KindFaction:
		return "faction"
	case KindSpaceObject:
		return "spaceobject"
	}
	return "unknown"
}
