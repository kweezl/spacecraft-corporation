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
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/emoji"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

var gid = uuid.New()

const thread = "thread-1"

// testRegistry is the real compiled-in gamedata (pure data, no I/O), shared by
// every handler test.
var testRegistry = func() *gamedata.Registry {
	reg, err := gamedata.Load(nil, nil)
	if err != nil {
		panic(err)
	}
	return reg
}()

// staticLang resolves every server to a fixed language (the picker's analog of
// i18n.StaticResolver).
type staticLang struct{ lang i18n.Language }

func (s staticLang) Resolve(context.Context, uuid.UUID) (string, i18n.Language) {
	return "standard", s.lang
}

// staticDefaults is a RewardDefaults resolving every server to a fixed factor
// (writes are no-op accepted).
type staticDefaults struct{ factor decimal.Decimal }

func (s staticDefaults) ContractsRewardFactor(context.Context, uuid.UUID) decimal.Decimal {
	return s.factor
}
func (s staticDefaults) SetContractsRewardFactor(context.Context, uuid.UUID, decimal.Decimal) error {
	return nil
}

// staticItemCap is an ItemCap resolving every server to a fixed cap; ok reports
// whether one is "set" (false → the feature falls back to DefaultMaxItems).
type staticItemCap struct {
	limit int
	set   bool
}

func (s staticItemCap) ContractsMaxItems(context.Context, uuid.UUID) (int, bool) {
	return s.limit, s.set
}
func (s staticItemCap) SetContractsMaxItems(context.Context, uuid.UUID, int) error { return nil }

// deps bundles the optional Feature dependencies a test may want to override;
// the zero value gives strict template-repo/search mocks, a zero reward-factor
// default, no emojis, English.
type featureDeps struct {
	tpls     contracts.TemplateRepository
	search   contracts.GameSearch
	access   session.CommandAccess
	defaults contracts.RewardDefaults
	itemCap  contracts.ItemCap
	reports  contracts.ReportsConfig
	emo      *emoji.Store
	// lang overrides the server's rendered + content language (default en).
	lang i18n.Language
}

func newFeature(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig) *contracts.Feature {
	return newFeatureDeps(t, repo, gw, forum, featureDeps{})
}

func newFeatureAccess(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig, access session.CommandAccess) *contracts.Feature {
	return newFeatureDeps(t, repo, gw, forum, featureDeps{access: access})
}

// newFeatureReports builds a Feature with a specific ReportsConfig (the payout
// task + reprint paths resolve the reports channel through it).
func newFeatureReports(t *testing.T, repo contracts.Repository, gw contracts.Gateway, reports contracts.ReportsConfig) *contracts.Feature {
	return newFeatureDeps(t, repo, gw, mocks.NewMockForumConfig(t), featureDeps{reports: reports})
}

func newFeatureDeps(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig, d featureDeps) *contracts.Feature {
	t.Helper()
	if d.lang == "" {
		d.lang = i18n.LanguageEN
	}
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: d.lang})
	if d.tpls == nil {
		d.tpls = mocks.NewMockTemplateRepository(t)
	}
	if d.search == nil {
		d.search = mocks.NewMockGameSearch(t)
	}
	if d.defaults == nil {
		d.defaults = staticDefaults{}
	}
	if d.reports == nil {
		d.reports = mocks.NewMockReportsConfig(t)
	}
	if d.itemCap == nil {
		d.itemCap = staticItemCap{} // unset → DefaultMaxItems (25)
	}
	return contracts.New(repo, d.tpls, loc, contracts.Config{PageSize: 8}, gw, forum, d.reports, d.defaults, d.itemCap, d.access,
		d.search, staticLang{lang: d.lang}, testRegistry, d.emo, zap.NewNop())
}

// newFeatureObserved is newFeature with a log observer, for asserting warnings.
func newFeatureObserved(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig) (*contracts.Feature, *observer.ObservedLogs) {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	core, logs := observer.New(zapcore.WarnLevel)
	return contracts.New(repo, mocks.NewMockTemplateRepository(t), loc, contracts.Config{PageSize: 8}, gw, forum, mocks.NewMockReportsConfig(t), staticDefaults{}, staticItemCap{}, nil,
		mocks.NewMockGameSearch(t), staticLang{lang: i18n.LanguageEN}, testRegistry, nil, zap.New(core)), logs
}

// fakeAccess grants exactly the keys in its set; everything else is denied. It
// keys on AccessRequest.Command only (the contracts gate passes the permission
// key there), so role wiring is irrelevant to these tests.
type fakeAccess struct{ allow map[string]bool }

func grant(keys ...string) fakeAccess {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return fakeAccess{allow: m}
}

