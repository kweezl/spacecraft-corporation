package session

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/appconfig"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// testLoc builds a Localizer over the real bundles, fixed to standard/en.
func testLoc() *i18n.Localizer {
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	if err != nil {
		panic(err)
	}
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

type fakeDiscord struct {
	token          string
	opened, closed bool
	connected      bool
	created        []created
	interaction    func(*discordgo.InteractionCreate)
	guildCreate    []func(*discordgo.GuildCreate)
	lastReply      string
	lastChoices    []*discordgo.ApplicationCommandOptionChoice
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
func (f *fakeDiscord) RespondEphemeral(_ *discordgo.Interaction, content string) error {
	f.lastReply = content
	return nil
}
func (f *fakeDiscord) RespondEmbed(_ *discordgo.Interaction, embed *discordgo.MessageEmbed) error {
	if embed != nil {
		f.lastReply = embed.Title
	}
	return nil
}
func (f *fakeDiscord) RespondAutocomplete(_ *discordgo.Interaction, choices []*discordgo.ApplicationCommandOptionChoice) error {
	f.lastChoices = choices
	return nil
}
func (f *fakeDiscord) RespondEmbedComponents(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	if embed != nil {
		f.lastReply = embed.Title
	}
	return nil
}
func (f *fakeDiscord) UpdateMessage(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	if embed != nil {
		f.lastReply = embed.Title
	}
	return nil
}
func (f *fakeDiscord) ForumThreadStartComplex(_ string, threadData *discordgo.ThreadStart, _ *discordgo.MessageSend) (*discordgo.Channel, error) {
	name := ""
	if threadData != nil {
		name = threadData.Name
	}
	return &discordgo.Channel{ID: "thread-1", Name: name}, nil
}
func (f *fakeDiscord) ChannelMessageEditComplex(_ *discordgo.MessageEdit) (*discordgo.Message, error) {
	return &discordgo.Message{}, nil
}
func (f *fakeDiscord) ChannelEditComplex(_ string, _ *discordgo.ChannelEdit) (*discordgo.Channel, error) {
	return &discordgo.Channel{}, nil
}
func (f *fakeDiscord) InteractionResponseEdit(_ *discordgo.Interaction, _ *discordgo.WebhookEdit) (*discordgo.Message, error) {
	return &discordgo.Message{}, nil
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

func (f *fakeDiscord) fireAutocomplete(serverID, name string) {
	f.interaction(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommandAutocomplete,
		GuildID: serverID,
		Data:    discordgo.ApplicationCommandInteractionData{Name: name},
	}})
}

func (f *fakeDiscord) fireComponent(serverID, customID string) {
	f.interaction(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionMessageComponent,
		GuildID: serverID,
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID},
	}})
}

// gateFunc adapts an approval predicate to the ServerResolver interface. The
// resolved id is uuid.Nil here — the test command handler ignores it.
type gateFunc func(serverID string) bool

func (g gateFunc) Resolve(_ context.Context, serverID string) (uuid.UUID, bool, error) {
	return uuid.Nil, g(serverID), nil
}

func newTestRegistry() *registry.Registry {
	cmd := &registry.Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r registry.Responder, i *discordgo.InteractionCreate, _ uuid.UUID) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	return registry.New(registry.Params{Commands: []*registry.Command{cmd}})
}

func startManager(t *testing.T, resolver ServerResolver) *fakeDiscord {
	t.Helper()
	return startManagerWithApp(t, resolver, appconfig.AppConfig{})
}

func startManagerWithApp(t *testing.T, resolver ServerResolver, app appconfig.AppConfig) *fakeDiscord {
	t.Helper()
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, resolver, nil, testLoc(), nil, nil, zap.NewNop(), app, newLive())
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

