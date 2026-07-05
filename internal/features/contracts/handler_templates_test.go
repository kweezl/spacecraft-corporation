package contracts_test

import (
	"context"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/emoji"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// modalInputs builds a modal submit whose text inputs carry the given values,
// each wrapped in a *Label as discordgo unmarshals them.
func modalInputs(m *discordgo.Member, customID string, kv map[string]string) *discordgo.InteractionCreate {
	comps := make([]discordgo.MessageComponent, 0, len(kv))
	for id, v := range kv {
		comps = append(comps, &discordgo.Label{Component: &discordgo.TextInput{CustomID: id, Value: v}})
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: "g", Member: m,
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID, Components: comps},
	}}
}

// selectIDs collects every select-menu CustomID in a Components V2 tree.
func selectIDs(comps []discordgo.MessageComponent) []string {
	var ids []string
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.ActionsRow:
			ids = append(ids, selectIDs(v.Components)...)
		case discordgo.Container:
			ids = append(ids, selectIDs(v.Components)...)
		case discordgo.SelectMenu:
			ids = append(ids, v.CustomID)
		}
	}
	return ids
}

func latestVersion() string { return testRegistry.Latest().Version() }

func TestConsole_HomeShowsTemplatesButtonWithKey(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Counts(mock.Anything, gid).Return(contracts.Counts{}, nil).Twice()

	// The templates-library button (like every authoring button) shows to a
	// contract manager and is hidden from anyone without the manager key.
	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	run(t, f, r, consoleCmd(member("librarian")))
	assert.True(t, has(buttonIDs(r.components), "contract:tlist:0:"), "the templates-library button shows for a manager")

	r2 := &capture{}
	f2 := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.use"))
	run(t, f2, r2, consoleCmd(member("participant")))
	assert.False(t, has(buttonIDs(r2.components), "contract:tlist:0:"), "hidden from a non-manager")
}

func TestConsole_AddItemSearch_NoHits(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	search := mocks.NewMockGameSearch(t)
	search.EXPECT().Search(gamedata.KindItem, i18n.LanguageEN, "vaporware", 10).Return(nil, nil).Once()

	r := &capture{}
	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{search: search})
	require.NoError(t, f.Component().Handler(context.Background(), r,
		modalInputs(member("officer"), "contract:m_cadd:"+cid.String(), map[string]string{"query": "vaporware", "qty": "5"}), gid))
	assert.NotEmpty(t, r.content, "no hits give an ephemeral notice")
	assert.Empty(t, r.components)
}

func TestConsole_AddItemSearch_SingleHitOffersPick(t *testing.T) {
	// Even a single item hit goes through the pick page: the quantity modal can
	// only open from a component interaction, so the one-option select bridges
	// the modal-submit → modal gap.
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	search := mocks.NewMockGameSearch(t)
	search.EXPECT().Search(gamedata.KindItem, i18n.LanguageEN, "actuator", 10).
		Return([]gamedata.Hit{{ID: "Actuator", Name: "Hydraulic Actuator"}}, nil).Once()

	r := &capture{}
	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{search: search})
	require.NoError(t, f.Component().Handler(context.Background(), r,
		modalInputs(member("officer"), "contract:m_cadd:"+cid.String(), map[string]string{"query": "actuator"}), gid))
	assert.True(t, r.updated, "the pick page replaces the console view in place")
	assert.True(t, has(selectIDs(r.components), "contract:pick:ci:"+cid.String()))
}