func (f fakeAccess) IsAllowed(_ context.Context, req session.AccessRequest) (bool, error) {
	return f.allow[req.Command], nil
}

// buttonIDs collects every button CustomID in a Components V2 tree (action rows,
// section accessories, nested containers) so a test can assert which buttons a
// view rendered.
func buttonIDs(comps []discordgo.MessageComponent) []string {
	var ids []string
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.ActionsRow:
			ids = append(ids, buttonIDs(v.Components)...)
		case discordgo.Container:
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

// sectionAccessoryIDs collects the accessory button CustomIDs of every Section in
// a component tree (recursing into Containers) — i.e. buttons rendered inline next
// to their text, not in a standalone action row.
func sectionAccessoryIDs(comps []discordgo.MessageComponent) []string {
	var ids []string
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.Container:
			ids = append(ids, sectionAccessoryIDs(v.Components)...)
		case discordgo.Section:
			if b, ok := v.Accessory.(discordgo.Button); ok {
				ids = append(ids, b.CustomID)
			}
		}
	}
	return ids
}

// isPostCard reports whether comps is the single-Container post card and whether
// it carries the reserve/deliver/release action buttons (open contracts) — used
// to assert the forum-post shape the Gateway is handed.
func isPostCard(comps []discordgo.MessageComponent, wantButtons bool) bool {
	if len(comps) != 1 {
		return false
	}
	if _, ok := comps[0].(discordgo.Container); !ok {
		return false
	}
	hasButtons := len(buttonIDs(comps)) > 0
	return hasButtons == wantButtons
}

func has(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func member(id string) *discordgo.Member { return &discordgo.Member{User: &discordgo.User{ID: id}} }

// consoleCmd builds the /contracts slash invocation (the console takes no options).
func consoleCmd(m *discordgo.Member) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: "g", Member: m,
		Data: discordgo.ApplicationCommandInteractionData{Name: "contracts"},
	}}
}

func run(t *testing.T, f *contracts.Feature, r *capture, i *discordgo.InteractionCreate) {
	t.Helper()
	require.NoError(t, f.Command().Handler(context.Background(), r, i, gid))
}

// --- capturing responder ---

type capture struct {
	content         string
	embed           *discordgo.MessageEmbed
	components      []discordgo.MessageComponent
	updated         bool
	modalCustomID   string
	modalTitle      string
	modalComponents []discordgo.MessageComponent
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
func (c *capture) RespondAutocomplete(_ *discordgo.Interaction, _ []*discordgo.ApplicationCommandOptionChoice) error {
	return nil
}
func (c *capture) RespondEmbedComponents(_ *discordgo.Interaction, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.embed, c.components = e, comps
	return nil
}
func (c *capture) RespondEmbedComponentsEphemeral(_ *discordgo.Interaction, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.embed, c.components = e, comps
	return nil
}
func (c *capture) UpdateMessage(_ *discordgo.Interaction, e *discordgo.MessageEmbed, comps []discordgo.MessageComponent) error {
	c.embed, c.components, c.updated = e, comps, true
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
func (c *capture) RespondModal(_ *discordgo.Interaction, customID, title string, comps []discordgo.MessageComponent) error {
	c.modalCustomID, c.modalTitle, c.modalComponents = customID, title, comps
	return nil
}

// --- console tests ---

func TestConsole_CommandAccessIsDiscordManaged(t *testing.T) {
	f := newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	cmd := f.Command()
	assert.Equal(t, "contracts", cmd.Def.Name)
	assert.True(t, cmd.DiscordManaged, "who can open /contracts is configured in Discord, not granted by the bot")
	assert.False(t, cmd.DefaultDeny, "the bot does not coarse-gate /contracts")
	// Two grantable keys remain: the participant panel key and the single manager
	// key gating every console modification. The old per-kind keys are gone.
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.use")
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.manage")
	assert.NotContains(t, cmd.ExtraAccessKeys, "contracts.custom")
	assert.NotContains(t, cmd.ExtraAccessKeys, "contracts.template")
	assert.NotContains(t, cmd.ExtraAccessKeys, "contracts.templates")
	assert.NotContains(t, cmd.ExtraAccessKeys, "contracts.republish")
}

func TestConsole_OpensDashboard(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// The console opens at the dashboard, which renders the per-status tally.
	repo.EXPECT().Counts(mock.Anything, gid).Return(contracts.Counts{Active: 2, Unpublished: 1}, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r, consoleCmd(member("officer")))
	// The dashboard is a single Components V2 Container holding the stats and the
	// nav/create buttons (no permissions feature here, so create buttons show).
	require.Len(t, r.components, 1)
	assert.True(t, has(buttonIDs(r.components), "contract:golist"), "the list button is inside the card")
}

func TestConsole_DashboardOpensList(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// "List contracts" navigates to the list at the default active filter, page 0.
	repo.EXPECT().List(mock.Anything, gid, []contracts.Status{contracts.StatusOpen}, 3, 0).Return(nil, 0, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:golist"), gid))
	require.NotEmpty(t, r.components)
	assert.True(t, r.updated, "opening the list edits the console in place")
}

func TestConsole_TemplateOpensPickList(t *testing.T) {
	tpls := mocks.NewMockTemplateRepository(t)
	tid := uuid.New()
	tpls.EXPECT().ListTemplates(mock.Anything, gid, "", 3, 0).
		Return([]contracts.TemplateListEntry{{ID: tid, Title: "Weekly Steel", ItemCount: 2}}, 1, nil).Once()

	r := &capture{}
	f := newFeatureDeps(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{tpls: tpls})
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:tmpl"), gid))
	require.NotEmpty(t, r.components)
	assert.True(t, r.updated, "the template button opens the pick list in place")
	ids := buttonIDs(r.components)
	assert.True(t, has(ids, "contract:tuse:"+tid.String()), "each template row carries a Use accessory")
	assert.True(t, has(ids, "contract:tsearch:p"), "the pick list has a search button")
}

func TestConsole_OpenContract(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	dl := time.Now().Add(2 * time.Hour)
	prog := contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, ThreadID: "t9", Title: "Steel Run", Status: contracts.StatusOpen, Kind: contracts.KindCustom, Deadline: &dl, LastRefreshedAt: time.Now()},
		Items:    []contracts.Item{{ID: uuid.New(), Name: "Steel", RequiredQty: 100}},
	}
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(prog, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:view:"+cid.String()), gid))
	require.NotEmpty(t, r.components)
	assert.True(t, r.updated, "drilling into a contract edits the console in place")
}

func TestConsole_Republish(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	// Republish first verifies the contract is still open (closed ones are read-only).
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom, LastRefreshedAt: time.Now()},
	}, nil).Once()
	repo.EXPECT().Republish(mock.Anything, gid, cid).Return(contracts.RepublishCreating, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:crepub:"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "republish gives ephemeral feedback")
}

