package gamedata

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/token/ngram"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/single"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Kind is a searchable game-data category. Each kind is indexed separately, so a
// search never mixes results across kinds.
type Kind int

// The searchable categories. Each is indexed and queried in isolation.
const (
	KindItem Kind = iota
	KindContract
	KindFaction
	KindSpaceObject
)

// searchKinds is every category, for iterating (warmup, footprint measurement).
var searchKinds = []Kind{KindItem, KindContract, KindFaction, KindSpaceObject}

// maxGram bounds the indexed substring length: every 2..maxGram-character
// substring of a name is indexed, so a query no longer than this is a single
// indexed term (fast exact-substring match). Larger values explode the index
// (build time + memory) for diminishing benefit, since autocomplete queries are
// short; a longer query falls back to a whole-name wildcard scan instead.
const maxGram = 12

// Hit is one search result: the entity id and the localized name it matched on.
type Hit struct {
	ID   schema.GDID
	Name string
}

// Searcher provides fast substring autocomplete over a single game-data category
// at a time, in one language. It builds one in-memory bleve index per
// (kind, language) lazily on first use and caches it; the underlying Catalog data
// is immutable, so the indexes never need invalidation. Safe for concurrent use.
type Searcher struct {
	reg   *Registry
	mu    sync.Mutex
	cache map[cacheKey]bleve.Index
}

type cacheKey struct {
	kind Kind
	lang i18n.Language
}

// newSearcher is the pure constructor (no fx); the module wires lifecycle.
func newSearcher(reg *Registry) *Searcher {
	return &Searcher{reg: reg, cache: map[cacheKey]bleve.Index{}}
}

// Search returns up to limit hits for the given category whose localized name
// contains query as a (case-insensitive) substring, prefix matches ranked first.
// It searches the latest loaded version. Results are confined to the requested
// kind — never mixed. A blank query, non-positive limit, or empty catalog yields
// no hits.
func (s *Searcher) Search(kind Kind, lang i18n.Language, queryStr string, limit int) ([]Hit, error) {
	q := foldLower(queryStr)
	if q == "" || limit <= 0 {
		return nil, nil
	}
	idx, err := s.index(kind, lang)
	if err != nil || idx == nil {
		return nil, err
	}
	req := bleve.NewSearchRequestOptions(buildQuery(q), limit, 0, false)
	req.Fields = []string{"name"}
	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		name, _ := h.Fields["name"].(string)
		hits = append(hits, Hit{ID: schema.GDID(h.ID), Name: name})
	}
	return hits, nil
}

