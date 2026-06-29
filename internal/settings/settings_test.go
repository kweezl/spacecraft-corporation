package settings_test

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
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
	"github.com/kweezl/spacecraft-corporation/internal/settings/mocks"
)

// g1 is a fixed resolved servers.id used across the store/panel tests (the
// session would resolve the snowflake to this before the handler runs).
var g1 = uuid.New()

func translator(t *testing.T) *i18n.Translator {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return tr
}

func testLoc(t *testing.T, tr *i18n.Translator) *i18n.Localizer {
	t.Helper()
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

func newStore(t *testing.T, repo settings.Repository) *settings.Store {
	t.Helper()
	s, err := settings.NewStore(repo, translator(t), zap.NewNop())
	require.NoError(t, err)
	return s
}

// --- Store / Resolver ---

func TestStore_Resolve_Defaults(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{}, nil).Once()

	theme, lang := newStore(t, repo).Resolve(context.Background(), g1)
	assert.Equal(t, "standard", theme)
	assert.Equal(t, i18n.LanguageEN, lang)
}

func TestStore_Resolve_StoredValues(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore", Language: "ru"}, nil).Once()

	theme, lang := newStore(t, repo).Resolve(context.Background(), g1)
	assert.Equal(t, "lore", theme)
	assert.Equal(t, i18n.LanguageRU, lang)
}

func TestStore_Resolve_InvalidStoredFallsBack(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).
		Return(settings.Settings{Theme: "ghost", Language: "xx"}, nil).Once()

	theme, lang := newStore(t, repo).Resolve(context.Background(), g1)
	assert.Equal(t, "standard", theme, "an unknown stored theme falls back to default")
	assert.Equal(t, i18n.LanguageEN, lang)
}

func TestStore_Resolve_Caches(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore"}, nil).Once()

	store := newStore(t, repo)
	for range 3 {
		theme, _ := store.Resolve(context.Background(), g1)
		assert.Equal(t, "lore", theme)
	}
}

func TestStore_InvalidatesOnWrite(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{}, nil).Once() // before
	repo.EXPECT().SetTheme(mock.Anything, g1, "lore").Return(nil).Once()
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore"}, nil).Once() // after

	store := newStore(t, repo)
	theme, _ := store.Resolve(context.Background(), g1)
	assert.Equal(t, "standard", theme)

	require.NoError(t, store.SetTheme(context.Background(), g1, "lore"))

	theme, _ = store.Resolve(context.Background(), g1)
	assert.Equal(t, "lore", theme, "the set invalidated the cache; resolve reloaded")
}

// --- /settings panel ---

// fakeResponder records the last response so panel tests can inspect it.
type fakeResponder struct {
	components  []discordgo.MessageComponent
	respondedV2 bool
	updatedV2   bool
}

func (f *fakeResponder) Respond(_ *discordgo.Interaction, _ string) error          { return nil }
func (f *fakeResponder) RespondEphemeral(_ *discordgo.Interaction, _ string) error { return nil }
func (f *fakeResponder) RespondEmbed(_ *discordgo.Interaction, _ *discordgo.MessageEmbed) error {
	return nil
}
func (f *fakeResponder) RespondAutocomplete(_ *discordgo.Interaction, _ []*discordgo.ApplicationCommandOptionChoice) error {
	return nil
}
func (f *fakeResponder) RespondEmbedComponents(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	return nil
}
func (f *fakeResponder) RespondEmbedComponentsEphemeral(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	return nil
}
func (f *fakeResponder) UpdateMessage(_ *discordgo.Interaction, _ *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
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

// denyAccess is a CommandAccess that refuses everyone (for the unauthorized test).
type denyAccess struct{}

func (denyAccess) IsAllowed(context.Context, session.AccessRequest) (bool, error) { return false, nil }

func adminMember() *discordgo.Member {
	return &discordgo.Member{Permissions: discordgo.PermissionAdministrator, User: &discordgo.User{ID: "u1"}}
}

func plainMember() *discordgo.Member {
	return &discordgo.Member{User: &discordgo.User{ID: "u1"}}
}

func commandInteraction(member *discordgo.Member) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "g1",
		Member:  member,
		Data:    discordgo.ApplicationCommandInteractionData{Name: "settings"},
	}}
}

func componentInteraction(customID, value string, member *discordgo.Member) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionMessageComponent,
		GuildID: "g1",
		Member:  member,
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID, Values: []string{value}},
	}}
}

// textOf concatenates the TextDisplay contents of a Components V2 view.
func textOf(comps []discordgo.MessageComponent) string {
	var b strings.Builder
	for _, c := range comps {
		if td, ok := c.(discordgo.TextDisplay); ok {
			b.WriteString(td.Content)
		}
	}
	return b.String()
}