func TestConsole_Filter(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Selecting completed+cancelled re-runs List at page 0 with that set.
	repo.EXPECT().List(mock.Anything, gid, mock.MatchedBy(func(ss []contracts.Status) bool {
		return len(ss) == 2
	}), 3, 0).Return(nil, 0, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:cfilter", "completed", "cancelled"), gid))
	require.NotEmpty(t, r.components)
}

func TestConsole_CreateOpensModal(t *testing.T) {
	r := &capture{}
	f := newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:create"), gid))
	assert.Equal(t, "contract:m_create", r.modalCustomID)
	assert.NotEmpty(t, r.modalComponents)
}

// --- permission gating ---

func TestConsole_DashboardHidesCreateButtonsWithoutPerm(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Counts(mock.Anything, gid).Return(contracts.Counts{}, nil).Twice()

	// A manager sees every authoring button (custom + template + library); the list
	// button always shows.
	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	run(t, f, r, consoleCmd(member("officer")))
	ids := buttonIDs(r.components)
	assert.True(t, has(ids, "contract:create"), "custom-create shown for a manager")
	assert.True(t, has(ids, "contract:tmpl"), "template-create shown for a manager")
	assert.True(t, has(ids, "contract:tlist:0:"), "templates-library shown for a manager")
	assert.True(t, has(ids, "contract:golist"), "list button is always shown")

	// A non-manager sees none of the authoring buttons, only the list button.
	r2 := &capture{}
	f2 := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.use"))
	run(t, f2, r2, consoleCmd(member("nobody")))
	ids2 := buttonIDs(r2.components)
	assert.False(t, has(ids2, "contract:create"))
	assert.False(t, has(ids2, "contract:tmpl"))
	assert.False(t, has(ids2, "contract:tlist:0:"))
	assert.True(t, has(ids2, "contract:golist"))
}

func TestConsole_CreateCustomDeniedWithoutPerm(t *testing.T) {
	// No Create/modal expectations: the gate must block before any handler runs.
	// The member holds no keys, so creating a custom contract is denied.
	repo := mocks.NewMockRepository(t)
	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant())
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("nobody"), "contract:create"), gid))
	assert.NotEmpty(t, r.content, "denied member gets an ephemeral notice")
	assert.Empty(t, r.modalCustomID, "no create modal is opened")
}

