package session

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
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
	serverID string
	name     string
}

func (f *fakeDiscord) AddInteractionHandler(fn func(*discordgo.InteractionCreate)) { f.handler = fn }
func (f *fakeDiscord) Open() error                                                 { f.opened = true; return nil }
func (f *fakeDiscord) Close() error                                                { f.closed = true; return nil }
func (f *fakeDiscord) CreateCommand(serverID string, cmd *discordgo.ApplicationCommand) error {
	f.created = append(f.created, created{serverID: serverID, name: cmd.Name})
	return nil
}
func (f *fakeDiscord) Respond(_ *discordgo.Interaction, content string) error {
	f.lastReply = content
	return nil
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

func TestManager_ServerScope_OpensAndRegisters(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) {
		fake = &fakeDiscord{token: tok}
		return fake, nil
	}
	m := newManager(
		Config{Token: "tok-1", Scope: "server", DevServerID: "dev-server"},
		newTestRegistry(), factory, zap.NewNop(),
	)

	require.NoError(t, m.Start(context.Background()))
	require.NotNil(t, fake)
	assert.Equal(t, "tok-1", fake.token)
	assert.True(t, fake.opened)
	require.Len(t, fake.created, 1)
	assert.Equal(t, "dev-server", fake.created[0].serverID)
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

func TestManager_GlobalScope_UsesEmptyServer(t *testing.T) {
	var fake *fakeDiscord
	factory := func(tok string) (Discord, error) { fake = &fakeDiscord{token: tok}; return fake, nil }
	m := newManager(
		Config{Token: "tok-1", Scope: "global"},
		newTestRegistry(), factory, zap.NewNop(),
	)
	require.NoError(t, m.Start(context.Background()))
	require.Len(t, fake.created, 1)
	assert.Equal(t, "", fake.created[0].serverID)
}
