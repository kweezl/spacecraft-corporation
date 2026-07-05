package bases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/bases"
	"github.com/kweezl/spacecraft-corporation/internal/features/bases/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Discord-facing names are asserted literally here: they are the wire contract,
// so a rename should fail a test, not pass silently.
const (
	cmdName = "base"
	tOwn    = "own"
	tCorp   = "corp"
	tMember = "member"

	oRegister  = "register"
	oUnregist  = "unregister"
	oAddExt    = "add-extractor"
	oRemovePrd = "remove-production"
	oList      = "list"

	pName     = "name"
	pSector   = "sector"
	pSystem   = "system"
	pPlanet   = "planet"
	pMember   = "member"
	pBase     = "base"
	pResource = "resource"
	pProdOpt  = "production"

	allValue = "all"
)

var gid = uuid.New()

func newFeature(t *testing.T, repo bases.Repository) *bases.Feature {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	f, err := bases.New(repo, loc, bases.Config{
		MemberLimit: 3, CorpLimit: 6, ExtractorLimit: 4, ProductionLimit: 30, PageSize: 8,
	})
	require.NoError(t, err)
	return f
}

func run(t *testing.T, f *bases.Feature, r *capture, i *discordgo.InteractionCreate) {
	t.Helper()
	require.NoError(t, f.Command().Handler(context.Background(), r, i, gid))
}

// --- interaction builders ---

type opt = discordgo.ApplicationCommandInteractionDataOption

func member(id string) *discordgo.Member { return &discordgo.Member{User: &discordgo.User{ID: id}} }

func strv(name, val string) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: val}
}
func userv(name, id string) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionUser, Value: id}
}
func intv(name string, val int) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionInteger, Value: float64(val)}
}
func focusedv(name, val string) *opt {
	o := strv(name, val)
	o.Focused = true
	return o
}

func tierCmd(typ discordgo.InteractionType, tier, op string, m *discordgo.Member, opts ...*opt) *discordgo.InteractionCreate {
	leaf := &opt{Name: op, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts}
	top := &opt{Name: tier, Type: discordgo.ApplicationCommandOptionSubCommandGroup, Options: []*opt{leaf}}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: typ, GuildID: "g", Member: m,
		Data: discordgo.ApplicationCommandInteractionData{Name: cmdName, Options: []*opt{top}},
	}}
}

func cmd(tier, op string, m *discordgo.Member, opts ...*opt) *discordgo.InteractionCreate {
	return tierCmd(discordgo.InteractionApplicationCommand, tier, op, m, opts...)
}

func listCmd(opts ...*opt) *discordgo.InteractionCreate {
	top := &opt{Name: oList, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: "g",
		Data: discordgo.ApplicationCommandInteractionData{Name: cmdName, Options: []*opt{top}},
	}}
}

// --- capturing responder ---

type capture struct {
	content    string
	embed      *discordgo.MessageEmbed
	components []discordgo.MessageComponent
	choices    []*discordgo.ApplicationCommandOptionChoice
	updated    bool
}

func (c *capture) Respond(_ *discordgo.Interaction, s string) error { c.content = s; return nil }
func (c *capture) RespondEphemeral(_ *discordgo.Interaction, s string) error {
	c.content = s
	return nil
}
func (c *capture) RespondEmbed(_ *discordgo.Interaction, e *discordgo.MessageEmbed) error {
	c.embed = e
	return nil
}
func (c *capture) RespondAutocomplete(_ *discordgo.Interaction, ch []*discordgo.ApplicationCommandOptionChoice) error {
	c.choices = ch
	return nil
}
func (c *capture) RespondEmbedComponents(_ *discordgo.Interaction, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.embed, c.components = e, comps
	return nil
}
func (c *capture) RespondEmbedComponentsEphemeral(i *discordgo.Interaction, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	return c.RespondEmbedComponents(i, e, comps)
}
func (c *capture) UpdateMessage(_ *discordgo.Interaction, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.embed, c.components, c.updated = e, comps, true
	return nil
}
func (c *capture) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, _ []discordgo.MessageComponent) error {
	return nil
}
func (c *capture) UpdateComponentsV2(_ *discordgo.Interaction, _ []discordgo.MessageComponent) error {
	return nil
}
func (c *capture) RespondModal(_ *discordgo.Interaction, _, _ string, _ []discordgo.MessageComponent) error {
	return nil
}

