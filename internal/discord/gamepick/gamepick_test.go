package gamepick

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

var errTestNotFound = errors.New("gamepick_test: not found")

// --- capturing responder ------------------------------------------------------

type capture struct {
	content       string
	components    []discordgo.MessageComponent
	updated       bool
	modalCustomID string
	modalTitle    string
}

func (c *capture) Respond(_ *discordgo.Interaction, s string) error { c.content = s; return nil }
func (c *capture) RespondEphemeral(_ *discordgo.Interaction, s string) error {
	c.content = s
	return nil
}
func (c *capture) RespondEmbed(_ *discordgo.Interaction, _ *discordgo.MessageEmbed) error {
	return nil
}
func (c *capture) RespondAutocomplete(_ *discordgo.Interaction, _ []*discordgo.ApplicationCommandOptionChoice) error {
	return nil
}
func (c *capture) RespondEmbedComponents(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.components = comps
	return nil
}
func (c *capture) RespondEmbedComponentsEphemeral(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.components = comps
	return nil
}
func (c *capture) UpdateMessage(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.components, c.updated = comps, true
	return nil
}
func (c *capture) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, comps []discordgo.MessageComponent) error {
	c.components = comps
	return nil
}
func (c *capture) UpdateComponentsV2(_ *discordgo.Interaction, comps []discordgo.MessageComponent) error {
	c.components, c.updated = comps, true
	return nil
}
func (c *capture) RespondModal(_ *discordgo.Interaction, customID, title string, _ []discordgo.MessageComponent) error {
	c.modalCustomID, c.modalTitle = customID, title
	return nil
}

// --- fakes -------------------------------------------------------------------

type fakeSearch struct {
	hits []gamedata.Hit
	err  error
}

func (f fakeSearch) Search(_ gamedata.Kind, _ i18n.Language, _ string, _ int) ([]gamedata.Hit, error) {
	return f.hits, f.err
}

func testLoc(t *testing.T) *i18n.Localizer {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

// newPicker builds a Picker over the real compiled-in gamedata with the given
// search and destinations. Prefix "supply" proves the id builder is prefix-driven.
func newPicker(t *testing.T, search GameSearch, dests ...Destination) *Picker {
	t.Helper()
	reg, err := gamedata.Load(nil, nil)
	require.NoError(t, err)
	return New(Config{
		Prefix:   "supply",
		Keys:     "contracts.console",
		Loc:      testLoc(t),
		Reg:      reg,
		Emo:      nil,
		Search:   search,
		Langs:    i18n.StaticResolver{Theme: "standard", Lang: "en"},
		Log:      zap.NewNop(),
		OnError:  onError,
		NotFound: errTestNotFound,
	}, dests...)
}

func onError(_ context.Context, r registry.Responder, i *discordgo.InteractionCreate, _ uuid.UUID, err error) error {
	return r.RespondEphemeral(i.Interaction, "error:"+err.Error())
}

// catalogIDs discovers real gdids from the loaded catalog so the tests don't
// hardcode fragile ids: two pickable items, one excluded-category item, and a
// space object.
func catalogIDs(t *testing.T, p *Picker) (item1, item2, excluded, spaceObj string) {
	t.Helper()
	cat := p.cfg.Reg.Latest()
	require.NotNil(t, cat)
	for _, it := range cat.Items() {
		if excludedItemCategories[it.DisplayCategory] {
			if excluded == "" {
				excluded = string(it.ID)
			}
			continue
		}
		if it.DisplayCategory == "" {
			continue
		}
		switch {
		case item1 == "":
			item1 = string(it.ID)
		case item2 == "" && string(it.ID) != item1:
			item2 = string(it.ID)
		}
	}
	sos := cat.SpaceObjects()
	require.NotEmpty(t, sos)
	spaceObj = string(sos[0].ID)
	require.NotEmpty(t, item1)
	require.NotEmpty(t, item2)
	return item1, item2, excluded, spaceObj
}

// --- interaction builders ----------------------------------------------------

func compInteraction(values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionMessageComponent,
		GuildID: "123",
		Data:    discordgo.MessageComponentInteractionData{Values: values},
	}}
}