func TestConsole_AdminBypassesCreateGate(t *testing.T) {
	r := &capture{}
	f := newFeatureAccess(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant())
	admin := &discordgo.Member{User: &discordgo.User{ID: "admin"}, Permissions: discordgo.PermissionAdministrator}
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", admin, "contract:create"), gid))
	assert.Equal(t, "contract:m_create", r.modalCustomID, "administrators bypass the create gate")
}

func TestConsole_AddItemRequiresManagerKey(t *testing.T) {
	// Add-item opens the gamedata browser for a contract manager; a non-manager is
	// denied before the browser opens. Kind no longer affects the key.
	cid := uuid.New()
	r := &capture{}
	f := newFeatureAccess(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:cadd:"+cid.String()), gid))
	assert.True(t, r.updated, "the item browser opens for a manager")
	require.NotEmpty(t, r.components)

	// A participant (non-manager) is denied the same click.
	r2 := &capture{}
	f2 := newFeatureAccess(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.use"))
	require.NoError(t, f2.Component().Handler(context.Background(), r2, component("", member("participant"), "contract:cadd:"+cid.String()), gid))
	assert.NotEmpty(t, r2.content, "a non-manager gets an ephemeral denial")
	assert.False(t, r2.updated, "the browser does not open")
}

func TestConsole_EditModalRequiresManagerKey(t *testing.T) {
	cid := uuid.New()
	dl := time.Now().Add(2 * time.Hour)
	repo := mocks.NewMockRepository(t)
	// The manager key opens the edit modal (no kind resolution); the opener loads it.
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindTemplate, Deadline: &dl, LastRefreshedAt: time.Now()},
	}, nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:cedit:"+cid.String()), gid))
	assert.Equal(t, "contract:m_cedit:"+cid.String(), r.modalCustomID, "edit modal opens for a manager")
}

func TestConsole_ItemViewParticipantEditIsInline(t *testing.T) {
	itemID := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByItemScoped(mock.Anything, gid, itemID).Return(contracts.Progress{
		Contract: contracts.Contract{ID: uuid.New(), ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom, Title: "Steel Run", LastRefreshedAt: time.Now()},
		Items: []contracts.Item{{ID: itemID, Name: "Steel", RequiredQty: 100, ReservedQty: 60, Participants: []contracts.Participant{
			{UserID: "u1", Reserved: 60, Delivered: 10},
		}}},
	}, nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:irow:"+itemID.String()), gid))
	// The participant's Edit button is a Section accessory (inline with its line),
	// not a standalone action row.
	assert.True(t, has(sectionAccessoryIDs(r.components), "contract:pedit:"+itemID.String()+":u1"),
		"participant Edit must be an inline Section accessory")
}

func TestConsole_ParticipantManageRequiresManagePerm(t *testing.T) {
	cid := uuid.New()
	itemID := uuid.New()
	// Participant management needs the manager key; a participant (contracts.use)
	// is refused before any repository call.
	repo := mocks.NewMockRepository(t)

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.use"))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("participant"), "contract:pedit:"+itemID.String()+":"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "denied without contracts.manage")
	assert.Empty(t, r.modalCustomID, "no participant modal opened")
}

// participantSubmit builds the participant-manage modal submit (action select + qty).
func participantSubmit(m *discordgo.Member, itemID, userID, action, qty string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: "g", Member: m,
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: "contract:m_pedit:" + itemID + ":" + userID,
			Components: []discordgo.MessageComponent{
				&discordgo.Label{Component: &discordgo.SelectMenu{CustomID: "paction", Values: []string{action}}},
				&discordgo.Label{Component: &discordgo.TextInput{CustomID: "qty", Value: qty}},
			},
		},
	}}
}

func TestConsole_ParticipantUpdateReserveIsRelative(t *testing.T) {
	itemID := uuid.New()
	cid := uuid.New()
	// A participant with 2 reserved / 2 delivered (nothing outstanding). Updating
	// the reserve to 1 means total reserved = delivered(2) + 1 = 3.
	prog := contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindCustom, Title: "Steel Run", LastRefreshedAt: time.Now()},
		Items: []contracts.Item{{ID: itemID, Name: "Steel", RequiredQty: 100, ReservedQty: 2, Participants: []contracts.Participant{
			{UserID: "u1", Reserved: 2, Delivered: 2},
		}}},
	}
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByItemScoped(mock.Anything, gid, itemID).Return(prog, nil) // submit + re-render
	repo.EXPECT().SetReservationByItem(mock.Anything, gid, itemID, "u1", 3, "officer").Return(cid, nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.manage"))
	require.NoError(t, f.Component().Handler(context.Background(), r, participantSubmit(member("officer"), itemID.String(), "u1", "set", "1"), gid))
}