func TestConsole_AddItemSearch_MultiHitOffersSelect(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	search := mocks.NewMockGameSearch(t)
	// Both hits must be REAL catalog items (the picker drops unknown/excluded
	// ones); "Actuator" also has an icon in the emoji store, "ActivatedCoal"
	// shares the Gunpowder icon which the store doesn't carry here — so one
	// option renders with an emoji and one plain.
	search.EXPECT().Search(gamedata.KindItem, i18n.LanguageEN, "act", 10).
		Return([]gamedata.Hit{{ID: "Actuator", Name: "Hydraulic Actuator"}, {ID: "ActivatedCoal", Name: "Activated Coal"}}, nil).Once()
	emo := emoji.StaticStore(map[string]string{"Actuator": "<:Actuator:1234567890>"})

	r := &capture{}
	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{search: search, emo: emo})
	require.NoError(t, f.Component().Handler(context.Background(), r,
		modalInputs(member("officer"), "contract:m_cadd:"+cid.String(), map[string]string{"query": "act"}), gid))
	// The console message transforms IN PLACE into the pick page (no separate
	// ephemeral to miss); the destination rides the select's CustomID, Search
	// reopens the query modal, and Back abandons into the contract view.
	assert.True(t, r.updated, "the pick page replaces the console view in place")
	assert.True(t, has(selectIDs(r.components), "contract:pick:ci:"+cid.String()))
	assert.True(t, has(buttonIDs(r.components), "contract:view:"+cid.String()), "a Back button returns to the contract view")
	assert.True(t, has(buttonIDs(r.components), "contract:brws:ci:"+cid.String()), "a Search button reopens the query modal")

	// The catalog item's option carries its icon emoji; the unknown one is plain.
	opts := selectOptions(t, r.components)
	require.Len(t, opts, 2)
	require.NotNil(t, opts[0].Emoji, "catalog item option carries its icon")
	assert.Equal(t, "Actuator", opts[0].Emoji.Name)
	assert.Equal(t, "1234567890", opts[0].Emoji.ID)
	assert.Nil(t, opts[1].Emoji, "unknown item option renders plain")
}

// optionsOf returns the options of the select menu with the given CustomID.
func optionsOf(t *testing.T, comps []discordgo.MessageComponent, customID string) []discordgo.SelectMenuOption {
	t.Helper()
	var find func(comps []discordgo.MessageComponent) []discordgo.SelectMenuOption
	find = func(comps []discordgo.MessageComponent) []discordgo.SelectMenuOption {
		for _, c := range comps {
			switch v := c.(type) {
			case discordgo.SelectMenu:
				if v.CustomID == customID {
					return v.Options
				}
			case discordgo.ActionsRow:
				if opts := find(v.Components); opts != nil {
					return opts
				}
			case discordgo.Container:
				if opts := find(v.Components); opts != nil {
					return opts
				}
			}
		}
		return nil
	}
	opts := find(comps)
	if opts == nil {
		t.Fatalf("no select menu %q found", customID)
	}
	return opts
}

// selectOptions returns the options of the first select menu in a component tree.
func selectOptions(t *testing.T, comps []discordgo.MessageComponent) []discordgo.SelectMenuOption {
	t.Helper()
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.ActionsRow:
			if opts := trySelectOptions(v.Components); opts != nil {
				return opts
			}
		case discordgo.Container:
			if opts := trySelectOptions(v.Components); opts != nil {
				return opts
			}
		}
	}
	t.Fatal("no select menu found")
	return nil
}

func trySelectOptions(comps []discordgo.MessageComponent) []discordgo.SelectMenuOption {
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.SelectMenu:
			return v.Options
		case discordgo.ActionsRow:
			if opts := trySelectOptions(v.Components); opts != nil {
				return opts
			}
		}
	}
	return nil
}

func TestConsole_PickSelectThenQtyApplies(t *testing.T) {
	cid := uuid.New()

	// Step 1: choosing an item from the pick select opens the quantity modal
	// (the pick re-checks the destination's key itself).
	repo := mocks.NewMockRepository(t)
	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:pick:ci:"+cid.String(), "Actuator"), gid))
	assert.Equal(t, "contract:m_bqty:ci:"+cid.String()+":Actuator", r.modalCustomID, "picking an item prompts for the quantity")

	// Step 2: the quantity submit applies — the localized name snapshot, the
	// gdid, and the latest catalog version reach the repository.
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().AddItemByID(mock.Anything, gid, cid, "Hydraulic Actuator", "Actuator", latestVersion(), mock.Anything, 7, 25, "officer").Return(nil).Once()
	repo2.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom, LastRefreshedAt: time.Now()},
	}, nil).Once()
	r2 := &capture{}
	f2 := newFeature(t, repo2, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f2.Component().Handler(context.Background(), r2,
		modalInputs(member("officer"), "contract:m_bqty:ci:"+cid.String()+":Actuator", map[string]string{"qty": "7"}), gid))
	assert.True(t, r2.updated, "the quantity submit lands on the contract view")
}

