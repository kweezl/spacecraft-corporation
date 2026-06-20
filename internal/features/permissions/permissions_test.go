package permissions_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/features/permissions"
	"github.com/kweezl/spacecraft-corporation/internal/features/permissions/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// g1 is a fixed resolved servers.id used across the gate/command tests (the
// session would resolve the snowflake to this before the handler runs).
var g1 = uuid.New()

func testLoc(t *testing.T) *i18n.Localizer {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

type fakeResponder struct{ last string }

func (f *fakeResponder) Respond(_ *discordgo.Interaction, content string) error {
	f.last = content
	return nil
}
func (f *fakeResponder) RespondEphemeral(_ *discordgo.Interaction, content string) error {
	f.last = content
	return nil
}

func (f *fakeResponder) RespondEmbed(_ *discordgo.Interaction, embed *discordgo.MessageEmbed) error {
	if embed != nil {
		f.last = embed.Title
	}
	return nil
}
func (f *fakeResponder) RespondAutocomplete(_ *discordgo.Interaction, _ []*discordgo.ApplicationCommandOptionChoice) error {
	return nil
}
func (f *fakeResponder) RespondEmbedComponents(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	if embed != nil {
		f.last = embed.Title
	}
	return nil
}
func (f *fakeResponder) UpdateMessage(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	if embed != nil {
		f.last = embed.Title
	}
	return nil
}

func newStore(t *testing.T, repo permissions.Repository) *permissions.Store {
	t.Helper()
	s, err := permissions.NewStore(repo)
	require.NoError(t, err)
	return s
}

// --- Gate (reads through the per-server cache, which loads via Repository.List) ---

func TestGate_NoMapping_RequiredDenies(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Once()

	ok, err := permissions.NewGate(newStore(t, repo)).IsAllowed(context.Background(), session.AccessRequest{
		ServerID: g1, Command: "locked", DefaultDeny: true,
	})
	require.NoError(t, err)
	assert.False(t, ok, "a required command with no mapping is denied")
}

func TestGate_NoMapping_OptionalAllows(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Once()

	ok, err := permissions.NewGate(newStore(t, repo)).IsAllowed(context.Background(), session.AccessRequest{
		ServerID: g1, Command: "ping", DefaultDeny: false,
	})
	require.NoError(t, err)
	assert.True(t, ok, "an optional command with no mapping is open")
}

func TestGate_Mapping_AnyOfRoleMatches(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{
		{Command: "ping", RoleID: "admin"},
		{Command: "ping", RoleID: "mod"},
	}, nil).Once()

	ok, err := permissions.NewGate(newStore(t, repo)).IsAllowed(context.Background(), session.AccessRequest{
		ServerID: g1, Command: "ping", UserRoles: []string{"member", "mod"}, DefaultDeny: false,
	})
	require.NoError(t, err)
	assert.True(t, ok, "holding any one mapped role grants access")
}

func TestGate_Mapping_NoMatchingRoleDenies(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{
		{Command: "ping", RoleID: "admin"},
	}, nil).Once()

	ok, err := permissions.NewGate(newStore(t, repo)).IsAllowed(context.Background(), session.AccessRequest{
		ServerID: g1, Command: "ping", UserRoles: []string{"member"}, DefaultDeny: false,
	})
	require.NoError(t, err)
	assert.False(t, ok, "a mapped command denies a member without any mapped role")
}

func TestGate_RepoError(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, assert.AnError).Once()

	_, err := permissions.NewGate(newStore(t, repo)).IsAllowed(context.Background(), session.AccessRequest{
		ServerID: g1, Command: "ping",
	})
	require.Error(t, err)
}

// TestGate_CachesPerServer asserts the server's mapping is loaded once and then
// served from cache: List is mocked .Once(), so a second DB load would fail.
func TestGate_CachesPerServer(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{
		{Command: "ping", RoleID: "r1"},
	}, nil).Once()
	gate := permissions.NewGate(newStore(t, repo))

	for range 3 {
		ok, err := gate.IsAllowed(context.Background(), session.AccessRequest{
			ServerID: g1, Command: "ping", UserRoles: []string{"r1"},
		})
		require.NoError(t, err)
		assert.True(t, ok)
	}
}