func modalInteraction(qty string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionModalSubmit,
		GuildID: "123",
		Data: discordgo.ModalSubmitInteractionData{Components: []discordgo.MessageComponent{
			&discordgo.Label{Component: &discordgo.TextInput{CustomID: qtyField, Value: qty}},
		}},
	}}
}

// applyRec records an Apply/Clear/OpenModal invocation.
type applyRec struct {
	applied bool
	picked  Picked
	qty     int
	cleared bool
	opened  bool
}

func itemDest(code string, needsQty, browse bool, rec *applyRec, authorize Authorizer) Destination {
	return Destination{
		Code: code, Kind: gamedata.KindItem, NeedsQty: needsQty, Browse: browse,
		Authorize: authorize,
		BackID:    func(t uuid.UUID) string { return "back:" + t.String() },
		SearchID:  func(t uuid.UUID) string { return "search:" + t.String() },
		OpenModal: func(_ context.Context, r registry.Responder, i *discordgo.InteractionCreate, _, _ uuid.UUID) error {
			rec.opened = true
			return r.RespondModal(i.Interaction, "opened", "t", nil)
		},
		Apply: func(_ context.Context, _ registry.Responder, _ *discordgo.InteractionCreate, _, _ uuid.UUID, p Picked, qty int, _ bool) error {
			rec.applied, rec.picked, rec.qty = true, p, qty
			return nil
		},
	}
}

func locDestFor(code, current string, rec *applyRec) Destination {
	return Destination{
		Code: code, Kind: gamedata.KindSpaceObject, NeedsQty: false, Browse: false,
		BackID:  func(t uuid.UUID) string { return "back:" + t.String() },
		Current: func(_ context.Context, _, _ uuid.UUID) (string, error) { return current, nil },
		Clear: func(_ context.Context, _ registry.Responder, _ *discordgo.InteractionCreate, _, _ uuid.UUID) error {
			rec.cleared = true
			return nil
		},
		Apply: func(_ context.Context, _ registry.Responder, _ *discordgo.InteractionCreate, _, _ uuid.UUID, p Picked, qty int, _ bool) error {
			rec.applied, rec.picked, rec.qty = true, p, qty
			return nil
		},
	}
}

var testTarget = uuid.MustParse("00000000-0000-7000-8000-000000000009")

// --- tests -------------------------------------------------------------------

func TestBuildID_PrefixDriven(t *testing.T) {
	p := newPicker(t, fakeSearch{})
	tid := testTarget.String()
	assert.Equal(t, "supply:pick:si:"+tid, p.buildID(segPick, "si", tid))
	assert.Equal(t, "supply:brwi:si:"+tid+":Cat:0:", p.buildID(segBrowseItems, "si", tid, "Cat", intStr(0), ""))
	assert.Equal(t, "supply:lbrw:sl:"+tid, p.buildID(segLocBrowse, "sl", tid))
}

func TestRunPick_ZeroHits(t *testing.T) {
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{hits: nil}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	require.NoError(t, p.RunPick(context.Background(), r, compInteraction(), uuid.Nil, "si", testTarget, "nothing"))
	assert.False(t, r.updated, "no view change on zero hits")
	assert.NotEmpty(t, r.content, "pick_none rendered ephemerally")
	assert.False(t, rec.applied)
}

func TestRunPick_SingleHit_NonQty_Applies(t *testing.T) {
	item1, _, _, _ := catalogIDs(t, newPicker(t, fakeSearch{}))
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{hits: []gamedata.Hit{{ID: gamedata.GDID(item1), Name: "One"}}},
		itemDest("si", false, false, rec, nil))
	r := &capture{}
	require.NoError(t, p.RunPick(context.Background(), r, compInteraction(), uuid.Nil, "si", testTarget, "one"))
	require.True(t, rec.applied, "single non-qty hit applies directly")
	assert.Equal(t, item1, rec.picked.GDID)
	assert.NotEmpty(t, rec.picked.Version, "stamped with latest version")
}

func TestRunPick_SingleHit_Qty_OpensModal(t *testing.T) {
	item1, _, _, _ := catalogIDs(t, newPicker(t, fakeSearch{}))
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{hits: []gamedata.Hit{{ID: gamedata.GDID(item1), Name: "One"}}},
		itemDest("si", true, true, rec, nil))
	r := &capture{}
	require.NoError(t, p.RunPick(context.Background(), r, compInteraction(), uuid.Nil, "si", testTarget, "one"))
	assert.False(t, rec.applied, "qty destinations wait for the modal")
	assert.Equal(t, "supply:pick:si:"+testTarget.String(), pickSelectCustomID(t, r), "single qty hit still shows the one-option pick page")
}

