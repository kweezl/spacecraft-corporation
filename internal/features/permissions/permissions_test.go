package permissions_test

import (
	"context"
	"strings"
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

// g1 is a fixed resolved servers.id used across the tests (the session would
// resolve the snowflake to this before the handler runs).
var g1 = uuid.New()

func testLoc(t *testing.T) *i18n.Localizer {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

// fakeResponder records the last response so panel tests can inspect it.
type fakeResponder struct {
	last        string
	components  []discordgo.MessageComponent
	respondedV2 bool
	updatedV2   bool
}

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
func (f *fakeResponder) RespondEmbedComponentsEphemeral(i *discordgo.Interaction, embed *discordgo.MessageEmbed, c []discordgo.MessageComponent) error {
	return f.RespondEmbedComponents(i, embed, c)
}
func (f *fakeResponder) UpdateMessage(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	if embed != nil {
		f.last = embed.Title
	}
	return nil
}
func (f *fakeResponder) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, components []discordgo.MessageComponent) error {
	f.components = components
	f.respondedV2 = true
	return nil
}
func (f *fakeResponder) UpdateComponentsV2(_ *discordgo.Interaction, components []discordgo.MessageComponent) error {
	f.components = components
	f.updatedV2 = true
	return nil
}
func (f *fakeResponder) RespondModal(_ *discordgo.Interaction, _, _ string, _ []discordgo.MessageComponent) error {
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

// TestStore_InvalidatesOnWrite asserts a write drops the cache so the gate sees
// the new mapping on its next check (the second List returns the updated set).
func TestStore_InvalidatesOnWrite(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Once() // before: unmapped
	repo.EXPECT().SetRoles(mock.Anything, g1, "ping", []string{"r1"}, "u1").Return(nil).Once()
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{
		{Command: "ping", RoleID: "r1"},
	}, nil).Once() // after: mapped to r1

	store := newStore(t, repo)
	gate := permissions.NewGate(store)
	req := session.AccessRequest{ServerID: g1, Command: "ping", UserRoles: []string{"member"}, DefaultDeny: false}

	ok, err := gate.IsAllowed(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, ok, "no mapping yet → optional command open")

	require.NoError(t, store.SetRoles(context.Background(), g1, "ping", []string{"r1"}, "u1"))

	ok, err = gate.IsAllowed(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, ok, "after the write the cache reloads; member lacks r1 → denied")
}

// --- /permissions panel ---

func cmdInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "g1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data:    discordgo.ApplicationCommandInteractionData{Name: "permissions"},
	}}
}

// compInteraction is a panel component click by an administrator (the common
// case: the panel re-authorizes every interaction, admins bypass the gate).
func compInteraction(customID string, values ...string) *discordgo.InteractionCreate {
	admin := &discordgo.Member{User: &discordgo.User{ID: "u1"}, Permissions: discordgo.PermissionAdministrator}
	return compInteractionBy(admin, customID, values...)
}

func compInteractionBy(member *discordgo.Member, customID string, values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionMessageComponent,
		GuildID: "g1",
		Member:  member,
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID, Values: values},
	}}
}

// textOf concatenates the TextDisplay content in a component tree.
func textOf(comps []discordgo.MessageComponent) string {
	var b strings.Builder
	for _, c := range comps {
		if td, ok := c.(discordgo.TextDisplay); ok {
			b.WriteString(td.Content)
		}
	}
	return b.String()
}

// roleSelects indexes every role-picker in a component tree by its CustomID.
func roleSelects(comps []discordgo.MessageComponent) map[string]discordgo.SelectMenu {
	out := map[string]discordgo.SelectMenu{}
	for _, c := range comps {
		row, ok := c.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, rc := range row.Components {
			if sm, ok := rc.(discordgo.SelectMenu); ok {
				out[sm.CustomID] = sm
			}
		}
	}
	return out
}

func TestPanel_IsDefaultDeny(t *testing.T) {
	cmd := permissions.NewPanelCommand(newStore(t, mocks.NewMockRepository(t)), testLoc(t), nil)
	assert.True(t, cmd.DefaultDeny, "/permissions is owner/admin-only by default")
	assert.Equal(t, "permissions", cmd.Def.Name)
	assert.Empty(t, cmd.Def.Options, "the panel command takes no options")
	assert.Nil(t, cmd.Autocomplete)
}