// --- register ---

func TestHandle_RegisterOwn(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Register(mock.Anything, mock.MatchedBy(func(in bases.RegisterInput) bool {
		return in.Kind == bases.KindMember && in.OwnerUserID == "u1" && in.CreatedByUserID == "u1" &&
			in.Name == "Alpha" && in.SectorName == "Orion" && in.SystemCode == "SOL" && in.PlanetNumber == 3
	}), 3).Return(uuid.New(), nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r,
		cmd(tOwn, oRegister, member("u1"), strv(pName, "Alpha"), strv(pSector, "Orion"), strv(pSystem, "SOL"), intv(pPlanet, 3)))
	assert.Contains(t, r.content, "Alpha")
	assert.Contains(t, r.content, "III", "planet renders as a Roman numeral")
}

func TestHandle_RegisterCorp_UsesCorpLimit(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Register(mock.Anything, mock.MatchedBy(func(in bases.RegisterInput) bool {
		return in.Kind == bases.KindCorp && in.OwnerUserID == "" && in.CreatedByUserID == "u1"
	}), 6).Return(uuid.New(), nil).Once()

	run(t, newFeature(t, repo), &capture{},
		cmd(tCorp, oRegister, member("u1"), strv(pName, "HQ"), strv(pSector, "Orion"), strv(pSystem, "SOL"), intv(pPlanet, 1)))
}

func TestHandle_RegisterMember_TargetsNamedMember(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Register(mock.Anything, mock.MatchedBy(func(in bases.RegisterInput) bool {
		return in.Kind == bases.KindMember && in.OwnerUserID == "u2" && in.CreatedByUserID == "mgr"
	}), 3).Return(uuid.New(), nil).Once()

	run(t, newFeature(t, repo), &capture{},
		cmd(tMember, oRegister, member("mgr"), userv(pMember, "u2"),
			strv(pName, "Alpha"), strv(pSector, "Orion"), strv(pSystem, "SOL"), intv(pPlanet, 3)))
}

func TestHandle_RegisterLimitReached(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Register(mock.Anything, mock.Anything, 3).Return(uuid.Nil, bases.ErrLimitReached).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r,
		cmd(tOwn, oRegister, member("u1"), strv(pName, "A"), strv(pSector, "S"), strv(pSystem, "Y"), intv(pPlanet, 2)))
	assert.Contains(t, r.content, "3", "the limit is surfaced to the user")
}

func TestHandle_MemberTier_RequiresMemberOption(t *testing.T) {
	repo := mocks.NewMockRepository(t) // no repo call expected
	r := &capture{}
	run(t, newFeature(t, repo), r, cmd(tMember, oUnregist, member("mgr"), strv(pBase, allValue)))
	assert.NotEmpty(t, r.content)
}

// --- unregister ---

func TestHandle_UnregisterAll(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().DeleteAll(mock.Anything, bases.MemberOwnership(gid, "u1")).Return(2, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r, cmd(tOwn, oUnregist, member("u1"), strv(pBase, allValue)))
	assert.Contains(t, r.content, "2")
}

func TestHandle_UnregisterOne_NotFound(t *testing.T) {
	id := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().DeleteOne(mock.Anything, bases.MemberOwnership(gid, "u1"), id).Return(0, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r, cmd(tOwn, oUnregist, member("u1"), strv(pBase, id.String())))
	assert.Contains(t, r.content, "isn't yours")
}

// --- equipment ---

func TestHandle_AddExtractor_LimitReached(t *testing.T) {
	id := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().AddExtractor(mock.Anything, bases.MemberOwnership(gid, "u1"), id, "Iron", 4).
		Return(bases.ErrLimitReached).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r, cmd(tOwn, oAddExt, member("u1"), strv(pBase, id.String()), strv(pResource, "Iron")))
	assert.Contains(t, r.content, "4")
}

func TestHandle_RemoveProduction_NotFound(t *testing.T) {
	id := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().RemoveProduction(mock.Anything, bases.MemberOwnership(gid, "u1"), id).Return(0, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r,
		cmd(tOwn, oRemovePrd, member("u1"), strv(pBase, uuid.New().String()), strv(pProdOpt, id.String())))
	assert.NotEmpty(t, r.content)
}

// --- list + pagination (end-to-end through the button CustomID) ---

