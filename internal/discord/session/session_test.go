package session

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"github.com/kweezl/spacecraft-cadet/internal/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeDiscord struct {
	token          string
	opened, closed bool
	created        []created
	handler        func(*discordgo.InteractionCreate)
	lastReply      string
}

type created struct {
	guildID string
	name    string
}

func (f *fakeDiscord) AddInteractionHandler(fn func(*discordgo.InteractionCreate)) { f.handler = fn }
func (f *fakeDiscord) Open() error                                                 { f.opened = true; return nil }
func (f *fakeDiscord) Close() error                                                { f.closed = true; return nil }
func (f *fakeDiscord) CreateCommand(guildID string, cmd *discordgo.ApplicationCommand) error {
	f.created = append(f.created, created{guildID: guildID, name: cmd.Name})
	return nil
}
func (f *fakeDiscord) Respond(_ *discordgo.Interaction, content string) error {
	f.lastReply = content
	return nil
}

type fakeTokens struct{ toks []token.Token }

func (f fakeTokens) ListEnabled(context.Context) ([]token.Token, error) { return f.toks, nil }

func newTestRegistry() *registry.Registry {
	cmd := &registry.Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r registry.Responder, i *discordgo.InteractionCreate) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	return registry.New(registry.Params{Commands: []*registry.Command{cmd}})
}

func TestManager_GuildScope_OpensAndRegisters(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) {
		fake = &fakeDiscord{token: tok}
		return fake, nil
	}
	m := newManager(
		Config{Scope: "guild", DevGuildID: "dev-guild"},
		fakeTokens{toks: []token.Token{{GuildID: "g1", Token: "tok-1"}}},
		newTestRegistry(), factory, zap.NewNop(),
	)

	require.NoError(t, m.Start(context.Background()))
	require.NotNil(t, fake)
	assert.Equal(t, "tok-1", fake.token)
	assert.True(t, fake.opened)
	require.Len(t, fake.created, 1)
	assert.Equal(t, "dev-guild", fake.created[0].guildID)
	assert.Equal(t, "ping", fake.created[0].name)

	// Interaction handler routes through the registry.
	fake.handler(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{Name: "ping"},
	}})
	assert.Equal(t, "pong", fake.lastReply)

	require.NoError(t, m.Stop(context.Background()))
	assert.True(t, fake.closed)
}

func TestManager_GlobalScope_UsesEmptyGuild(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(
		Config{Scope: "global"},
		fakeTokens{toks: []token.Token{{GuildID: "g1", Token: "tok-1"}}},
		newTestRegistry(), factory, zap.NewNop(),
	)
	require.NoError(t, m.Start(context.Background()))
	require.Len(t, fake.created, 1)
	assert.Equal(t, "", fake.created[0].guildID)
}
