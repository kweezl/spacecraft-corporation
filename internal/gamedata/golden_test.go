package gamedata

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// goldenV1 builds a registry over the real generated data.
func goldenV1(t *testing.T) *Catalog {
	t.Helper()
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	require.NoError(t, err)
	c, ok := reg.Version("v1")
	require.True(t, ok, "v1 must be defined")
	return c
}

func TestV1KnownItem(t *testing.T) {
	c := goldenV1(t)

	ore, ok := c.Item("IronOre")
	require.True(t, ok)
	assert.NotEmpty(t, ore.IconName, "IronOre should have an icon")
	assert.Equal(t, "Iron Ore", c.Name("IronOre", i18n.LanguageEN))
	assert.NotEmpty(t, c.Name("IronOre", i18n.LanguageRU), "IronOre should have a Russian name")
}

func TestV1LocalizedContractFactionSpaceObject(t *testing.T) {
	c := goldenV1(t)
	assert.Equal(t, "Iron Ingots", c.ContractName("Tuto_Material_Iron", i18n.LanguageEN))
	assert.Equal(t, "The Company", c.FactionName("TheCo", i18n.LanguageEN))
	assert.Equal(t, "Babylon", c.SpaceObjectName("Station_Start", i18n.LanguageEN))
	// Contracts are localized in Russian too.
	assert.NotEmpty(t, c.ContractName("Tuto_Material_Iron", i18n.LanguageRU))
	// The stale "Tuto"/"Deprecated" templates are excluded by the generator.
	assert.Empty(t, c.ContractName("Tuto", i18n.LanguageEN))
	assert.Empty(t, c.ContractName("Deprecated", i18n.LanguageEN))
}

func TestV1SearcherRealData(t *testing.T) {
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	require.NoError(t, err)
	searcher := newSearcher(reg)
	t.Cleanup(func() { require.NoError(t, searcher.Close()) })

	// "ingot" matches contract titles (e.g. "Iron Ingots") in the contract index.
	contracts, err := searcher.Search(KindContract, i18n.LanguageEN, "ingot", 25)
	require.NoError(t, err)
	assert.NotEmpty(t, contracts, "expected contract titles containing 'ingot'")

	// The same query against items must not return any contract id, and vice
	// versa — categories never mix.
	items, err := searcher.Search(KindItem, i18n.LanguageEN, "ingot", 25)
	require.NoError(t, err)
	contractIDs := map[schema.GDID]bool{}
	for _, h := range contracts {
		contractIDs[h.ID] = true
	}
	for _, h := range items {
		assert.Falsef(t, contractIDs[h.ID], "item search leaked contract id %s", h.ID)
	}
}

// TestV1TranslationCoverage asserts every kept entity has a name in EVERY known
// game language. It reads the raw per-language tables directly — not the
// Catalog accessors, which fall back to the default language and would hide a
// missing translation. Guards against upstream localization gaps (and against
// junk entities, which are excluded by the generator rather than left untranslated).
func TestV1TranslationCoverage(t *testing.T) {
	c := goldenV1(t)
	langs := i18n.KnownLanguages()
	require.NotEmpty(t, langs)

	cover := func(kind string, ids []schema.GDID, table map[i18n.Language]map[schema.GDID]string) {
		require.NotEmptyf(t, ids, "%s: expected some ids", kind)
		for _, lang := range langs {
			m := table[lang]
			for _, id := range ids {
				assert.NotEmptyf(t, m[id], "%s %q has no %s translation", kind, id, lang)
			}
		}
	}

	itemIDs := make([]schema.GDID, 0, len(c.Items()))
	for _, it := range c.Items() {
		itemIDs = append(itemIDs, it.ID)
	}
	contractIDs := make([]schema.GDID, 0, len(c.Contracts()))
	for _, ct := range c.Contracts() {
		contractIDs = append(contractIDs, ct.ID)
	}
	spaceObjIDs := make([]schema.GDID, 0, len(c.SpaceObjects()))
	for _, so := range c.SpaceObjects() {
		spaceObjIDs = append(spaceObjIDs, so.ID)
	}

	cover("item", itemIDs, c.names)
	cover("contract", contractIDs, c.contractNames)
	cover("faction", c.FactionCodes(), c.factionNames)
	cover("spaceobject", spaceObjIDs, c.spaceObjectNames)
}

func TestV1SafetyNetRescuedCurrencies(t *testing.T) {
	c := goldenV1(t)
	for _, id := range []schema.GDID{"CorpoCredits", "LicensePoints", "CorpoReputation"} {
		_, ok := c.Item(id)
		assert.Truef(t, ok, "%s must survive exclusion (referenced by a contract)", id)
	}
}

func TestV1LatestIsV1(t *testing.T) {
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, reg.Latest())
	assert.Equal(t, "v1", reg.Latest().Version())
}

// TestV1IconAssetsExist guards against drift between the catalog and the emoji
// assets the generator copies: every item that claims an icon must have a
// committed asset file the emoji module can upload.
func TestV1IconAssetsExist(t *testing.T) {
	c := goldenV1(t)
	assetsDir := filepath.Join("..", "emoji", "assets")

	missing := 0
	for _, it := range c.Items() {
		if it.IconName == "" {
			continue
		}
		path := filepath.Join(assetsDir, it.IconName+".webp")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("item %s: icon asset %s missing", it.ID, path)
			missing++
		}
	}
	assert.Zero(t, missing)
}