// routingRegistry builds a registry that records whether autocomplete and
// component dispatch were reached.
func routingRegistry(autoCalled, compCalled *bool) *registry.Registry {
	return registry.New(registry.Params{
		Commands: []*registry.Command{{
			Def: &discordgo.ApplicationCommand{Name: "base"},
			Autocomplete: func(context.Context, *discordgo.InteractionCreate, uuid.UUID) ([]*discordgo.ApplicationCommandOptionChoice, error) {
				*autoCalled = true
				return nil, nil
			},
		}},
		Components: []*registry.Component{{
			Prefix: "base",
			Handler: func(context.Context, registry.Responder, *discordgo.InteractionCreate, uuid.UUID) error {
				*compCalled = true
				return nil
			},
		}},
	})
}

func startWithRegistry(t *testing.T, reg *registry.Registry, resolver ServerResolver) *fakeDiscord {
	t.Helper()
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, reg, factory, resolver, nil, testLoc(), nil, nil, zap.NewNop(), appconfig.AppConfig{}, newLive())
	require.NoError(t, m.Start(context.Background()))
	require.NotNil(t, fake)
	return fake
}

func TestManager_RoutesAutocompleteAndComponentsFromApprovedServer(t *testing.T) {
	var autoCalled, compCalled bool
	fake := startWithRegistry(t, routingRegistry(&autoCalled, &compCalled),
		gateFunc(func(id string) bool { return id == "g1" }))

	fake.fireAutocomplete("g1", "base")
	assert.True(t, autoCalled, "autocomplete from an approved server is dispatched")

	fake.fireComponent("g1", "base:list:tok:1")
	assert.True(t, compCalled, "component from an approved server is dispatched")
}

func TestManager_AutocompleteFromUnapprovedServer_NotDispatched(t *testing.T) {
	var autoCalled, compCalled bool
	fake := startWithRegistry(t, routingRegistry(&autoCalled, &compCalled),
		gateFunc(func(string) bool { return false }))

	fake.fireAutocomplete("g2", "base")
	assert.False(t, autoCalled, "no suggestions are produced for an unapproved server")

	fake.fireComponent("g2", "base:list:tok:1")
	assert.False(t, compCalled, "components from an unapproved server are ignored")
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

// accessFunc adapts a func to the CommandAccess interface.
type accessFunc func(req AccessRequest) (bool, error)

func (f accessFunc) IsAllowed(_ context.Context, req AccessRequest) (bool, error) {
	return f(req)
}

// gateRegistry has an open command ("ping") and a locked one ("locked").
func gateRegistry() *registry.Registry {
	return registry.New(registry.Params{Commands: []*registry.Command{
		{Def: &discordgo.ApplicationCommand{Name: "ping"}},
		{Def: &discordgo.ApplicationCommand{Name: "locked"}, DefaultDeny: true},
	}})
}

// managerWithAccess builds a Manager wired with the given access gate, bypassing
// Start (allowed needs only registry/access/log).
func managerWithAccess(access CommandAccess) *Manager {
	return newManager(Config{}, gateRegistry(), nil, nil, access, testLoc(), nil, nil, zap.NewNop(), appconfig.AppConfig{}, newLive())
}

func interactionAs(command string, member *discordgo.Member) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "g1",
		Member:  member,
		Data:    discordgo.ApplicationCommandInteractionData{Name: command},
	}}
}

func TestManager_Allowed_NilGate_AllowsEverything(t *testing.T) {
	m := managerWithAccess(nil)
	member := &discordgo.Member{Roles: []string{"r1"}}
	assert.True(t, m.allowed(context.Background(), interactionAs("locked", member), uuid.Nil))
}

func TestManager_Allowed_AdminBypassesGate(t *testing.T) {
	denyAll := accessFunc(func(AccessRequest) (bool, error) { return false, nil })
	m := managerWithAccess(denyAll)
	admin := &discordgo.Member{Permissions: discordgo.PermissionAdministrator}
	assert.True(t, m.allowed(context.Background(), interactionAs("locked", admin), uuid.Nil),
		"owner/admin bypasses the gate even when it would deny")
}

