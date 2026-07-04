package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

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
			// Cut/unreleased content: valid category, but inGame:false -> dropped.
			"CutGizmo": {ID: "CutGizmo", DisplayCategory: strptr("ShipPart"), InGame: boolptr(false)},
			// inGame:false but referenced by a kept contract: rescue overrides.
			"Drone0": {ID: "Drone0", DisplayCategory: strptr("ShipPart"), InGame: boolptr(false)},
			// inGame:false, referenced only by a dropped contract: NOT rescued.
			"GhostItem": {ID: "GhostItem", DisplayCategory: strptr("ShipPart"), InGame: boolptr(false)},
		},
		categories: map[string]rawCategory{
			"Minerals": {ID: "Minerals"},
		},
		contracts: map[string]rawContract{
			"C1": {
				ID:      "C1",
				Items:   []rawItemQty{{Item: "IronOre", Qty: 2}, {Item: "Drone0", Qty: 1}},
				Rewards: []rawItemCount{{Item: "CorpoCredits", Count: 5}},
			},
			// A dropped (inGame:false) contract; its item refs must NOT rescue.
			"DeadC": {ID: "DeadC", InGame: boolptr(false), Items: []rawItemQty{{Item: "GhostItem", Qty: 1}}},
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

func TestBuildExcludesNotInGame(t *testing.T) {
	res, err := buildDataset(testSource())
	require.NoError(t, err)

	// CutGizmo has a valid category but inGame:false and no reference.
	_, ok := res.data.Items["CutGizmo"]
	assert.False(t, ok, "inGame:false item must be dropped")
	assert.Equal(t, "not-in-game", res.dropped["CutGizmo"])

	// The inGame:false contract and its unreferenced item are both dropped.
	_, ok = res.data.Contracts["DeadC"]
	assert.False(t, ok, "inGame:false contract must be dropped")
	assert.Contains(t, res.droppedContracts, "DeadC")
	_, ok = res.data.Items["GhostItem"]
	assert.False(t, ok, "item referenced only by a dropped contract is not rescued")
}

func TestBuildReferencedOverridesNotInGame(t *testing.T) {
	res, err := buildDataset(testSource())
	require.NoError(t, err)

	// Drone0 is inGame:false but a kept contract requires it: keep for link
	// stability, exactly like the real Drone0/*_5_Drones contracts.
	_, ok := res.data.Items["Drone0"]
	assert.True(t, ok, "inGame:false item referenced by a kept contract must survive")
	assert.NotContains(t, res.dropped, "Drone0")
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
