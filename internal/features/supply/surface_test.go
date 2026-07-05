package supply_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/supply"
	"github.com/kweezl/spacecraft-corporation/internal/features/supply/mocks"
)

const (
	tGuild  = "111111111111111111"
	tUser   = "user-1"
	tThread = "th-1"
)

// --- capturing responder ---

type capture struct {
	content       string
	components    []discordgo.MessageComponent
	updated       bool
	modalCustomID string
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
func (c *capture) RespondEmbedComponents(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, m []discordgo.MessageComponent) error {
	c.components = m
	return nil
}
func (c *capture) RespondEmbedComponentsEphemeral(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, m []discordgo.MessageComponent) error {
	c.components = m
	return nil
}
func (c *capture) UpdateMessage(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, m []discordgo.MessageComponent) error {
	c.components, c.updated = m, true
	return nil
}
func (c *capture) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, m []discordgo.MessageComponent) error {
	c.components = m
	return nil
}
func (c *capture) UpdateComponentsV2(_ *discordgo.Interaction, m []discordgo.MessageComponent) error {
	c.components, c.updated = m, true
	return nil
}
func (c *capture) RespondModal(_ *discordgo.Interaction, customID, _ string, _ []discordgo.MessageComponent) error {
	c.modalCustomID = customID
	return nil
}

// --- interaction builders ---

func member() *discordgo.Member { return &discordgo.Member{User: &discordgo.User{ID: tUser}} }

func componentIx(customID string, values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: tGuild, ChannelID: tThread, Member: member(),
		Data: discordgo.MessageComponentInteractionData{CustomID: customID, Values: values},
	}}
}

// modalIx builds a modal submit with label-wrapped text inputs (id → value) and
// optional single-value selects (id → value).
func modalIx(customID string, texts map[string]string, selects map[string]string) *discordgo.InteractionCreate {
	var comps []discordgo.MessageComponent
	for id, v := range texts {
		comps = append(comps, &discordgo.Label{Component: &discordgo.TextInput{CustomID: id, Value: v}})
	}
	for id, v := range selects {
		comps = append(comps, &discordgo.Label{Component: &discordgo.SelectMenu{CustomID: id, Values: []string{v}}})
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: tGuild, ChannelID: tThread, Member: member(),
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID, Components: comps},
	}}
}

func drive(t *testing.T, f *supply.Feature, i *discordgo.InteractionCreate) (*capture, error) {
	t.Helper()
	r := &capture{}
	return r, f.Component().Handler(context.Background(), r, i, uuid.New())
}

// buttonIDs collects every button CustomID in a Components V2 tree (action rows,
// section accessories, nested containers).
func buttonIDs(comps []discordgo.MessageComponent) []string {
	var ids []string
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.Container:
			ids = append(ids, buttonIDs(v.Components)...)
		case discordgo.ActionsRow:
			ids = append(ids, buttonIDs(v.Components)...)
		case discordgo.Section:
			if b, ok := v.Accessory.(discordgo.Button); ok {
				ids = append(ids, b.CustomID)
			}
		case discordgo.Button:
			ids = append(ids, v.CustomID)
		}
	}
	return ids
}

func hasID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// --- console list (self-scoped) ---

// TestConsole_ListScopedToInvoker proves /supply opens the list scoped to the
// invoking user with the default (open) filter.
func TestConsole_ListScopedToInvoker(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ListByOwner(mock.Anything, mock.Anything, tUser,
		mock.MatchedBy(func(ss []supply.Status) bool { return len(ss) == 1 && ss[0] == supply.StatusOpen }),
		mock.Anything, 0).Return(nil, 0, nil).Once()
	f := testFeature(t, repo, nil)

	slash := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: tGuild, Member: member(),
	}}
	r := &capture{}
	require.NoError(t, f.Command().Handler(context.Background(), r, slash, uuid.New()))
}