// TestPanel_Opens renders an ephemeral V2 panel with one role-picker per command,
// prefilled with that command's current roles.
func TestPanel_Opens(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// One List per render; ping has a role, permissions has none.
	repo.EXPECT().List(mock.Anything, g1).Return([]permissions.Mapping{{Command: "ping", RoleID: "r1"}}, nil).Maybe()

	resp := &fakeResponder{}
	cmd := permissions.NewPanelCommand(newStore(t, repo), testLoc(t), []string{"ping", "permissions"})
	require.NoError(t, cmd.Handler(context.Background(), resp, cmdInteraction(), g1))

	assert.True(t, resp.respondedV2, "panel is an ephemeral Components V2 reply")
	selects := roleSelects(resp.components)
	require.Contains(t, selects, "permissions:set:0:ping")
	require.Contains(t, selects, "permissions:set:0:permissions")
	ping := selects["permissions:set:0:ping"]
	assert.Equal(t, discordgo.RoleSelectMenu, ping.MenuType)
	require.Len(t, ping.DefaultValues, 1, "ping's current role is prefilled")
	assert.Equal(t, "r1", ping.DefaultValues[0].ID)
	assert.Empty(t, selects["permissions:set:0:permissions"].DefaultValues)
}

// TestPanel_LocalizesDescriptions renders a prose description for a key that has
// one (contracts.custom) and falls back to the bare command path for one that
// doesn't (ping).
func TestPanel_LocalizesDescriptions(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Maybe()

	resp := &fakeResponder{}
	cmd := permissions.NewPanelCommand(newStore(t, repo), testLoc(t), []string{"contracts.custom", "ping"})
	require.NoError(t, cmd.Handler(context.Background(), resp, cmdInteraction(), g1))

	text := textOf(resp.components)
	assert.Contains(t, text, "Create & edit custom contracts", "a key with a description shows localized prose")
	assert.Contains(t, text, "`/ping`", "a key without one falls back to its command path")
}

// TestPanel_SetRoles applies a role-picker change to the named command and
// re-renders the panel in place.
func TestPanel_SetRoles(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().SetRoles(mock.Anything, g1, "ping", []string{"r1", "r2"}, "u1").Return(nil).Once()
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Maybe()

	resp := &fakeResponder{}
	comp := permissions.NewPanelComponent(newStore(t, repo), testLoc(t), []string{"ping"})
	require.NoError(t, comp.Handler(context.Background(), resp,
		compInteraction("permissions:set:0:ping", "r1", "r2"), g1))

	assert.True(t, resp.updatedV2, "the change re-renders the panel in place")
}

// TestPanel_SetUnknownCommandIgnored does not write for a CustomID naming a
// command outside the catalog (don't trust component input), but still re-renders.
func TestPanel_SetUnknownCommandIgnored(t *testing.T) {
	repo := mocks.NewMockRepository(t) // no SetRoles expected
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Maybe()

	resp := &fakeResponder{}
	comp := permissions.NewPanelComponent(newStore(t, repo), testLoc(t), []string{"ping"})
	require.NoError(t, comp.Handler(context.Background(), resp,
		compInteraction("permissions:set:0:bogus", "r1"), g1))

	assert.True(t, resp.updatedV2)
}

// TestPanel_UnauthorizedDenied re-authorizes every panel interaction: a
// non-admin without a granted role is refused and nothing is written, even
// though the (ephemeral) panel was reachable. Mirrors the session's command gate
// so a persisted panel can't outlive the invoker's access.
func TestPanel_UnauthorizedDenied(t *testing.T) {
	repo := mocks.NewMockRepository(t) // no SetRoles expected
	// Authorization consults the gate, which loads the server's mapping; the
	// "permissions" command is unmapped here, so a non-admin is denied.
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Maybe()

	resp := &fakeResponder{}
	comp := permissions.NewPanelComponent(newStore(t, repo), testLoc(t), []string{"ping"})
	member := &discordgo.Member{User: &discordgo.User{ID: "u2"}} // not admin, no roles
	require.NoError(t, comp.Handler(context.Background(), resp,
		compInteractionBy(member, "permissions:set:0:ping", "r1"), g1))

	assert.True(t, resp.updatedV2)
	assert.Contains(t, textOf(resp.components), "permission", "shows the denial notice")
}

// TestPanel_Paging flips to another page on the Next button, showing that page's
// commands.
func TestPanel_Paging(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().List(mock.Anything, g1).Return(nil, nil).Maybe()

	resp := &fakeResponder{}
	// 6 commands → 2 pages (4 + 2). Page index 1 shows c5, c6.
	paths := []string{"c1", "c2", "c3", "c4", "c5", "c6"}
	comp := permissions.NewPanelComponent(newStore(t, repo), testLoc(t), paths)
	require.NoError(t, comp.Handler(context.Background(), resp, compInteraction("permissions:page:1"), g1))

	assert.True(t, resp.updatedV2)
	selects := roleSelects(resp.components)
	assert.Contains(t, selects, "permissions:set:1:c5")
	assert.Contains(t, selects, "permissions:set:1:c6")
	assert.NotContains(t, selects, "permissions:set:1:c1", "page 1 does not show page 0's commands")
}

// TestModule provides the gate, command, and component into the graph. A pool is
// provided for the repository; pgxpool connects lazily, so no live DB is needed.
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