func TestRunPick_MultipleHits_PickPage(t *testing.T) {
	pp := newPicker(t, fakeSearch{})
	item1, item2, _, _ := catalogIDs(t, pp)
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{hits: []gamedata.Hit{
		{ID: gamedata.GDID(item1), Name: "One"}, {ID: gamedata.GDID(item2), Name: "Two"},
	}}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	require.NoError(t, p.RunPick(context.Background(), r, compInteraction(), uuid.Nil, "si", testTarget, "q"))
	assert.True(t, r.updated)
	assert.Equal(t, "supply:pick:si:"+testTarget.String(), pickSelectCustomID(t, r))
}

func TestRunPick_UnknownDest(t *testing.T) {
	p := newPicker(t, fakeSearch{})
	r := &capture{}
	err := p.RunPick(context.Background(), r, compInteraction(), uuid.Nil, "zz", testTarget, "q")
	require.Error(t, err, "unknown dest is an error, not a panic")
}

func TestHandlePick_AuthorizeDenyShortCircuits(t *testing.T) {
	item1, _, _, _ := catalogIDs(t, newPicker(t, fakeSearch{}))
	rec := &applyRec{}
	deny := func(_ context.Context, r registry.Responder, i *discordgo.InteractionCreate, _, _ uuid.UUID) (bool, error) {
		return false, r.RespondEphemeral(i.Interaction, "denied")
	}
	p := newPicker(t, fakeSearch{}, itemDest("si", false, false, rec, deny))
	r := &capture{}
	require.NoError(t, p.HandlePick(context.Background(), r, compInteraction(item1), uuid.Nil, []string{"si", testTarget.String()}))
	assert.Equal(t, "denied", r.content)
	assert.False(t, rec.applied, "apply never runs when authorize denies")
}

func TestHandleQtySubmit_ValidAndInvalid(t *testing.T) {
	item1, _, _, _ := catalogIDs(t, newPicker(t, fakeSearch{}))
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec, nil))

	// Valid quantity applies with the parsed qty.
	r := &capture{}
	require.NoError(t, p.HandleQtySubmit(context.Background(), r, modalInteraction("7"), uuid.Nil, []string{"si", testTarget.String(), item1}))
	require.True(t, rec.applied)
	assert.Equal(t, 7, rec.qty)

	// Non-numeric / zero / negative → bad_qty, no apply.
	for _, bad := range []string{"", "abc", "0", "-3"} {
		rec2 := &applyRec{}
		p2 := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec2, nil))
		r2 := &capture{}
		require.NoError(t, p2.HandleQtySubmit(context.Background(), r2, modalInteraction(bad), uuid.Nil, []string{"si", testTarget.String(), item1}))
		assert.Falsef(t, rec2.applied, "bad qty %q must not apply", bad)
		assert.NotEmptyf(t, r2.content, "bad qty %q renders bad_qty", bad)
	}
}

func TestApply_ExcludedGDIDRejected(t *testing.T) {
	pp := newPicker(t, fakeSearch{})
	_, _, excluded, _ := catalogIDs(t, pp)
	if excluded == "" {
		t.Skip("no excluded-category item in the compiled-in catalog")
	}
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	// Forged qty submit carrying an excluded gdid: the hard boundary rejects it.
	require.NoError(t, p.HandleQtySubmit(context.Background(), r, modalInteraction("3"), uuid.Nil, []string{"si", testTarget.String(), excluded}))
	assert.False(t, rec.applied, "excluded gdid never reaches Apply")
	assert.NotEmpty(t, r.content, "item_excluded rendered")
}

func TestHandleBrowseSearch_DelegatesToOpenModal(t *testing.T) {
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	require.NoError(t, p.HandleBrowseSearch(context.Background(), r, compInteraction(), uuid.Nil, []string{"si", testTarget.String()}))
	assert.True(t, rec.opened, "browser Search delegates to the destination's modal opener")
}