// Warm eagerly builds and caches every category index for each given language,
// so the first real query pays no build cost. Idempotent — an already-built
// index is reused. A nil/empty catalog is a no-op. Intended to run once in the
// background at startup over the server-pickable languages.
func (s *Searcher) Warm(langs []i18n.Language) error {
	for _, lang := range langs {
		for _, kind := range searchKinds {
			if _, err := s.index(kind, lang); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close releases every cached index. Wired to fx OnStop by the module.
func (s *Searcher) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for k, idx := range s.cache {
		if err := idx.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.cache, k)
	}
	return firstErr
}

// index returns the cached index for (kind, lang), building it on first use.
func (s *Searcher) index(kind Kind, lang i18n.Language) (bleve.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := cacheKey{kind, lang}
	if idx, ok := s.cache[key]; ok {
		return idx, nil
	}
	cat := s.reg.Latest()
	if cat == nil {
		return nil, nil
	}
	idx, err := buildIndex(catalogEntries(cat, kind, lang))
	if err != nil {
		return nil, err
	}
	s.cache[key] = idx
	return idx, nil
}

// buildQuery is a substring match on the ngram-indexed name field, with a prefix
// match on the exact (non-ngram) field added as a scoring boost so names that
// start with the query rank above mid-string matches.
func buildQuery(q string) query.Query {
	var main query.Query
	if utf8.RuneCountInString(q) <= maxGram {
		// Short query: it is itself an indexed ngram term — an O(1) lookup.
		tq := bleve.NewTermQuery(q)
		tq.SetField("name")
		main = tq
	} else {
		// Longer than any indexed ngram: scan the whole-name "exact" field, which
		// holds the full (un-ngrammed) name, so substrings of any length match.
		wq := bleve.NewWildcardQuery("*" + q + "*")
		wq.SetField("exact")
		main = wq
	}
	pq := bleve.NewPrefixQuery(q)
	pq.SetField("exact")
	pq.SetBoost(2)

	b := bleve.NewBooleanQuery()
	b.AddMust(main)
	b.AddShould(pq)
	return b
}

type entry struct {
	id   schema.GDID
	name string
}

// catalogEntries lists the (id, localized name) pairs for one category. Entries
// with no localized name are skipped — there is nothing to match on.
func catalogEntries(cat *Catalog, kind Kind, lang i18n.Language) []entry {
	var out []entry
	add := func(id schema.GDID, name string) {
		if name != "" {
			out = append(out, entry{id: id, name: name})
		}
	}
	switch kind {
	case KindItem:
		for _, it := range cat.Items() {
			add(it.ID, cat.Name(it.ID, lang))
		}
	case KindContract:
		for _, ct := range cat.Contracts() {
			add(ct.ID, cat.ContractName(ct.ID, lang))
		}
	case KindFaction:
		for _, code := range cat.FactionCodes() {
			add(code, cat.FactionName(string(code), lang))
		}
	case KindSpaceObject:
		for _, so := range cat.SpaceObjects() {
			add(so.ID, cat.SpaceObjectName(so.ID, lang))
		}
	}
	return out
}

// buildIndex creates a mem-only index over the entries: the id is the bleve doc
// id, the localized name is indexed twice — ngrammed (substring) and whole
// (prefix-boost) — and stored for display.
func buildIndex(entries []entry) (bleve.Index, error) {
	im, err := substrMapping()
	if err != nil {
		return nil, err
	}
	idx, err := bleve.NewMemOnly(im)
	if err != nil {
		return nil, err
	}
	batch := idx.NewBatch()
	for _, e := range entries {
		doc := map[string]any{"name": e.name, "exact": e.name}
		if err := batch.Index(string(e.id), doc); err != nil {
			return nil, err
		}
	}
	if err := idx.Batch(batch); err != nil {
		return nil, err
	}
	return idx, nil
}

// substrMapping builds the index mapping: a custom analyzer that lowercases the
// whole field and emits every 2..maxGram-character substring (so a term lookup is
// an exact substring match), plus an "exact" analyzer that only lowercases the
// whole field (for the prefix-rank boost).
func substrMapping() (mapping.IndexMapping, error) {
	im := bleve.NewIndexMapping()
	if err := im.AddCustomTokenFilter("gd_ngram", map[string]any{
		"type": ngram.Name,
		"min":  2.0,
		"max":  float64(maxGram),
	}); err != nil {
		return nil, err
	}
	if err := im.AddCustomAnalyzer("gd_substr", map[string]any{
		"type":          custom.Name,
		"tokenizer":     single.Name,
		"token_filters": []string{lowercase.Name, "gd_ngram"},
	}); err != nil {
		return nil, err
	}
	if err := im.AddCustomAnalyzer("gd_exact", map[string]any{
		"type":          custom.Name,
		"tokenizer":     single.Name,
		"token_filters": []string{lowercase.Name},
	}); err != nil {
		return nil, err
	}

	nameField := mapping.NewTextFieldMapping()
	nameField.Analyzer = "gd_substr"
	nameField.Store = true
	nameField.IncludeInAll = false
	nameField.IncludeTermVectors = false

	exactField := mapping.NewTextFieldMapping()
	exactField.Analyzer = "gd_exact"
	exactField.Store = false
	exactField.IncludeInAll = false
	exactField.IncludeTermVectors = false

	doc := bleve.NewDocumentMapping()
	doc.Dynamic = false
	doc.AddFieldMappingsAt("name", nameField)
	doc.AddFieldMappingsAt("exact", exactField)

	im.DefaultMapping = doc
	im.DefaultAnalyzer = "gd_substr"
	return im, nil
}

// foldLower normalizes a query for matching: trimmed and lowercased (bleve
// lowercases the indexed terms too), so search is case-insensitive across Latin
// and Cyrillic.
func foldLower(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