// TestConsole_FilterFoldsToOwner proves the status-filter select re-lists scoped
// to the invoker with the chosen statuses.
func TestConsole_FilterFoldsToOwner(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ListByOwner(mock.Anything, mock.Anything, tUser,
		mock.MatchedBy(func(ss []supply.Status) bool { return len(ss) == 2 }),
		mock.Anything, 0).Return(nil, 0, nil).Once()
	f := testFeature(t, repo, nil)
	_, err := drive(t, f, componentIx("supply:sfilter", "open", "cancelled"))
	require.NoError(t, err)
}

// --- request / item views: per-status button presence ---

func TestRequestView_OpenHasManagementButtons(t *testing.T) {
	rid := uuid.New()
	itemID := uuid.New()
	prog := supply.Progress{
		Request: supply.Request{ID: rid, Title: "need parts", Status: supply.StatusOpen},
		Items:   []supply.Item{{ID: itemID, GDID: "IronOre", GDVersion: "v1", RequiredQty: 5}},
	}
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(prog, nil).Once()
	f := testFeature(t, repo, nil)

	r, err := drive(t, f, componentIx("supply:view:"+rid.String()))
	require.NoError(t, err)
	ids := buttonIDs(r.components)
	for _, want := range []string{
		"supply:radd:" + rid.String(), "supply:redit:" + rid.String(), "supply:repub:" + rid.String(),
		"supply:rclose:" + rid.String(), "supply:rloc:" + rid.String(), "supply:rsys:" + rid.String(),
		"supply:rref:" + rid.String(), "supply:list", "supply:irow:" + itemID.String(),
	} {
		assert.Truef(t, hasID(ids, want), "open request view should have %s (got %v)", want, ids)
	}
}

func TestRequestView_ClosedIsReadOnly(t *testing.T) {
	rid := uuid.New()
	prog := supply.Progress{Request: supply.Request{ID: rid, Title: "done", Status: supply.StatusCancelled}}
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(prog, nil).Once()
	f := testFeature(t, repo, nil)

	r, err := drive(t, f, componentIx("supply:view:"+rid.String()))
	require.NoError(t, err)
	ids := buttonIDs(r.components)
	assert.True(t, hasID(ids, "supply:list"), "closed request keeps a Back button")
	for _, gone := range []string{"supply:radd:" + rid.String(), "supply:rclose:" + rid.String(), "supply:rref:" + rid.String()} {
		assert.Falsef(t, hasID(ids, gone), "closed request must not offer %s", gone)
	}
}

func TestItemView_ButtonsPerStatus(t *testing.T) {
	rid := uuid.New()
	itemID := uuid.New()
	item := supply.Item{ID: itemID, GDID: "IronOre", GDVersion: "v1", RequiredQty: 5}

	// Open: edit-qty + remove + back.
	openProg := supply.Progress{Request: supply.Request{ID: rid, Status: supply.StatusOpen}, Items: []supply.Item{item}}
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByItemOwned(mock.Anything, mock.Anything, tUser, itemID).Return(openProg, nil).Once()
	f := testFeature(t, repo, nil)
	r, err := drive(t, f, componentIx("supply:irow:"+itemID.String()))
	require.NoError(t, err)
	ids := buttonIDs(r.components)
	assert.True(t, hasID(ids, "supply:iedit:"+itemID.String()))
	assert.True(t, hasID(ids, "supply:idel:"+itemID.String()))
	assert.True(t, hasID(ids, "supply:view:"+rid.String()), "Back returns to the request view")

	// Closed: only back.
	closedProg := supply.Progress{Request: supply.Request{ID: rid, Status: supply.StatusCompleted}, Items: []supply.Item{item}}
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().ProgressByItemOwned(mock.Anything, mock.Anything, tUser, itemID).Return(closedProg, nil).Once()
	f2 := testFeature(t, repo2, nil)
	r2, err := drive(t, f2, componentIx("supply:irow:"+itemID.String()))
	require.NoError(t, err)
	ids2 := buttonIDs(r2.components)
	assert.True(t, hasID(ids2, "supply:view:"+rid.String()))
	assert.False(t, hasID(ids2, "supply:iedit:"+itemID.String()), "closed item is read-only")
	assert.False(t, hasID(ids2, "supply:idel:"+itemID.String()))
}

