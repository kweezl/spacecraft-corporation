package gamedata

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

type SearchSuite struct {
	suite.Suite
	searcher *Searcher
}

func TestSearchSuite(t *testing.T) {
	suite.Run(t, new(SearchSuite))
}

// searchSrc seeds every searchable category. "Iron" deliberately appears in both
// an item ("Iron Ore") and a contract ("Iron Ingots") so the per-category
// isolation tests are meaningful. "Scrap Iron" puts the term mid-string so the
// prefix-ranking test has a non-prefix competitor.
func searchSrc() source {
	return source{
		version: "v1",
		items: map[schema.GDID]schema.Item{
			"IronOre":   {ID: "IronOre"},
			"ScrapIron": {ID: "ScrapIron"},
			"Copper":    {ID: "Copper"},
			"LongHull":  {ID: "LongHull"},
		},
		contracts: map[schema.GDID]schema.Contract{
			"Kits":     {ID: "Kits"},
			"IronDeal": {ID: "IronDeal"},
		},
		spaceObjects: map[schema.GDID]schema.SpaceObject{
			"Station_Start": {ID: "Station_Start"},
		},
		names: map[i18n.Language]map[schema.GDID]string{
			i18n.LanguageEN: {
				"IronOre":   "Iron Ore",
				"ScrapIron": "Scrap Iron",
				"Copper":    "Copper Ingot",
				// A name longer than maxGram, to exercise the wildcard fallback.
				"LongHull": "Reinforced Bulkhead Assembly",
			},
			i18n.LanguageRU: {"IronOre": "Железная руда"},
		},
		contractNames: map[i18n.Language]map[schema.GDID]string{
			i18n.LanguageEN: {"Kits": "Module Kits", "IronDeal": "Iron Ingots"},
		},
		factionNames: map[i18n.Language]map[schema.GDID]string{
			i18n.LanguageEN: {"TheCo": "The Company", "Eris": "Eris"},
		},
		spaceObjectNames: map[i18n.Language]map[schema.GDID]string{
			i18n.LanguageEN: {"Station_Start": "Babylon"},
		},
	}
}

func (s *SearchSuite) SetupTest() {
	reg, err := buildRegistry([]string{"v1"}, map[string]source{"v1": searchSrc()}, zap.NewNop())
	s.Require().NoError(err)
	s.searcher = newSearcher(reg)
}

func (s *SearchSuite) TearDownTest() {
	s.Require().NoError(s.searcher.Close())
}

// ids returns the result ids as a set for order-independent assertions.
func (s *SearchSuite) ids(kind Kind, lang i18n.Language, q string, limit int) map[schema.GDID]bool {
	hits, err := s.searcher.Search(kind, lang, q, limit)
	s.Require().NoError(err)
	set := map[schema.GDID]bool{}
	for _, h := range hits {
		set[h.ID] = true
	}
	return set
}

func (s *SearchSuite) TestSubstringMidWord() {
	// "ron" is interior to both Iron Ore and Scrap Iron.
	got := s.ids(KindItem, i18n.LanguageEN, "ron", 10)
	s.Equal(map[schema.GDID]bool{"IronOre": true, "ScrapIron": true}, got)
}

func (s *SearchSuite) TestCaseInsensitive() {
	got := s.ids(KindItem, i18n.LanguageEN, "IRON", 10)
	s.True(got["IronOre"])
	s.True(got["ScrapIron"])
}

func (s *SearchSuite) TestPrefixRankedFirst() {
	hits, err := s.searcher.Search(KindItem, i18n.LanguageEN, "iron", 10)
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(len(hits), 2)
	// "Iron Ore" starts with the query; "Scrap Iron" only contains it.
	s.Equal(schema.GDID("IronOre"), hits[0].ID)
}

func (s *SearchSuite) TestRussianSubstring() {
	got := s.ids(KindItem, i18n.LanguageRU, "руда", 10)
	s.Equal(map[schema.GDID]bool{"IronOre": true}, got)
}

func (s *SearchSuite) TestKindIsolation() {
	// An item query must never surface the "Iron Ingots" contract...
	items := s.ids(KindItem, i18n.LanguageEN, "iron", 10)
	s.False(items["IronDeal"])
	s.True(items["IronOre"])

	// ...and a contract query must never surface the items.
	contracts := s.ids(KindContract, i18n.LanguageEN, "iron", 10)
	s.Equal(map[schema.GDID]bool{"IronDeal": true}, contracts)
}

func (s *SearchSuite) TestFactionAndSpaceObject() {
	s.Equal(map[schema.GDID]bool{"TheCo": true}, s.ids(KindFaction, i18n.LanguageEN, "compan", 10))
	s.Equal(map[schema.GDID]bool{"Station_Start": true}, s.ids(KindSpaceObject, i18n.LanguageEN, "baby", 10))
}

func (s *SearchSuite) TestLimitCaps() {
	hits, err := s.searcher.Search(KindItem, i18n.LanguageEN, "ron", 1)
	s.Require().NoError(err)
	s.Len(hits, 1) // two match, but limit is 1
}

func (s *SearchSuite) TestLongQueryWildcardFallback() {
	// "Bulkhead Assembly" (17 chars) is longer than maxGram, so it takes the
	// whole-name wildcard path instead of an ngram term lookup.
	got := s.ids(KindItem, i18n.LanguageEN, "Bulkhead Assembly", 10)
	s.Equal(map[schema.GDID]bool{"LongHull": true}, got)
}

func (s *SearchSuite) TestBlankQueryEmpty() {
	hits, err := s.searcher.Search(KindItem, i18n.LanguageEN, "   ", 10)
	s.Require().NoError(err)
	s.Empty(hits)
}

func (s *SearchSuite) TestNonPositiveLimitEmpty() {
	hits, err := s.searcher.Search(KindItem, i18n.LanguageEN, "iron", 0)
	s.Require().NoError(err)
	s.Empty(hits)
}

func (s *SearchSuite) TestWarmBuildsAllIndexes() {
	langs := []i18n.Language{i18n.LanguageEN, i18n.LanguageRU}
	s.Require().NoError(s.searcher.Warm(langs))
	// One cached index per (kind, language).
	s.Len(s.searcher.cache, len(langs)*len(searchKinds))
	// Idempotent — a second warm reuses the cache, no growth.
	s.Require().NoError(s.searcher.Warm(langs))
	s.Len(s.searcher.cache, len(langs)*len(searchKinds))
}

func (s *SearchSuite) TestNoVersionEmpty() {
	reg, err := buildRegistry(nil, map[string]source{"v1": searchSrc()}, zap.NewNop())
	s.Require().NoError(err)
	s.Require().Nil(reg.Latest())
	searcher := newSearcher(reg)
	hits, err := searcher.Search(KindItem, i18n.LanguageEN, "iron", 10)
	s.Require().NoError(err)
	s.Empty(hits)
}
