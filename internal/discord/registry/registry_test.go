package registry

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeResponder struct{ last string }

func (f *fakeResponder) Respond(_ *discordgo.Interaction, content string) error {
	f.last = content
	return nil
}

func interaction(name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{Name: name},
	}}
}

func TestRegistry_DispatchesToHandler(t *testing.T) {
	cmd := &Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r Responder, i *discordgo.InteractionCreate) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	reg := New(Params{Commands: []*Command{cmd, nil}}) // nil = disabled module

	resp := &fakeResponder{}
	err := reg.Dispatch(context.Background(), resp, interaction("ping"))
	require.NoError(t, err)
	assert.Equal(t, "pong", resp.last)
}

func TestRegistry_CommandsSkipsNil(t *testing.T) {
	cmd := &Command{Def: &discordgo.ApplicationCommand{Name: "ping"}}
	reg := New(Params{Commands: []*Command{nil, cmd}})
	defs := reg.Commands()
	require.Len(t, defs, 1)
	assert.Equal(t, "ping", defs[0].Name)
}

func TestRegistry_UnknownCommand(t *testing.T) {
	reg := New(Params{Commands: nil})
	err := reg.Dispatch(context.Background(), &fakeResponder{}, interaction("nope"))
	require.Error(t, err)
}