// --- routing ---

func TestRoute_UnknownSegmentErrors(t *testing.T) {
	f := testFeature(t, nil, nil)
	_, err := drive(t, f, componentIx("supply:bogus"))
	require.Error(t, err, "unknown component segment is an error, not a panic")

	_, err = drive(t, f, modalIx("supply:m_bogus", map[string]string{"x": "y"}, nil))
	require.Error(t, err, "unknown modal segment is an error, not a panic")
}

// --- create modal ---

func TestSubmitCreate_Validation(t *testing.T) {
	// Empty title → bad_name, no Create, no forum lookup.
	repo := mocks.NewMockRepository(t)
	f := testFeature(t, repo, nil)
	r, err := drive(t, f, modalIx("supply:m_new", map[string]string{"title": "   "}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r.content)

	// Title set but no forum configured → no_forum, no Create.
	repo2 := mocks.NewMockRepository(t)
	f2 := testFeatureWith(t, repo2, nil, func(forum *mocks.MockForumConfig, _ *mocks.MockLimitConfig) {
		forum.EXPECT().SupplyForumChannelID(mock.Anything, mock.Anything).Return("", false)
	})
	r2, err := drive(t, f2, modalIx("supply:m_new", map[string]string{"title": "need parts"}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r2.content)
}

func TestSubmitCreate_LimitReached(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := testFeatureWith(t, repo, nil, func(forum *mocks.MockForumConfig, limit *mocks.MockLimitConfig) {
		forum.EXPECT().SupplyForumChannelID(mock.Anything, mock.Anything).Return("forum", true)
		limit.EXPECT().SupplyRequestLimit(mock.Anything, mock.Anything).Return(3, true)
	})
	repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(in supply.CreateInput) bool {
		return in.OwnerUserID == tUser && in.OpenLimit == 3 && in.Title == "need parts"
	})).Return(uuid.Nil, supply.ErrLimit).Once()

	r, err := drive(t, f, modalIx("supply:m_new", map[string]string{"title": "need parts"}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r.content, "limit_reached rendered")
}

// --- reference-link modal (the added requirement) ---

func TestSubmitRef(t *testing.T) {
	rid := uuid.New()
	okProg := supply.Progress{Request: supply.Request{ID: rid, Status: supply.StatusOpen}}

	// Valid in-guild link → SetMessageRef with the three parsed ids, then re-render.
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().SetMessageRef(mock.Anything, mock.Anything, tUser, rid, tGuild, "222", "333").Return(nil).Once()
	repo.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(okProg, nil).Once()
	f := testFeature(t, repo, nil)
	_, err := drive(t, f, modalIx("supply:m_rref:"+rid.String(),
		map[string]string{"ref_link": "https://discord.com/channels/" + tGuild + "/222/333"}, nil))
	require.NoError(t, err)

	// Wrong-guild link → bad_link, no write.
	repo2 := mocks.NewMockRepository(t)
	f2 := testFeature(t, repo2, nil)
	r2, err := drive(t, f2, modalIx("supply:m_rref:"+rid.String(),
		map[string]string{"ref_link": "https://discord.com/channels/999999999999999999/222/333"}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r2.content, "bad_link rendered; SetMessageRef never called")

	// Malformed link → bad_link, no write.
	repo3 := mocks.NewMockRepository(t)
	f3 := testFeature(t, repo3, nil)
	r3, err := drive(t, f3, modalIx("supply:m_rref:"+rid.String(), map[string]string{"ref_link": "not a link"}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r3.content)

	// Empty input clears the reference (all three ids empty).
	repo4 := mocks.NewMockRepository(t)
	repo4.EXPECT().SetMessageRef(mock.Anything, mock.Anything, tUser, rid, "", "", "").Return(nil).Once()
	repo4.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(okProg, nil).Once()
	f4 := testFeature(t, repo4, nil)
	_, err = drive(t, f4, modalIx("supply:m_rref:"+rid.String(), map[string]string{"ref_link": ""}, nil))
	require.NoError(t, err)
}

// --- system modal ---

func TestSubmitSystem(t *testing.T) {
	rid := uuid.New()
	okProg := supply.Progress{Request: supply.Request{ID: rid, Status: supply.StatusOpen}}

	// Bad planet → bad_planet, no write.
	repo := mocks.NewMockRepository(t)
	f := testFeature(t, repo, nil)
	r, err := drive(t, f, modalIx("supply:m_rsys:"+rid.String(),
		map[string]string{"system_name": "Muvalis", "system_code": "QR", "planet": "0"}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r.content)

	// Valid inputs persist; empty planet clears (nil).
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().SetSystemInfo(mock.Anything, mock.Anything, tUser, rid, "Muvalis", "QR", mock.MatchedBy(func(p *int) bool {
		return p != nil && *p == 15
	})).Return(nil).Once()
	repo2.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(okProg, nil).Once()
	f2 := testFeature(t, repo2, nil)
	_, err = drive(t, f2, modalIx("supply:m_rsys:"+rid.String(),
		map[string]string{"system_name": "Muvalis", "system_code": "QR", "planet": "15"}, nil))
	require.NoError(t, err)
}

// --- close modal (typed confirm) ---

func TestSubmitClose(t *testing.T) {
	rid := uuid.New()
	prog := supply.Progress{Request: supply.Request{ID: rid, Title: "need parts", Status: supply.StatusOpen}}

	// Mismatch → close_mismatch, no Cancel.
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(prog, nil).Once()
	f := testFeature(t, repo, nil)
	r, err := drive(t, f, modalIx("supply:m_rclose:"+rid.String(), map[string]string{"confirm": "wrong"}, nil))
	require.NoError(t, err)
	assert.NotEmpty(t, r.content)

	// Match (case-insensitive) → Cancel, then re-render.
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().ProgressByIDOwned(mock.Anything, mock.Anything, tUser, rid).Return(prog, nil).Twice()
	repo2.EXPECT().Cancel(mock.Anything, mock.Anything, tUser, rid).Return(nil).Once()
	f2 := testFeature(t, repo2, nil)
	_, err = drive(t, f2, modalIx("supply:m_rclose:"+rid.String(), map[string]string{"confirm": "NEED PARTS"}, nil))
	require.NoError(t, err)
}

// --- public panel ---

func TestPanel_NoneEligible(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Progress(mock.Anything, mock.Anything, tThread).Return(
		supply.Progress{Request: supply.Request{Status: supply.StatusOpen}}, nil).Once()
	f := testFeature(t, repo, nil)
	r, err := drive(t, f, componentIx("supply:panel:reserve"))
	require.NoError(t, err)
	assert.NotEmpty(t, r.content, "none_reserve rendered when nothing is left to reserve")
}

func TestPanel_DeliverSentinelAndCompleted(t *testing.T) {
	// Over-reserved delivery → over_reserved.
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Deliver(mock.Anything, mock.Anything, tThread, "IronOre", tUser, 5).Return(false, supply.ErrOverReserved).Once()
	f := testFeature(t, repo, nil)
	r, err := drive(t, f, modalIx("supply:qty:deliver",
		map[string]string{"qty": "5"}, map[string]string{"item": "IronOre"}))
	require.NoError(t, err)
	assert.NotEmpty(t, r.content)

	// Delivery completing the request → the "completed" confirmation.
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().Deliver(mock.Anything, mock.Anything, tThread, "IronOre", tUser, 5).Return(true, nil).Once()
	f2 := testFeature(t, repo2, nil)
	r2, err := drive(t, f2, modalIx("supply:qty:deliver",
		map[string]string{"qty": "5"}, map[string]string{"item": "IronOre"}))
	require.NoError(t, err)
	assert.NotEmpty(t, r2.content)
}