func TestConsole_BrowseFlow(t *testing.T) {
	// The zero-typing path: Add item → category list → category page → item pick
	// (→ quantity modal, covered above by the shared m_bqty step).
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	emo := emoji.StaticStore(map[string]string{"Actuator": "<:Actuator:1234567890>"})

	// Add item transforms the console into the category browser.
	r := &capture{}
	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{emo: emo})
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:cadd:"+cid.String()), gid))
	assert.True(t, r.updated)
	assert.True(t, has(selectIDs(r.components), "contract:brw:ci:"+cid.String()), "the category select is on the page")
	assert.True(t, has(buttonIDs(r.components), "contract:brws:ci:"+cid.String()), "a Search button covers type-first users")
	assert.True(t, has(buttonIDs(r.components), "contract:view:"+cid.String()), "Back returns to the contract view")

	// Categories that can't be contract requirements are absent from the switcher.
	for _, o := range optionsOf(t, r.components, "contract:brw:ci:"+cid.String()) {
		assert.NotContains(t, []string{"BaseBuilding", "BeaconData", "Blueprint"}, o.Value,
			"excluded categories are hidden from the browser")
	}

	// Choosing a category renders its item page, ordered by name, with icons.
	r2 := &capture{}
	require.NoError(t, f.Component().Handler(context.Background(), r2,
		component("", member("officer"), "contract:brw:ci:"+cid.String(), "FactoredMaterial"), gid))
	itemsSelectID := "contract:brwi:ci:" + cid.String() + ":FactoredMaterial:0:"
	assert.True(t, has(selectIDs(r2.components), itemsSelectID))
	opts := optionsOf(t, r2.components, itemsSelectID)
	require.NotEmpty(t, opts)
	assert.LessOrEqual(t, len(opts), 25)
	for i := 1; i < len(opts); i++ {
		assert.LessOrEqual(t, opts[i-1].Label, opts[i].Label, "items are ordered by name")
	}
	// The category switcher stays on the item page with the current category
	// pre-selected — correcting a wrong pick needs no Back round-trip.
	catOpts := optionsOf(t, r2.components, "contract:brw:ci:"+cid.String())
	var defaulted string
	for _, o := range catOpts {
		if o.Default {
			defaulted = o.Value
		}
	}
	assert.Equal(t, "FactoredMaterial", defaulted, "the current category is pre-selected in the switcher")
	assert.True(t, has(buttonIDs(r2.components), "contract:view:"+cid.String()), "Back exits the browser to the contract view")
	// FactoredMaterial holds 76 items — more than one 25-option page — so the
	// pager buttons must be present.
	assert.True(t, has(buttonIDs(r2.components), "contract:brwi:ci:"+cid.String()+":FactoredMaterial:1:"), "Next pages into the category's remaining items")

	// The optional subcategory filter appears (FactoredMaterial distinguishes 6)
	// and narrowing to one re-renders a smaller, single-subcategory page.
	subSelectID := "contract:brwsub:ci:" + cid.String() + ":FactoredMaterial"
	assert.True(t, has(selectIDs(r2.components), subSelectID))
	r2b := &capture{}
	require.NoError(t, f.Component().Handler(context.Background(), r2b,
		component("", member("officer"), subSelectID, "ShipAmmunition"), gid))
	filteredID := "contract:brwi:ci:" + cid.String() + ":FactoredMaterial:0:ShipAmmunition"
	filtered := optionsOf(t, r2b.components, filteredID)
	assert.Len(t, filtered, 4, "ShipAmmunition holds exactly 4 items")
	subOpts := optionsOf(t, r2b.components, subSelectID)
	for _, o := range subOpts {
		if o.Default {
			assert.Equal(t, "ShipAmmunition", o.Value, "the active subcategory is pre-selected")
		}
	}

	// Choosing an item opens the quantity modal.
	r3 := &capture{}
	require.NoError(t, f.Component().Handler(context.Background(), r3,
		component("", member("officer"), itemsSelectID, "Actuator"), gid))
	assert.Equal(t, "contract:m_bqty:ci:"+cid.String()+":Actuator", r3.modalCustomID)
}

func TestConsole_ExcludedItemRejectedAtApply(t *testing.T) {
	// The browser and search hide excluded categories, but the gdid arrives via a
	// forgeable CustomID — the quantity submit is the hard boundary.
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	// BaseBuilding_CommandCenter-like ids vary; use the known excluded item
	// "SpaceStation0" if present — instead pick any BaseBuilding item via the
	// registry to stay robust against data changes.
	var excluded string
	for _, it := range testRegistry.Latest().Items() {
		if it.DisplayCategory == "BaseBuilding" {
			excluded = string(it.ID)
			break
		}
	}
	require.NotEmpty(t, excluded, "the catalog ships BaseBuilding items")
	require.NoError(t, f.Component().Handler(context.Background(), r,
		modalInputs(member("officer"), "contract:m_bqty:ci:"+cid.String()+":"+excluded, map[string]string{"qty": "5"}), gid))
	assert.NotEmpty(t, r.content, "the excluded item is refused with a notice")
	assert.False(t, r.updated, "nothing is applied")
}

