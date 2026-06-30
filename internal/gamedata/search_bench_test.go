package gamedata

import (
	"testing"

	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Benchmarks run against the real generated v1 data — the biggest sets the bot
// actually searches: ~442 items and 206 contracts (factions/space objects are
// tiny but included for contrast). Two costs matter for autocomplete: the lazy
// per-(kind,language) index build that happens on the first keystroke, and the
// warm query that runs on every subsequent one.

func benchRegistry(b *testing.B) *Registry {
	b.Helper()
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	return reg
}

// BenchmarkIndexBuild isolates the cost of building one category's bleve index
// (entries precomputed, so this is pure index construction).
func BenchmarkIndexBuild(b *testing.B) {
	cat, ok := benchRegistry(b).Version("v1")
	if !ok {
		b.Fatal("v1 not defined")
	}
	for _, c := range []struct {
		name string
		kind Kind
	}{
		{"items", KindItem},
		{"contracts", KindContract},
		{"factions", KindFaction},
		{"spaceobjects", KindSpaceObject},
	} {
		entries := catalogEntries(cat, c.kind, i18n.LanguageEN)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				idx, err := buildIndex(entries)
				if err != nil {
					b.Fatal(err)
				}
				if err := idx.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSearchCold measures the first-keystroke latency: a fresh Searcher
// (cold cache) builds the index and runs the query. This is the worst case a
// user ever sees for a given category+language.
func BenchmarkSearchCold(b *testing.B) {
	reg := benchRegistry(b)
	for _, c := range []struct {
		name string
		kind Kind
		q    string
	}{
		{"items", KindItem, "iron"},
		{"contracts", KindContract, "module"},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s := newSearcher(reg)
				if _, err := s.Search(c.kind, i18n.LanguageEN, c.q, 25); err != nil {
					b.Fatal(err)
				}
				if err := s.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSearchWarm measures the steady-state per-keystroke query against a
// pre-built (cached) index, across query shapes: a short prefix, a mid-string
// substring, a longer multi-word query, and a Cyrillic substring (the multibyte
// ngram path).
func BenchmarkSearchWarm(b *testing.B) {
	s := newSearcher(benchRegistry(b))
	b.Cleanup(func() { _ = s.Close() })

	for _, c := range []struct {
		name string
		kind Kind
		lang i18n.Language
		q    string
	}{
		{"items/prefix", KindItem, i18n.LanguageEN, "ir"},
		{"items/substr", KindItem, i18n.LanguageEN, "ron"},
		{"items/long", KindItem, i18n.LanguageEN, "reinforced"},
		{"items/cyrillic", KindItem, i18n.LanguageRU, "руд"},
		{"contracts/prefix", KindContract, i18n.LanguageEN, "mod"},
		{"contracts/substr", KindContract, i18n.LanguageEN, "ngot"},
	} {
		// Warm the cache so the timed loop measures only the query.
		if _, err := s.Search(c.kind, c.lang, c.q, 25); err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.Search(c.kind, c.lang, c.q, 25); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
