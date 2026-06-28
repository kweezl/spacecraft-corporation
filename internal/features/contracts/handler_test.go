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

	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

var gid = uuid.New()

const thread = "thread-1"

func newFeature(t *testing.T, repo contracts.Repository, gw contracts.Gateway, forum contracts.ForumConfig) *contracts.Feature {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	return contracts.New(repo, loc, contracts.Config{PageSize: 8, MaxItems: 25}, gw, forum, nil, zap.NewNop())
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

func TestConsole_CommandIsCoarseGated(t *testing.T) {
	f := newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	cmd := f.Command()
	assert.Equal(t, "contracts", cmd.Def.Name)
	assert.True(t, cmd.DefaultDeny)
	assert.False(t, cmd.SubcommandGated, "the console is one coarse gate, not per-subcommand")
	assert.Contains(t, cmd.ExtraAccessKeys, "contracts.use", "the public panel key is grantable via /permissions")
}

func TestConsole_OpensList(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Default filter is active (open) only, page 0.
	repo.EXPECT().List(mock.Anything, gid, []contracts.Status{contracts.StatusOpen}, 3, 0).Return(nil, 0, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r, consoleCmd(member("officer")))
	// The console is a Components V2 message: a container plus the filter row and
	// the [Prev/Next] + Create row are always present (even with no contracts).
	require.NotEmpty(t, r.components)
	assert.GreaterOrEqual(t, len(r.components), 3)
}

func TestConsole_OpenContract(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	dl := time.Now().Add(2 * time.Hour)
	prog := contracts.Progress{
		Contract: contracts.Contract{ID: cid, ServerID: gid, ThreadID: "t9", Title: "Steel Run", Status: contracts.StatusOpen, Deadline: &dl, LastRefreshedAt: time.Now()},
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