func TestConsole_LegacyItemLinkFlow(t *testing.T) {
	// A pre-gamedata contract: free-text item, no rewards/location. The item view
	// must render it and offer the Link button; the link flow stamps the gdid.
	cid, itemID := uuid.New(), uuid.New()
	legacy := contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, ThreadID: "t1", Title: "Old Contract", Status: contracts.StatusOpen, Kind: contracts.KindCustom, LastRefreshedAt: time.Now()},
		Items:    []contracts.Item{{ID: itemID, Name: "Hand-typed Actuator", RequiredQty: 100}},
	}

	// 1. The item view of a legacy item shows [Link to game data].
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByItemScoped(mock.Anything, gid, itemID).Return(legacy, nil).Once()
	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:irow:"+itemID.String()), gid))
	assert.True(t, has(buttonIDs(r.components), "contract:ilink:"+itemID.String()), "legacy item offers the Link button")

	// 2. The Link button opens the search modal prefilled with the stored name.
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().ProgressByItemScoped(mock.Anything, gid, itemID).Return(legacy, nil).Once()
	r2 := &capture{}
	f2 := newFeature(t, repo2, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f2.Component().Handler(context.Background(), r2,
		component("", member("officer"), "contract:ilink:"+itemID.String()), gid))
	assert.Equal(t, "contract:m_ilink:"+itemID.String(), r2.modalCustomID)

	// 3. Submitting the search with one hit links immediately: gdid + latest
	// version + the all-language alias set reach the repository.
	repo3 := mocks.NewMockRepository(t)
	repo3.EXPECT().LinkItemGDID(mock.Anything, gid, itemID, "Actuator", latestVersion(),
		mock.MatchedBy(func(aliases []string) bool {
			return has(aliases, "Actuator") && has(aliases, "Hydraulic Actuator")
		}), "officer").Return(cid, nil).Once()
	repo3.EXPECT().ProgressByItemScoped(mock.Anything, gid, itemID).Return(legacy, nil).Once()
	search := mocks.NewMockGameSearch(t)
	search.EXPECT().Search(gamedata.KindItem, i18n.LanguageEN, "Hand-typed Actuator", 10).
		Return([]gamedata.Hit{{ID: "Actuator", Name: "Hydraulic Actuator"}}, nil).Once()
	r3 := &capture{}
	f3 := newFeatureDeps(t, repo3, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{search: search})
	require.NoError(t, f3.Component().Handler(context.Background(), r3,
		modalInputs(member("officer"), "contract:m_ilink:"+itemID.String(), map[string]string{"query": "Hand-typed Actuator"}), gid))
	assert.True(t, r3.updated, "linking re-renders the item view in place")
}

func TestConsole_LegacyContractRendersWithoutGamedata(t *testing.T) {
	// A pre-gamedata contract renders in the console with its stored names, no
	// facts block, and a working Open accessory per item.
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	itemID := uuid.New()
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, ThreadID: "t1", Title: "Old Contract", Status: contracts.StatusOpen, Kind: contracts.KindCustom, LastRefreshedAt: time.Now()},
		Items:    []contracts.Item{{ID: itemID, Name: "Hand-typed Steel", RequiredQty: 100, ReservedQty: 10}},
	}, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:view:"+cid.String()), gid))
	require.NotEmpty(t, r.components)
	assert.True(t, has(buttonIDs(r.components), "contract:irow:"+itemID.String()), "legacy items keep their drill-down")
}