func TestHandle_List_Empty(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, gid, bases.Filter{}, 8, 0).Return(nil, 0, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo), r, listCmd())
	require.NotNil(t, r.embed)
	assert.Empty(t, r.components, "no buttons when there's nothing to page")
}

func TestHandle_ListThenPaginate(t *testing.T) {
	page := []bases.Base{{ID: uuid.New(), Kind: bases.KindMember, OwnerUserID: "u1", Name: "Alpha", SectorName: "Orion", SystemCode: "SOL", PlanetNumber: 3}}
	repo := mocks.NewMockRepository(t)
	// 20 total over page size 8 → 3 pages → pagination buttons present.
	repo.EXPECT().List(mock.Anything, gid, bases.Filter{SectorName: "Orion"}, 8, 0).Return(page, 20, nil).Once()

	f := newFeature(t, repo)
	r := &capture{}
	run(t, f, r, listCmd(strv(pSector, "Orion")))
	require.NotNil(t, r.embed)
	require.Len(t, r.embed.Fields, 3, "one base renders as three inline columns")
	nextID := nextButtonID(t, r.components)

	// Clicking "next" re-runs the stored filter at the next offset and edits in place.
	repo.EXPECT().List(mock.Anything, gid, bases.Filter{SectorName: "Orion"}, 8, 8).Return(page, 20, nil).Once()
	rc := &capture{}
	ci := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: "g",
		Data: discordgo.MessageComponentInteractionData{CustomID: nextID},
	}}
	require.NoError(t, f.Component().Handler(context.Background(), rc, ci, gid))
	assert.True(t, rc.updated, "pagination edits the message in place")
}

func TestHandle_Component_ExpiredToken(t *testing.T) {
	repo := mocks.NewMockRepository(t) // List must NOT be called
	f := newFeature(t, repo)
	r := &capture{}
	ci := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: "g",
		Data: discordgo.MessageComponentInteractionData{CustomID: cmdName + ":list:missing-token:1"},
	}}
	require.NoError(t, f.Component().Handler(context.Background(), r, ci, gid))
	assert.True(t, r.updated)
	require.NotNil(t, r.embed)
	assert.Empty(t, r.components, "an expired listing drops its buttons")
}

// nextButtonID returns the CustomID of the "next" pagination button.
func nextButtonID(t *testing.T, comps []discordgo.MessageComponent) string {
	t.Helper()
	require.NotEmpty(t, comps)
	row, ok := comps[0].(discordgo.ActionsRow)
	require.True(t, ok)
	require.Len(t, row.Components, 2)
	btn, ok := row.Components[1].(discordgo.Button)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(btn.CustomID, cmdName+":list:"))
	return btn.CustomID
}

// --- autocomplete ---

func TestAutocomplete_UnregisterBasePicker_IncludesAll(t *testing.T) {
	id := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ListOwned(mock.Anything, bases.MemberOwnership(gid, "u1"), 25).
		Return([]bases.Base{{ID: id, Name: "Alpha", SectorName: "Orion", SystemCode: "SOL", PlanetNumber: 3}}, nil).Once()

	f := newFeature(t, repo)
	i := tierCmd(discordgo.InteractionApplicationCommandAutocomplete, tOwn, oUnregist, member("u1"), focusedv(pBase, ""))
	choices, err := f.Command().Autocomplete(context.Background(), i, gid)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(choices), 2)
	assert.Equal(t, allValue, choices[0].Value, "the All sentinel is offered first")
	assert.Equal(t, id.String(), choices[1].Value)
}

func TestAutocomplete_AddExtractor_NoAllEntry(t *testing.T) {
	id := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ListOwned(mock.Anything, bases.MemberOwnership(gid, "u1"), 25).
		Return([]bases.Base{{ID: id, Name: "Alpha", SectorName: "Orion", SystemCode: "SOL", PlanetNumber: 3}}, nil).Once()

	f := newFeature(t, repo)
	i := tierCmd(discordgo.InteractionApplicationCommandAutocomplete, tOwn, oAddExt, member("u1"),
		focusedv(pBase, ""), strv(pResource, ""))
	choices, err := f.Command().Autocomplete(context.Background(), i, gid)
	require.NoError(t, err)
	require.Len(t, choices, 1)
	assert.Equal(t, id.String(), choices[0].Value)
}
