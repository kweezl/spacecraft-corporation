package settings_test

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
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/settings"
	"github.com/kweezl/spacecraft-corporation/internal/settings/mocks"
)

// g1 is a fixed resolved servers.id used across the store/command tests (the
// session would resolve the snowflake to this before the handler runs).
var g1 = uuid.New()

func translator(t *testing.T) *i18n.Translator {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return tr
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
	assert.Equal(t, "en", lang)
}

func TestStore_Resolve_StoredValues(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore", Language: "ru"}, nil).Once()

	theme, lang := newStore(t, repo).Resolve(context.Background(), g1)
	assert.Equal(t, "lore", theme)
	assert.Equal(t, "ru", lang)
}

func TestStore_Resolve_InvalidStoredFallsBack(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).
		Return(settings.Settings{Theme: "ghost", Language: "xx"}, nil).Once()

	theme, lang := newStore(t, repo).Resolve(context.Background(), g1)
	assert.Equal(t, "standard", theme, "an unknown stored theme falls back to default")
	assert.Equal(t, "en", lang)
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

// --- /settings command ---

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
func (f *fakeResponder) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, _ []discordgo.MessageComponent) error {
	return nil
}
func (f *fakeResponder) UpdateComponentsV2(_ *discordgo.Interaction, _ []discordgo.MessageComponent) error {
	return nil
}

func settingsInteraction(sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "g1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "settings",
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

func newCommand(t *testing.T, repo settings.Repository) *registry.Command {
	t.Helper()
	tr := translator(t)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	return settings.NewCommand(newStore(t, repo), tr, loc)
}

func TestCommand_IsDefaultDeny(t *testing.T) {
	cmd := newCommand(t, mocks.NewMockRepository(t))
	assert.True(t, cmd.DefaultDeny, "/settings is owner/admin-only by default")
	assert.Equal(t, "settings", cmd.Def.Name)
}

func TestCommand_SetTheme(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().SetTheme(mock.Anything, g1, "lore").Return(nil).Once()

	resp := &fakeResponder{}
	err := newCommand(t, repo).Handler(context.Background(), resp,
		settingsInteraction("theme", opt("name", "lore")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "lore")
}

func TestCommand_SetLanguage(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().SetLanguage(mock.Anything, g1, "ru").Return(nil).Once()

	resp := &fakeResponder{}
	err := newCommand(t, repo).Handler(context.Background(), resp,
		settingsInteraction("language", opt("code", "ru")), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "ru")
}

func TestCommand_Show(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, g1).Return(settings.Settings{Theme: "lore", Language: "ru"}, nil).Once()

	resp := &fakeResponder{}
	err := newCommand(t, repo).Handler(context.Background(), resp, settingsInteraction("show"), g1)
	require.NoError(t, err)
	assert.Contains(t, resp.last, "lore")
	assert.Contains(t, resp.last, "ru")
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
