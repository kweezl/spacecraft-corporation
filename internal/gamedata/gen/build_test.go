package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func strptr(s string) *string { return &s }

// testSource builds a small source covering each exclusion path plus the
// contract/space-object safety net.
func testSource() *source {
	return &source{
		items: map[string]rawItem{
			"IronOre":    {ID: "IronOre", DisplayCategory: strptr("Minerals")},
			"Blueprint1": {ID: "Blueprint1", DisplayCategory: strptr("Knowledge")},
			"Quest1":     {ID: "Quest1", DisplayCategory: strptr("QuestItem")},
			"Scrap1":     {ID: "Scrap1", DisplayCategory: strptr("ShipPart"), Subcategory: "Scrap"},
			"Deco1":      {ID: "Deco1", DisplayCategory: strptr("ShipPart"), Subcategory: "ShipDecorative"},
			"NoCat":      {ID: "NoCat"}, // no displayCategory -> dropped
			// Referenced-but-otherwise-excluded: the safety net keeps it.
			"CorpoCredits": {ID: "CorpoCredits"}, // no category, but a contract reward
		},
		categories: map[string]rawCategory{
			"Minerals": {ID: "Minerals"},
		},
		contracts: map[string]rawContract{
			"C1": {
				ID:      "C1",
				Items:   []rawItemQty{{Item: "IronOre", Qty: 2}},
				Rewards: []rawItemCount{{Item: "CorpoCredits", Count: 5}},
			},
		},
		spaceObjects: map[string]rawSpaceObject{},
		aliases: map[string]string{
			"IronOre": "IronOre.webp",
		},
		translations: map[i18n.Language]rawTranslation{
			i18n.LanguageEN: {Item: map[string]rawString{"IronOre": {Name: "Iron Ore"}}},
			i18n.LanguageRU: {Item: map[string]rawString{"IronOre": {Name: "Железная руда"}}},
		},
	}
}

func TestBuildExcludesByRule(t *testing.T) {
	res, err := buildDataset(testSource())
	require.NoError(t, err)

	for _, id := range []string{"Blueprint1", "Quest1", "Scrap1", "Deco1", "NoCat"} {
		_, ok := res.data.Items[schema.GDID(id)]
		assert.Falsef(t, ok, "%s should be excluded", id)
		assert.Contains(t, res.dropped, id)
	}
	assert.Contains(t, res.data.Items, schema.GDID("IronOre"))
}

func TestBuildSafetyNetKeepsReferenced(t *testing.T) {
	res, err := buildDataset(testSource())
	require.NoError(t, err)

	// CorpoCredits has no category (normally dropped) but is a contract reward.
	_, ok := res.data.Items["CorpoCredits"]
	assert.True(t, ok, "referenced item must survive exclusion")
	assert.NotContains(t, res.dropped, "CorpoCredits")
}

func TestBuildResolvesIconAndTranslations(t *testing.T) {
	res, err := buildDataset(testSource())
	require.NoError(t, err)

	assert.Equal(t, "IronOre", res.data.Items["IronOre"].IconName)
	assert.Equal(t, "IronOre.webp", res.icons["IronOre"])
	assert.Equal(t, "Iron Ore", res.data.Names[i18n.LanguageEN]["IronOre"])
	assert.Equal(t, "Железная руда", res.data.Names[i18n.LanguageRU]["IronOre"])
}

func TestBuildRejectsInvalidIconName(t *testing.T) {
	s := testSource()
	s.aliases["IronOre"] = "bad name!.webp"
	_, err := buildDataset(s)
	require.Error(t, err)
}

func TestDiffClassifiesBreaking(t *testing.T) {
	base, err := buildDataset(testSource())
	require.NoError(t, err)
	prev := snapshotOf("v1", "", base.data)

	// Drop a kept item from the source: a removal must read as breaking.
	s := testSource()
	delete(s.items, "IronOre")
	delete(s.contracts, "C1") // also remove its only reference
	next, err := buildDataset(s)
	require.NoError(t, err)

	rep := diffSnapshots(&prev, next.data)
	assert.Contains(t, rep.Items.Removed, "IronOre")
	assert.True(t, rep.breaking())
}

func TestDiffAdditionIsCompatible(t *testing.T) {
	base, err := buildDataset(testSource())
	require.NoError(t, err)
	prev := snapshotOf("v1", "", base.data)

	s := testSource()
	s.items["Copper"] = rawItem{ID: "Copper", DisplayCategory: strptr("Minerals")}
	next, err := buildDataset(s)
	require.NoError(t, err)

	rep := diffSnapshots(&prev, next.data)
	assert.Contains(t, rep.Items.Added, "Copper")
	assert.False(t, rep.breaking())
}

func TestDeltaDatasetOverridesAndRemovals(t *testing.T) {
	base, err := buildDataset(testSource())
	require.NoError(t, err)
	parent := snapshotOf("v1", "", base.data)

	s := testSource()
	delete(s.items, "IronOre")
	delete(s.contracts, "C1")
	s.items["Copper"] = rawItem{ID: "Copper", DisplayCategory: strptr("Minerals")}
	next, err := buildDataset(s)
	require.NoError(t, err)

	delta, removed := deltaDataset(parent, next.data)
	assert.Contains(t, delta.Items, schema.GDID("Copper"), "added item is in the delta")
	assert.NotContains(t, delta.Items, schema.GDID("Quartz"))
	assert.Contains(t, removed, schema.GDID("IronOre"), "removed item is tombstoned")
}
