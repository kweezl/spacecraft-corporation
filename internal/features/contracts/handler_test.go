package contracts_test

import (
	"context"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
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

// deps bundles the optional Feature dependencies a test may want to override;
// the zero value gives strict template-repo/search mocks, no emojis, English.
type featureDeps struct {
	tpls   contracts.TemplateRepository
	search contracts.GameSearch
	access session.CommandAccess
	emo    *emoji.Store
}

func newFeature(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig) *contracts.Feature {
	return newFeatureDeps(t, repo, gw, forum, featureDeps{})
}

func newFeatureAccess(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig, access session.CommandAccess) *contracts.Feature {
	return newFeatureDeps(t, repo, gw, forum, featureDeps{access: access})
}

func newFeatureDeps(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig, d featureDeps) *contracts.Feature {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	if d.tpls == nil {
		d.tpls = mocks.NewMockTemplateRepository(t)
	}
	if d.search == nil {
		d.search = mocks.NewMockGameSearch(t)
	}
	return contracts.New(repo, d.tpls, loc, contracts.Config{PageSize: 8, MaxItems: 25}, gw, forum, d.access,
		d.search, staticLang{lang: i18n.LanguageEN}, testRegistry, d.emo, zap.NewNop())
}

// newFeatureObserved is newFeature with a log observer, for asserting warnings.
func newFeatureObserved(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig) (*contracts.Feature, *observer.ObservedLogs) {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	core, logs := observer.New(zapcore.WarnLevel)
	return contracts.New(repo, mocks.NewMockTemplateRepository(t), loc, contracts.Config{PageSize: 8, MaxItems: 25}, gw, forum, nil,
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
	// The fine-grained create/edit keys and the public panel key remain grantable.
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.use")
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.custom")
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.template")
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.republish")
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

	// Granted only the custom key: custom-create shows, template-create hidden.
	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.custom"))
	run(t, f, r, consoleCmd(member("officer")))
	ids := buttonIDs(r.components)
	assert.True(t, has(ids, "contract:create"), "custom-create button shown when granted")
	assert.False(t, has(ids, "contract:tmpl"), "template-create button hidden without grant")
	assert.True(t, has(ids, "contract:golist"), "list button is always shown")

	// Granted nothing: neither create button shows, only the list button.
	r2 := &capture{}
	f2 := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant())
	run(t, f2, r2, consoleCmd(member("nobody")))
	ids2 := buttonIDs(r2.components)
	assert.False(t, has(ids2, "contract:create"))
	assert.False(t, has(ids2, "contract:tmpl"))
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

func TestConsole_TemplateContractItemEditAllowed(t *testing.T) {
	// A template is defaults only: contracts created from one are fully editable,
	// so add-item opens the item browser under the template key.
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().KindByID(mock.Anything, gid, cid).Return(contracts.KindTemplate, nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.template"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:cadd:"+cid.String()), gid))
	assert.True(t, r.updated, "the item browser opens for a template contract")
	require.NotEmpty(t, r.components)

	// Without the template key the same click is denied.
	r2 := &capture{}
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().KindByID(mock.Anything, gid, cid).Return(contracts.KindTemplate, nil).Once()
	f2 := newFeatureAccess(t, repo2, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.custom"))
	require.NoError(t, f2.Component().Handler(context.Background(), r2, component("", member("officer"), "contract:cadd:"+cid.String()), gid))
	assert.NotEmpty(t, r2.content, "denied member gets an ephemeral notice")
	assert.False(t, r2.updated, "the browser does not open")
}

func TestConsole_TemplateContractEditAllowed(t *testing.T) {
	cid := uuid.New()
	dl := time.Now().Add(2 * time.Hour)
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().KindByID(mock.Anything, gid, cid).Return(contracts.KindTemplate, nil).Once()
	// Editing a template is allowed (deadline only); the modal opener loads it.
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, Status: contracts.StatusOpen, Kind: contracts.KindTemplate, Deadline: &dl, LastRefreshedAt: time.Now()},
	}, nil).Once()

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.template"))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:cedit:"+cid.String()), gid))
	assert.Equal(t, "contract:m_cedit:"+cid.String(), r.modalCustomID, "edit modal opens for a template contract")
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
	// keyManage is a fixed-key gate (no kind resolution), so a member without it is
	// refused before any repository call.
	repo := mocks.NewMockRepository(t)

	r := &capture{}
	f := newFeatureAccess(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), grant("contracts.custom"))
	require.NoError(t, f.Component().Handler(context.Background(), r,
		component("", member("officer"), "contract:pedit:"+itemID.String()+":"+cid.String()), gid))
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