func TestManager_Allowed_ConsultsGateForNonAdmin(t *testing.T) {
	var got AccessRequest
	gate := accessFunc(func(req AccessRequest) (bool, error) { got = req; return true, nil })
	m := managerWithAccess(gate)
	member := &discordgo.Member{Roles: []string{"r1", "r2"}}
	srv := uuid.New()

	assert.True(t, m.allowed(context.Background(), interactionAs("locked", member), srv))
	assert.Equal(t, srv, got.ServerID, "the resolved server id is passed through to the gate")
	assert.Equal(t, "locked", got.Command)
	assert.Equal(t, []string{"r1", "r2"}, got.UserRoles)
	assert.True(t, got.DefaultDeny, "locked command carries its deny-by-default policy")
}

func TestManager_Allowed_SubcommandGated_KeysOnPath(t *testing.T) {
	var got AccessRequest
	gate := accessFunc(func(req AccessRequest) (bool, error) { got = req; return true, nil })
	reg := registry.New(registry.Params{Commands: []*registry.Command{{
		Def: &discordgo.ApplicationCommand{
			Name: "base",
			Options: []*discordgo.ApplicationCommandOption{{
				Name: "own",
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "register", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			}},
		},
		DefaultDeny:     true,
		SubcommandGated: true,
	}}})
	m := newManager(Config{}, reg, nil, nil, gate, testLoc(), nil, nil, zap.NewNop(), appconfig.AppConfig{}, newLive())

	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "g1",
		Member:  &discordgo.Member{Roles: []string{"r1"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "base",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{{
				Name: "own",
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
				Options: []*discordgo.ApplicationCommandInteractionDataOption{{
					Name: "register",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				}},
			}},
		},
	}}

	assert.True(t, m.allowed(context.Background(), i, uuid.Nil))
	assert.Equal(t, "base own register", got.Command, "the gate authorizes the full subcommand path")
	assert.True(t, got.DefaultDeny, "the command's deny-by-default policy applies to its subcommands")
}

func TestManager_Allowed_GateDenies(t *testing.T) {
	gate := accessFunc(func(AccessRequest) (bool, error) { return false, nil })
	m := managerWithAccess(gate)
	member := &discordgo.Member{Roles: []string{"r1"}}
	assert.False(t, m.allowed(context.Background(), interactionAs("locked", member), uuid.Nil))
}

func TestManager_Allowed_GateErrorFailsClosed(t *testing.T) {
	gate := accessFunc(func(AccessRequest) (bool, error) { return true, assert.AnError })
	m := managerWithAccess(gate)
	member := &discordgo.Member{Roles: []string{"r1"}}
	assert.False(t, m.allowed(context.Background(), interactionAs("locked", member), uuid.Nil),
		"a gate error denies access")
}

func TestManager_Allowed_UnknownCommandNotGated(t *testing.T) {
	gate := accessFunc(func(AccessRequest) (bool, error) { return false, nil })
	m := managerWithAccess(gate)
	member := &discordgo.Member{Roles: []string{"r1"}}
	assert.True(t, m.allowed(context.Background(), interactionAs("ghost", member), uuid.Nil),
		"an unknown command isn't gated here; dispatch surfaces the error")
}

func TestManager_BlockedCommand_RepliesDenied(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	denyAll := accessFunc(func(AccessRequest) (bool, error) { return false, nil })
	gate := gateFunc(func(string) bool { return true })
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, gate, denyAll, testLoc(), nil, nil,
		zap.NewNop(), appconfig.AppConfig{}, newLive())
	require.NoError(t, m.Start(context.Background()))

	fake.fireCommand("g1") // non-admin (no Member), approved server, gate denies
	assert.Contains(t, fake.lastReply, "don't have permission")
}

func TestManager_Stop_ClosesSession(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, nil, nil, testLoc(), nil, nil, zap.NewNop(), appconfig.AppConfig{}, newLive())
	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Stop(context.Background()))
	assert.True(t, fake.closed)
}

func TestReadinessCheck_ReflectsGatewayLifecycle(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(Config{Token: "tok-1"}, newTestRegistry(), factory, nil, nil, testLoc(), nil, nil, zap.NewNop(), appconfig.AppConfig{}, newLive())
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