// TestStore_InvalidatesOnWrite asserts a grant drops the cache so the gate sees
// the new mapping on its next check (the second List returns the updated set).
func TestStore_InvalidatesOnWrite(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Once() // before: unmapped
	repo.EXPECT().Grant(mock.Anything, g1, "ping", "r1", "u1").Return(nil).Once()
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{
		{Command: "ping", RoleID: "r1"},
	}, nil).Once() // after: mapped to r1

	store := newStore(t, repo)
	gate := permissions.NewGate(store)
	req := session.AccessRequest{ServerID: g1, Command: "ping", UserRoles: []string{"member"}, DefaultDeny: false}

	ok, err := gate.IsAllowed(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, ok, "no mapping yet → optional command open")

	require.NoError(t, store.Grant(context.Background(), g1, "ping", "r1", "u1"))

	ok, err = gate.IsAllowed(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, ok, "after the grant the cache reloads; member lacks r1 → denied")
}

// --- /permissions command ---

func permInteraction(sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "g1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "permissions",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts},
			},
		},
	}}
}

func opt(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value,
	}
}

func TestCommand_IsDefaultDeny(t *testing.T) {
	cmd := permissions.NewCommand(newStore(t, mocks.NewMockRepository(t)), testLoc(t))
	assert.True(t, cmd.DefaultDeny, "/permissions is owner/admin-only by default")
	assert.Equal(t, "permissions", cmd.Def.Name)
}

func TestCommand_Grant(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Grant(mock.Anything, g1, "ping", "r1", "u1").Return(nil).Once()

	resp := &fakeResponder{}
	err := permissions.NewCommand(newStore(t, repo), testLoc(t)).Handler(context.Background(), resp,
		permInteraction("grant", opt("command", "ping"), opt("role", "r1")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "<@&r1>")
	assert.Contains(t, resp.last, "/ping")
}

func TestCommand_Revoke(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Revoke(mock.Anything, g1, "ping", "r1").Return(nil).Once()

	resp := &fakeResponder{}
	err := permissions.NewCommand(newStore(t, repo), testLoc(t)).Handler(context.Background(), resp,
		permInteraction("revoke", opt("command", "ping"), opt("role", "r1")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "Revoked")
}

func TestCommand_Clear(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Clear(mock.Anything, g1, "ping").Return(nil).Once()

	resp := &fakeResponder{}
	err := permissions.NewCommand(newStore(t, repo), testLoc(t)).Handler(context.Background(), resp,
		permInteraction("clear", opt("command", "ping")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "Cleared")
}

func TestCommand_ListForCommand(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().RolesFor(mock.Anything, g1, "ping").Return([]string{"r1", "r2"}, nil).Once()

	resp := &fakeResponder{}
	err := permissions.NewCommand(newStore(t, repo), testLoc(t)).Handler(context.Background(), resp,
		permInteraction("list", opt("command", "ping")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "<@&r1>")
	assert.Contains(t, resp.last, "<@&r2>")
}

func TestCommand_ListForCommand_Empty(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().RolesFor(mock.Anything, g1, "ping").Return(nil, nil).Once()

	resp := &fakeResponder{}
	err := permissions.NewCommand(newStore(t, repo), testLoc(t)).Handler(context.Background(), resp,
		permInteraction("list", opt("command", "ping")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "default access")
}

func TestCommand_ListAll(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{
		{Command: "ping", RoleID: "r1"},
		{Command: "permissions", RoleID: "r9"},
	}, nil).Once()

	resp := &fakeResponder{}
	err := permissions.NewCommand(newStore(t, repo), testLoc(t)).Handler(context.Background(), resp,
		permInteraction("list"), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "/ping")
	assert.Contains(t, resp.last, "/permissions")
}

// TestModule provides the gate and command into the graph. A pool is provided
// for the repository; pgxpool connects lazily, so no live DB is needed.
func TestModule(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://user:pass@localhost:5432/db")
	require.NoError(t, err)
	defer pool.Close()

	var reg *registry.Registry
	var gate session.CommandAccess
	app := fxtest.New(t,
		fx.Provide(func() *pgxpool.Pool { return pool }),
		fx.Provide(prometheus.NewRegistry),
		fx.Supply(testLoc(t)),
		permissions.Module(),
		registry.Module(),
		fx.Populate(&reg, &gate),
	)
	app.RequireStart()
	defer app.RequireStop()

	require.NotNil(t, gate, "module exposes the session CommandAccess gate")
	cmds := reg.Commands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "permissions", cmds[0].Name)
}
