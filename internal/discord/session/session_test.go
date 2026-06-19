package session

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/appconfig"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

type fakeDiscord struct {
	token          string
	opened, closed bool
	connected      bool
	created        []created
	interaction    func(*discordgo.InteractionCreate)
	guildCreate    []func(*discordgo.GuildCreate)
	lastReply      string
}

type created struct {
	serverID string
	name     string
}

func (f *fakeDiscord) AddInteractionHandler(fn func(*discordgo.InteractionCreate)) {
	f.interaction = fn
}
func (f *fakeDiscord) AddGuildCreateHandler(fn func(*discordgo.GuildCreate)) {
	f.guildCreate = append(f.guildCreate, fn)
}
func (f *fakeDiscord) AddGuildDeleteHandler(func(*discordgo.GuildDelete)) {}
func (f *fakeDiscord) Open() error                                        { f.opened = true; f.connected = true; return nil }
func (f *fakeDiscord) Close() error                                       { f.closed = true; f.connected = false; return nil }
func (f *fakeDiscord) Connected() bool                                    { return f.connected }
func (f *fakeDiscord) CreateCommand(serverID string, cmd *discordgo.ApplicationCommand) error {
	f.created = append(f.created, created{serverID: serverID, name: cmd.Name})
	return nil
}
func (f *fakeDiscord) Respond(_ *discordgo.Interaction, content string) error {
	f.lastReply = content
	return nil
}

// fireGuildCreate invokes every registered GuildCreate handler, mimicking
// discordgo delivering the event.
func (f *fakeDiscord) fireGuildCreate(id string) {
	for _, h := range f.guildCreate {
		h(&discordgo.GuildCreate{Guild: &discordgo.Guild{ID: id}})
	}
}

func (f *fakeDiscord) fireCommand(serverID string) {
	f.interaction(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: serverID,
		Data:    discordgo.ApplicationCommandInteractionData{Name: "ping"},
	}})
}

// gateFunc adapts a func to the ServerApproval interface.
type gateFunc func(serverID string) bool

func (g gateFunc) IsApproved(_ context.Context, serverID string) (bool, error) {
	return g(serverID), nil
}

func newTestRegistry() *registry.Registry {
	cmd := &registry.Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r registry.Responder, i *discordgo.InteractionCreate) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	return registry.New(registry.Params{Commands: []*registry.Command{cmd}})
}

func startManager(t *testing.T, gate ServerApproval) *fakeDiscord {
	t.Helper()
	return startManagerWithApp(t, gate, appconfig.AppConfig{})
}

func startManagerWithApp(t *testing.T, gate ServerApproval, app appconfig.AppConfig) *fakeDiscord {
	t.Helper()
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, gate, nil, nil, zap.NewNop(), app)
	require.NoError(t, m.Start(context.Background()))
	require.NotNil(t, fake)
	assert.True(t, fake.opened)
	return fake
}

func TestManager_RegistersCommandsPerJoinedServer(t *testing.T) {
	fake := startManager(t, gateFunc(func(string) bool { return true }))

	// Nothing is registered until a server is joined.
	assert.Empty(t, fake.created)

	fake.fireGuildCreate("g1")
	require.Len(t, fake.created, 1)
	assert.Equal(t, "g1", fake.created[0].serverID)
	assert.Equal(t, "ping", fake.created[0].name)
}

func TestManager_DispatchesFromApprovedServer(t *testing.T) {
	fake := startManager(t, gateFunc(func(id string) bool { return id == "g1" }))
	fake.fireCommand("g1")
	assert.Equal(t, "pong", fake.lastReply)
}

func TestManager_RepliesToUnapprovedServer(t *testing.T) {
	fake := startManager(t, gateFunc(func(id string) bool { return id == "g1" }))
	fake.fireCommand("g2")
	assert.Contains(t, fake.lastReply, "isn't approved",
		"unapproved server should get an approval-required reply")
}

func TestManager_UnapprovedReply_MentionsOwnerWhenSet(t *testing.T) {
	fake := startManagerWithApp(t,
		gateFunc(func(string) bool { return false }),
		appconfig.AppConfig{OwnerDiscordID: "12345"})
	fake.fireCommand("g2")
	assert.Contains(t, fake.lastReply, "<@12345>", "owner should be mentioned when configured")
}

func TestManager_IgnoresDirectMessages(t *testing.T) {
	fake := startManager(t, gateFunc(func(string) bool { return true }))
	fake.fireCommand("") // no GuildID == DM
	assert.Empty(t, fake.lastReply)
}

func TestManager_NilGate_ApprovesEverything(t *testing.T) {
	fake := startManager(t, nil)
	fake.fireCommand("g1")
	assert.Equal(t, "pong", fake.lastReply)
}

func TestManager_Stop_ClosesSession(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, nil, nil, nil, zap.NewNop(), appconfig.AppConfig{})
	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Stop(context.Background()))
	assert.True(t, fake.closed)
}

func TestReadinessCheck_ReflectsGatewayLifecycle(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, nil, nil, nil, zap.NewNop(), appconfig.AppConfig{})
	probe := newReadinessCheck(m).Probe

	// Not ready before the session is opened.
	assert.Error(t, probe(context.Background()))

	require.NoError(t, m.Start(context.Background()))
	assert.NoError(t, probe(context.Background()), "ready once the gateway is connected")

	require.NoError(t, m.Stop(context.Background()))
	assert.Error(t, probe(context.Background()), "not ready again after the session closes")
}

func TestConfig_BotToken_PrefersFile(t *testing.T) {
	// TokenFile holds the file's contents (env resolves the ,file option).
	got, err := Config{Token: "from-env", TokenFile: "from-file\n"}.botToken()
	require.NoError(t, err)
	assert.Equal(t, "from-file", got) // trimmed
}

func TestConfig_BotToken_FallsBackToEnv(t *testing.T) {
	got, err := Config{Token: "from-env"}.botToken()
	require.NoError(t, err)
	assert.Equal(t, "from-env", got)
}

func TestConfig_BotToken_Missing(t *testing.T) {
	_, err := Config{}.botToken()
	require.Error(t, err)
}
