package gamedata

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// baseSrc/deltaSrc model a v1 base and a v2 delta layered on top.
func baseSrc() source {
	return source{
		version: "v1",
		items: map[schema.GDID]schema.Item{
			"IronOre": {ID: "IronOre", IconName: "IronOre", Price: 2},
			"OldItem": {ID: "OldItem"},
			"Keep":    {ID: "Keep"},
		},
		names: map[i18n.Language]map[schema.GDID]string{
			i18n.LanguageEN: {"IronOre": "Iron Ore", "Keep": "Keep"},
			i18n.LanguageRU: {"IronOre": "Железная руда"},
		},
		descs:         map[i18n.Language]map[schema.GDID]string{i18n.LanguageEN: {"IronOre": "ore"}},
		categories:    map[schema.GDID]schema.Category{"Minerals": {ID: "Minerals"}},
		contracts:     map[schema.GDID]schema.Contract{},
		spaceObjects:  map[schema.GDID]schema.SpaceObject{},
		categoryNames: map[i18n.Language]map[schema.GDID]string{i18n.LanguageEN: {"Minerals": "Minerals"}},
	}
}

func deltaSrc() source {
	return source{
		version:      "v2",
		parent:       "v1",
		removedItems: []schema.GDID{"OldItem"},
		items: map[schema.GDID]schema.Item{
			"IronOre": {ID: "IronOre", IconName: "IronOre", Price: 3}, // override
			"NewItem": {ID: "NewItem"},                                // add
		},
		names:         map[i18n.Language]map[schema.GDID]string{i18n.LanguageEN: {"NewItem": "New Item"}},
		descs:         map[i18n.Language]map[schema.GDID]string{},
		categories:    map[schema.GDID]schema.Category{},
		contracts:     map[schema.GDID]schema.Contract{},
		spaceObjects:  map[schema.GDID]schema.SpaceObject{},
		categoryNames: map[i18n.Language]map[schema.GDID]string{},
	}
}

func TestCatalogParentChain(t *testing.T) {
	base := newCatalog(baseSrc(), nil)
	v2 := newCatalog(deltaSrc(), base)

	// Inherited from parent.
	keep, ok := v2.Item("Keep")
	assert.True(t, ok)
	assert.Equal(t, schema.GDID("Keep"), keep.ID)

	// Overridden by the delta layer.
	ore, ok := v2.Item("IronOre")
	assert.True(t, ok)
	assert.EqualValues(t, 3, ore.Price)

	// Added by the delta layer.
	_, ok = v2.Item("NewItem")
	assert.True(t, ok)

	// Removed by the delta layer, even though the parent defines it.
	_, ok = v2.Item("OldItem")
	assert.False(t, ok)
	// ...but the parent still resolves it.
	_, ok = base.Item("OldItem")
	assert.True(t, ok)
}

func TestCatalogNameLangFallback(t *testing.T) {
	base := newCatalog(baseSrc(), nil)

	assert.Equal(t, "Iron Ore", base.Name("IronOre", i18n.LanguageEN))
	assert.Equal(t, "Железная руда", base.Name("IronOre", i18n.LanguageRU))
	// No German name -> falls back to the default (en).
	assert.Equal(t, "Iron Ore", base.Name("IronOre", i18n.LanguageDE))
	// Unknown item -> empty.
	assert.Equal(t, "", base.Name("Nope", i18n.LanguageEN))
}

func TestCatalogRemovedHasNoName(t *testing.T) {
	base := newCatalog(baseSrc(), nil)
	v2 := newCatalog(deltaSrc(), base)
	// Parent has no name for OldItem anyway, but removal must short-circuit.
	assert.Equal(t, "", v2.Name("OldItem", i18n.LanguageEN))
}

func TestCatalogItemsFlattens(t *testing.T) {
	base := newCatalog(baseSrc(), nil)
	v2 := newCatalog(deltaSrc(), base)

	ids := map[schema.GDID]bool{}
	for _, it := range v2.Items() {
		ids[it.ID] = true
	}
	assert.Equal(t, map[schema.GDID]bool{"IronOre": true, "Keep": true, "NewItem": true}, ids)
}

func TestCatalogIconAndCategoryName(t *testing.T) {
	base := newCatalog(baseSrc(), nil)
	assert.Equal(t, "IronOre", base.IconName("IronOre"))
	assert.Equal(t, "", base.IconName("Nope"))
	assert.Equal(t, "Minerals", base.CategoryName("Minerals", i18n.LanguageEN))
}