func TestConsole_LocationBrowser(t *testing.T) {
	// The Location button transforms the console into the modal-free space-object
	// picker, current location pre-selected.
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom,
			LocationGDID: "Station_Cairn", LocationGDVersion: "v1", LastRefreshedAt: time.Now()},
	}, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:cloc:"+cid.String()), gid))
	assert.True(t, r.updated)
	locSelectID := "contract:lbrw:cl:" + cid.String()
	opts := optionsOf(t, r.components, locSelectID)
	require.NotEmpty(t, opts)
	var defaulted string
	for _, o := range opts {
		if o.Default {
			defaulted = o.Value
		}
	}
	assert.Equal(t, "Station_Cairn", defaulted, "the stored location is pre-selected")
	assert.True(t, has(buttonIDs(r.components), "contract:lclr:cl:"+cid.String()), "a Clear button is offered")

	// Choosing a station applies immediately (no quantity, no modal) and lands
	// back on the contract view.
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().SetDeliveryLocation(mock.Anything, gid, cid, "Station_Horizon", latestVersion(), "officer").Return(nil).Once()
	repo2.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom, LastRefreshedAt: time.Now()},
	}, nil).Once()
	r2 := &capture{}
	f2 := newFeature(t, repo2, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f2.Component().Handler(context.Background(), r2,
		component("", member("officer"), locSelectID, "Station_Horizon"), gid))
	assert.True(t, r2.updated, "picking a station lands on the contract view")

	// Clear drops the location.
	repo3 := mocks.NewMockRepository(t)
	repo3.EXPECT().SetDeliveryLocation(mock.Anything, gid, cid, "", "", "officer").Return(nil).Once()
	repo3.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom, LastRefreshedAt: time.Now()},
	}, nil).Once()
	r3 := &capture{}
	f3 := newFeature(t, repo3, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f3.Component().Handler(context.Background(), r3,
		component("", member("officer"), "contract:lclr:cl:"+cid.String()), gid))
	assert.True(t, r3.updated, "clearing lands on the contract view")
}

func TestConsole_UseTemplateCreatesContract(t *testing.T) {
	tid, cid := uuid.New(), uuid.New()
	tpls := mocks.NewMockTemplateRepository(t)
	tpls.EXPECT().TemplateByID(mock.Anything, gid, tid).Return(contracts.Template{
		ID: tid, ServerID: gid, Title: "Weekly Steel", Description: "Restock",
		RewardCredits: decimal.RequireFromString("1250.50"), RewardReputation: 5, RewardLicencePoints: 0,
		DeadlineMinutes: 60, LocationGDID: "Station_Cairn", LocationGDVersion: "v1",
		Items: []contracts.TemplateItem{{ID: uuid.New(), GDID: "Actuator", GDVersion: "v1", Qty: 100}},
	}, nil).Once()
	forum := mocks.NewMockForumConfig(t)
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("chan", true).Once()

	repo := mocks.NewMockRepository(t)
	var got contracts.CreateInput
	repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(in contracts.CreateInput) bool {
		got = in
		return true
	})).Return(cid, nil).Once()
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindTemplate, LastRefreshedAt: time.Now()},
	}, nil).Once()

	r := &capture{}
	f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), forum, featureDeps{tpls: tpls})
	require.NoError(t, f.Component().Handler(context.Background(), r,
		modalInputs(member("officer"), "contract:m_tuse:"+tid.String(), map[string]string{
			"name": "Steel — March run", "days": "0", "hours": "1", "minutes": "30",
		}), gid))

	// The template's values are copied BY VALUE onto the new contract.
	assert.Equal(t, contracts.KindTemplate, got.Kind)
	require.NotNil(t, got.TemplateID)
	assert.Equal(t, tid, *got.TemplateID)
	assert.Equal(t, "Steel — March run", got.Title, "the confirm modal's title wins over the template's")
	assert.Equal(t, "Restock", got.Description)
	require.NotNil(t, got.RewardCredits)
	assert.True(t, got.RewardCredits.Equal(decimal.RequireFromString("1250.50")), "got %s", got.RewardCredits)
	require.NotNil(t, got.RewardReputation)
	assert.Equal(t, 5, *got.RewardReputation)
	assert.Nil(t, got.RewardLicencePoints, "a zero template reward stays unset on the contract")
	assert.Equal(t, "Station_Cairn", got.LocationGDID)
	require.NotNil(t, got.Deadline)
	assert.WithinDuration(t, time.Now().Add(90*time.Minute), *got.Deadline, 5*time.Second,
		"the deadline comes from the modal's D/H/M, not the template")
	require.Len(t, got.Items, 1)
	assert.Equal(t, contracts.CreateItemInput{Name: "Hydraulic Actuator", GDID: "Actuator", GDVersion: "v1", Qty: 100}, got.Items[0],
		"items copy the template's stamped version and snapshot the localized name")
	assert.Empty(t, got.AppID, "no interaction token: the console navigates itself")
	assert.True(t, r.updated, "the console lands on the new contract view")
}