func TestHandleBrowseItems_ChoiceOpensQtyModal(t *testing.T) {
	item1, _, _, _ := catalogIDs(t, newPicker(t, fakeSearch{}))
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	// A select CHOICE (value present) on the item page opens the quantity modal.
	parts := []string{"si", testTarget.String(), "SomeCategory", "0", ""}
	require.NoError(t, p.HandleBrowseItems(context.Background(), r, compInteraction(item1), uuid.Nil, parts))
	assert.Equal(t, "supply:m_bqty:si:"+testTarget.String()+":"+item1, r.modalCustomID)
	assert.False(t, rec.applied, "the modal gathers the qty first")
}

func TestHandleBrowseItems_PagerRerenders(t *testing.T) {
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	// A plain click (no value) re-renders the page.
	parts := []string{"si", testTarget.String(), "SomeCategory", "0", ""}
	require.NoError(t, p.HandleBrowseItems(context.Background(), r, compInteraction(), uuid.Nil, parts))
	assert.True(t, r.updated)
}

func TestHandleBrowseSub_Rerenders(t *testing.T) {
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", true, true, rec, nil))
	r := &capture{}
	parts := []string{"si", testTarget.String(), "SomeCategory"}
	require.NoError(t, p.HandleBrowseSub(context.Background(), r, compInteraction("SubX"), uuid.Nil, parts))
	assert.True(t, r.updated)
}

func TestRenderBrowse_RejectsNonBrowseDest(t *testing.T) {
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, itemDest("si", false, false, rec, nil)) // Browse=false
	r := &capture{}
	require.NoError(t, p.RenderBrowse(context.Background(), r, compInteraction(), uuid.Nil, "si", testTarget))
	assert.Contains(t, r.content, "error:", "a non-browse dest is not-found via OnError")
}

func TestLocation_CurrentPreselected_AndClear(t *testing.T) {
	pp := newPicker(t, fakeSearch{})
	_, _, _, spaceObj := catalogIDs(t, pp)
	rec := &applyRec{}
	p := newPicker(t, fakeSearch{}, locDestFor("sl", spaceObj, rec))

	// Render pre-selects the current location.
	r := &capture{}
	require.NoError(t, p.RenderLocation(context.Background(), r, compInteraction(), uuid.Nil, "sl", testTarget))
	assert.True(t, r.updated)
	assert.True(t, locOptionSelected(r, spaceObj), "current location is pre-selected")

	// Clear delegates to the destination's Clear hook.
	rc := &capture{}
	require.NoError(t, p.HandleLocClear(context.Background(), rc, compInteraction(), uuid.Nil, []string{"sl", testTarget.String()}))
	assert.True(t, rec.cleared)

	// Picking a location applies it.
	ra := &capture{}
	require.NoError(t, p.HandleLocBrowse(context.Background(), ra, compInteraction(spaceObj), uuid.Nil, []string{"sl", testTarget.String()}))
	assert.True(t, rec.applied)
	assert.Equal(t, spaceObj, rec.picked.GDID)
}

// --- component inspection helpers --------------------------------------------

func pickSelectCustomID(t *testing.T, r *capture) string {
	t.Helper()
	return findSelectCustomID(r.components)
}

func findSelectCustomID(comps []discordgo.MessageComponent) string {
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.Container:
			if id := findSelectCustomID(v.Components); id != "" {
				return id
			}
		case discordgo.ActionsRow:
			if id := findSelectCustomID(v.Components); id != "" {
				return id
			}
		case discordgo.SelectMenu:
			return v.CustomID
		}
	}
	return ""
}

func locOptionSelected(r *capture, gdid string) bool {
	var sel *discordgo.SelectMenu
	var walk func(comps []discordgo.MessageComponent)
	walk = func(comps []discordgo.MessageComponent) {
		for _, c := range comps {
			switch v := c.(type) {
			case discordgo.Container:
				walk(v.Components)
			case discordgo.ActionsRow:
				walk(v.Components)
			case discordgo.SelectMenu:
				vv := v
				sel = &vv
			}
		}
	}
	walk(r.components)
	if sel == nil {
		return false
	}
	for _, o := range sel.Options {
		if o.Value == gdid && o.Default {
			return true
		}
	}
	return false
}