// selects indexes the view's string-selects by CustomID.
func selects(comps []discordgo.MessageComponent) map[string]discordgo.SelectMenu {
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

func defaultValue(sm discordgo.SelectMenu) string {
	for _, o := range sm.Options {
		if o.Default {
			return o.Value
		}
	}
	return ""
}

func TestPanel_IsDefaultDeny(t *testing.T) {
	tr := translator(t)
	cmd := settings.NewPanelCommand(newStore(t, mocks.NewMockRepository(t)), tr, testLoc(t, tr))
	assert.True(t, cmd.DefaultDeny, "/settings is owner/admin-only by default")
	assert.Equal(t, "settings", cmd.Def.Name)
	assert.Empty(t, cmd.Def.Options, "the V2 panel replaces the old subcommands")
}

// TestPanel_Opens renders an ephemeral V2 panel with a theme select and a
// language select, each prefilled with the server's current value.
func TestPanel_Opens(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore", Language: "ru"}, nil).Once()

	tr := translator(t)
	cmd := settings.NewPanelCommand(newStore(t, repo), tr, testLoc(t, tr))
	resp := &fakeResponder{}
	require.NoError(t, cmd.Handler(context.Background(), resp, commandInteraction(adminMember()), g1))

	assert.True(t, resp.respondedV2, "panel is an ephemeral Components V2 reply")
	header := textOf(resp.components)
	assert.Contains(t, header, "lore")
	assert.Contains(t, header, "ru")

	sel := selects(resp.components)
	require.Contains(t, sel, "settings:theme")
	require.Contains(t, sel, "settings:language")
	assert.Equal(t, "lore", defaultValue(sel["settings:theme"]), "current theme is preselected")
	assert.Equal(t, "ru", defaultValue(sel["settings:language"]), "current language is preselected")
}

// TestPanel_SetTheme applies a theme-select change and re-renders in the new
// theme. The pre-write Resolve reads the old value (default), so the new
// selection differs and a write fires.
func TestPanel_SetTheme(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{}, nil).Once() // current: default
	repo.EXPECT().SetTheme(mock.Anything, g1, "lore").Return(nil).Once()

	tr := translator(t)
	comp := settings.NewPanelComponent(newStore(t, repo), tr, testLoc(t, tr), nil)
	resp := &fakeResponder{}
	require.NoError(t, comp.Handler(context.Background(), resp,
		componentInteraction("settings:theme", "lore", adminMember()), g1))

	assert.True(t, resp.updatedV2, "the panel edits itself in place")
	assert.Contains(t, textOf(resp.components), "lore")
}

// TestPanel_SetLanguage applies a language-select change.
func TestPanel_SetLanguage(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{}, nil).Once() // current: default
	repo.EXPECT().SetLanguage(mock.Anything, g1, i18n.LanguageRU).Return(nil).Once()

	tr := translator(t)
	comp := settings.NewPanelComponent(newStore(t, repo), tr, testLoc(t, tr), nil)
	resp := &fakeResponder{}
	require.NoError(t, comp.Handler(context.Background(), resp,
		componentInteraction("settings:language", "ru", adminMember()), g1))

	assert.True(t, resp.updatedV2)
	assert.Contains(t, textOf(resp.components), "ru")
}

// TestPanel_ReselectCurrentNoWrite re-picks the already-current value: the panel
// re-renders but performs no DB write (no SetTheme expectation on the mock).
func TestPanel_ReselectCurrentNoWrite(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore"}, nil).Once() // current: lore

	tr := translator(t)
	comp := settings.NewPanelComponent(newStore(t, repo), tr, testLoc(t, tr), nil)
	resp := &fakeResponder{}
	require.NoError(t, comp.Handler(context.Background(), resp,
		componentInteraction("settings:theme", "lore", adminMember()), g1))

	assert.True(t, resp.updatedV2)
	assert.Contains(t, textOf(resp.components), "lore", "unchanged: still the current theme")
}

// TestPanel_UnknownValueIgnored does not write for a value that is not a real
// theme (a crafted/stale selection); it still re-renders the panel.
func TestPanel_UnknownValueIgnored(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// No SetTheme expected. Only the re-render's Resolve reads.
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{}, nil).Once()

	tr := translator(t)
	comp := settings.NewPanelComponent(newStore(t, repo), tr, testLoc(t, tr), nil)
	resp := &fakeResponder{}
	require.NoError(t, comp.Handler(context.Background(), resp,
		componentInteraction("settings:theme", "ghost", adminMember()), g1))

	assert.True(t, resp.updatedV2)
	assert.Contains(t, textOf(resp.components), "standard", "unchanged: still the default theme")
}

// TestPanel_UnauthorizedDenied re-authorizes every panel interaction: a
// non-admin without the granted role gets a denial and no write happens.
func TestPanel_UnauthorizedDenied(t *testing.T) {
	repo := mocks.NewMockRepository(t) // no calls expected

	tr := translator(t)
	comp := settings.NewPanelComponent(newStore(t, repo), tr, testLoc(t, tr), denyAccess{})
	resp := &fakeResponder{}
	require.NoError(t, comp.Handler(context.Background(), resp,
		componentInteraction("settings:theme", "lore", plainMember()), g1))

	assert.True(t, resp.updatedV2)
	assert.Contains(t, textOf(resp.components), "settings", "the denial names the command")
}

func TestModule(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://user:pass@localhost:5432/db")
	require.NoError(t, err)
	defer pool.Close()

	var reg *registry.Registry
	var resolver i18n.Resolver
	app := fxtest.New(t,
		fx.Provide(func() *pgxpool.Pool { return pool }),
		fx.Provide(prometheus.NewRegistry),
		fx.Provide(zap.NewNop),
		i18n.Module(),
		settings.Module(),
		registry.Module(),
		fx.Populate(&reg, &resolver),
	)
	app.RequireStart()
	defer app.RequireStop()

	require.NotNil(t, resolver, "settings provides the i18n resolver")
	cmds := reg.Commands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "settings", cmds[0].Name)
}
