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
	f, err := contracts.New(repo, loc, contracts.Config{PageSize: 8, MaxItems: 25}, gw, forum, zap.NewNop())
	require.NoError(t, err)
	return f
}

func run(t *testing.T, f *contracts.Feature, r *capture, i *discordgo.InteractionCreate) {
	t.Helper()
	require.NoError(t, f.Command().Handler(context.Background(), r, i, gid))
}

// --- interaction builders ---

type opt = discordgo.ApplicationCommandInteractionDataOption

func member(id string) *discordgo.Member { return &discordgo.Member{User: &discordgo.User{ID: id}} }

func strv(name, val string) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: val}
}
func intv(name string, val int) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionInteger, Value: float64(val)}
}
func userv(name, id string) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionUser, Value: id}
}

func leaf(name string, opts ...*opt) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts}
}
func group(name string, l *opt) *opt {
	return &opt{Name: name, Type: discordgo.ApplicationCommandOptionSubCommandGroup, Options: []*opt{l}}
}

func cmd(channelID string, m *discordgo.Member, top *opt) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: "g", ChannelID: channelID, Member: m,
		Data: discordgo.ApplicationCommandInteractionData{Name: "contract", Options: []*opt{top}},
	}}
}

// --- capturing responder ---

type capture struct {
	content string
	embed   *discordgo.MessageEmbed
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
func (c *capture) RespondEmbedComponents(_ *discordgo.Interaction, e *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	c.embed = e
	return nil
}
func (c *capture) UpdateMessage(_ *discordgo.Interaction, e *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	c.embed = e
	return nil
}

// Command handlers are now DB-only and ack immediately; the Discord side effects
// run on the outbox worker (see tasks_test.go). So these tests assert the repo
// call + the ack, and the Gateway is never touched on the interaction path.

func TestCreate_AcksAndPersists(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	forum := mocks.NewMockForumConfig(t)
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("forum-1", true).Once()
	repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(in contracts.CreateInput) bool {
		return in.Title == "Steel Run" && in.Deadline.After(time.Now()) && in.Token == "tok" && in.AppID == "app"
	})).Return(uuid.New(), nil).Once()

	r := &capture{}
	i := cmd("", member("u1"), leaf("create", strv("title", "Steel Run"), strv("duration", "4d 12h")))
	i.AppID, i.Token = "app", "tok"
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), forum), r, i)
	assert.Contains(t, r.content, "accepted")
}

func TestCreate_NoForum(t *testing.T) {
	forum := mocks.NewMockForumConfig(t)
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("", false).Once()

	r := &capture{}
	run(t, newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), forum), r,
		cmd("", member("u1"), leaf("create", strv("title", "X"), strv("duration", "1d"))))
	assert.Contains(t, r.content, "forum")
}

func TestCreate_BadDuration(t *testing.T) {
	r := &capture{}
	run(t, newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd("", member("u1"), leaf("create", strv("title", "X"), strv("duration", "soon"))))
	assert.Contains(t, r.content, "Nd Nh Nm")
}

func TestItemAdd_OK(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().AddItem(mock.Anything, gid, thread, "Steel", 500, 25, "u1").Return(nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("u1"), group("item", leaf("add", strv("name", "Steel"), intv("qty", 500)))))
	assert.Contains(t, r.content, "Steel")
}

func TestItemAdd_MaxItems(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().AddItem(mock.Anything, gid, thread, "Steel", 1, 25, "u1").Return(contracts.ErrMaxItems).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("u1"), group("item", leaf("add", strv("name", "Steel"), intv("qty", 1)))))
	assert.Contains(t, r.content, "25")
}

func TestParticipate_OverCap(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Participate(mock.Anything, gid, thread, "Steel", "u1", 100).Return(contracts.ErrOverCap).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("u1"), leaf("participate", strv("item", "Steel"), intv("qty", 100))))
	assert.NotEmpty(t, r.content)
}

func TestDeliver_Completes(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Deliver(mock.Anything, gid, thread, "Steel", "u1", 40).Return(true, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("u1"), leaf("deliver", strv("item", "Steel"), intv("qty", 40))))
	assert.NotEmpty(t, r.content)
}

func TestDeliver_NotInThread(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Deliver(mock.Anything, gid, thread, "Steel", "u1", 1).Return(false, contracts.ErrNotFound).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("u1"), leaf("deliver", strv("item", "Steel"), intv("qty", 1))))
	assert.Contains(t, r.content, "thread")
}

func TestRelease_Self(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Release(mock.Anything, gid, thread, "Steel", "u1", 10, "u1").Return(nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("u1"), leaf("release", strv("item", "Steel"), intv("qty", 10))))
	assert.NotEmpty(t, r.content)
}

func TestReleaseMember_TargetsOption(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// actor is the invoker (officer); target is the member option.
	repo.EXPECT().Release(mock.Anything, gid, thread, "Steel", "u2", 5, "officer").Return(nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("officer"), leaf("release-member", userv("member", "u2"), strv("item", "Steel"), intv("qty", 5))))
	assert.NotEmpty(t, r.content)
}

func TestReleaseMember_NoTarget(t *testing.T) {
	r := &capture{}
	run(t, newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd(thread, member("officer"), leaf("release-member", strv("item", "Steel"), intv("qty", 5))))
	assert.NotEmpty(t, r.content)
}

func TestSetForum_OK(t *testing.T) {
	forum := mocks.NewMockForumConfig(t)
	forum.EXPECT().SetContractsForumChannelID(mock.Anything, gid, "chan-7").Return(nil).Once()

	r := &capture{}
	run(t, newFeature(t, mocks.NewMockRepository(t), mocks.NewMockGateway(t), forum), r,
		cmd("anywhere", member("admin"), leaf("forum", &opt{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "chan-7"})))
	assert.Contains(t, r.content, "chan-7")
}

func TestList_Empty(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, gid, "", 8, 0).Return(nil, 0, nil).Once()

	r := &capture{}
	run(t, newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t)), r,
		cmd("anywhere", member("u1"), leaf("list")))
	require.NotNil(t, r.embed)
	assert.NotEmpty(t, r.embed.Title)
}
