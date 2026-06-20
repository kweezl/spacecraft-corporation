package registry

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeResponder struct {
	last       string
	choices    []*discordgo.ApplicationCommandOptionChoice
	components []discordgo.MessageComponent
	updated    bool
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

func (f *fakeResponder) RespondAutocomplete(_ *discordgo.Interaction, choices []*discordgo.ApplicationCommandOptionChoice) error {
	f.choices = choices
	return nil
}

func (f *fakeResponder) RespondEmbedComponents(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	if embed != nil {
		f.last = embed.Title
	}
	f.components = components
	return nil
}

func (f *fakeResponder) UpdateMessage(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	if embed != nil {
		f.last = embed.Title
	}
	f.components = components
	f.updated = true
	return nil
}
func (f *fakeResponder) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, components []discordgo.MessageComponent) error {
	f.components = components
	return nil
}
func (f *fakeResponder) UpdateComponentsV2(_ *discordgo.Interaction, components []discordgo.MessageComponent) error {
	f.components = components
	f.updated = true
	return nil
}

func interaction(name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{Name: name},
	}}
}

// subInteraction builds a command interaction nested as group → subcommand
// (e.g. "base", "own", "register").
func subInteraction(name, group, sub string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: name,
			Options: []*discordgo.ApplicationCommandInteractionDataOption{{
				Name: group,
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
				Options: []*discordgo.ApplicationCommandInteractionDataOption{{
					Name: sub,
					Type: discordgo.ApplicationCommandOptionSubCommand,
				}},
			}},
		},
	}}
}

// gatedDef mirrors the bases shape: a SubcommandGated command with one
// group ("own") holding "register", plus a top-level "list" subcommand.
func gatedDef() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name: "base",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name: "own",
				Type: discordgo.ApplicationCommandOptionSubCommandGroup,
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "register", Type: discordgo.ApplicationCommandOptionSubCommand},
				},
			},
			{Name: "list", Type: discordgo.ApplicationCommandOptionSubCommand},
		},
	}
}

func TestRegistry_AccessKey(t *testing.T) {
	reg := New(Params{Commands: []*Command{
		{Def: &discordgo.ApplicationCommand{Name: "ping"}},
		{Def: gatedDef(), SubcommandGated: true},
	}})

	// A non-gated command keys on its bare name even with options present.
	assert.Equal(t, "ping", reg.AccessKey(interaction("ping")))
	// A gated command keys on the full subcommand path.
	assert.Equal(t, "base own register", reg.AccessKey(subInteraction("base", "own", "register")))
}

func TestRegistry_CommandPaths(t *testing.T) {
	reg := New(Params{Commands: []*Command{
		{Def: &discordgo.ApplicationCommand{Name: "ping"}},
		{Def: gatedDef(), SubcommandGated: true},
	}})

	assert.Equal(t, []string{"base list", "base own register", "ping"}, reg.CommandPaths())
}

func TestRegistry_DispatchAutocomplete(t *testing.T) {
	want := []*discordgo.ApplicationCommandOptionChoice{{Name: "Alpha", Value: "a"}}
	reg := New(Params{Commands: []*Command{{
		Def: &discordgo.ApplicationCommand{Name: "base"},
		Autocomplete: func(context.Context, *discordgo.InteractionCreate, uuid.UUID) ([]*discordgo.ApplicationCommandOptionChoice, error) {
			return want, nil
		},
	}}})

	resp := &fakeResponder{}
	require.NoError(t, reg.DispatchAutocomplete(context.Background(), resp, interaction("base"), uuid.Nil))
	assert.Equal(t, want, resp.choices)
}

func TestRegistry_DispatchAutocomplete_NoHandlerAnswersEmpty(t *testing.T) {
	reg := New(Params{Commands: []*Command{{Def: &discordgo.ApplicationCommand{Name: "base"}}}})
	resp := &fakeResponder{choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "stale"}}}
	require.NoError(t, reg.DispatchAutocomplete(context.Background(), resp, interaction("base"), uuid.Nil))
	assert.Nil(t, resp.choices, "a command without an autocomplete handler answers with no choices")
}

func componentInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		Data: discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func TestRegistry_DispatchComponent(t *testing.T) {
	var gotID string
	reg := New(Params{Components: []*Component{{
		Prefix: "base",
		Handler: func(_ context.Context, _ Responder, i *discordgo.InteractionCreate, _ uuid.UUID) error {
			gotID = i.MessageComponentData().CustomID
			return nil
		},
	}}})

	require.NoError(t, reg.DispatchComponent(context.Background(), &fakeResponder{}, componentInteraction("base:list:tok:2"), uuid.Nil))
	assert.Equal(t, "base:list:tok:2", gotID, "routed to the handler for the 'base' namespace")
}

func TestRegistry_DispatchComponent_UnknownPrefix(t *testing.T) {
	reg := New(Params{Components: nil})
	err := reg.DispatchComponent(context.Background(), &fakeResponder{}, componentInteraction("ghost:1"), uuid.Nil)
	require.Error(t, err)
}

func TestRegistry_DispatchesToHandler(t *testing.T) {
	cmd := &Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r Responder, i *discordgo.InteractionCreate, _ uuid.UUID) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	reg := New(Params{Commands: []*Command{cmd, nil}}) // nil = disabled module

	resp := &fakeResponder{}
	err := reg.Dispatch(context.Background(), resp, interaction("ping"), uuid.Nil)
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
	err := reg.Dispatch(context.Background(), &fakeResponder{}, interaction("nope"), uuid.Nil)
	require.Error(t, err)
}

func TestRegistry_Policy(t *testing.T) {
	reg := New(Params{Commands: []*Command{
		{Def: &discordgo.ApplicationCommand{Name: "open"}},
		{Def: &discordgo.ApplicationCommand{Name: "locked"}, DefaultDeny: true},
	}})

	deny, known := reg.Policy("open")
	assert.True(t, known)
	assert.False(t, deny, "default-deny defaults to false (open)")

	deny, known = reg.Policy("locked")
	assert.True(t, known)
	assert.True(t, deny)

	_, known = reg.Policy("missing")
	assert.False(t, known, "unknown command is reported as not known")
}

func TestRegistry_DefaultMemberPermissions(t *testing.T) {
	open := &Command{Def: &discordgo.ApplicationCommand{Name: "open"}}
	locked := &Command{Def: &discordgo.ApplicationCommand{Name: "locked"}, DefaultDeny: true}
	custom := &Command{Def: &discordgo.ApplicationCommand{
		Name:                     "custom",
		DefaultMemberPermissions: ptr(int64(discordgo.PermissionManageGuild)),
	}, DefaultDeny: true}
	New(Params{Commands: []*Command{open, locked, custom}})

	assert.Nil(t, open.Def.DefaultMemberPermissions, "open command stays visible to everyone")
	require.NotNil(t, locked.Def.DefaultMemberPermissions, "DefaultDeny command is hidden from non-admins")
	assert.Equal(t, int64(0), *locked.Def.DefaultMemberPermissions, "admin-only is the empty permission bitfield")
	require.NotNil(t, custom.Def.DefaultMemberPermissions)
	assert.Equal(t, int64(discordgo.PermissionManageGuild), *custom.Def.DefaultMemberPermissions,
		"a command's explicit default_member_permissions is preserved")
}

func ptr[T any](v T) *T { return &v }

func TestRegistry_CountsDispatchByCommand(t *testing.T) {
	counter := newCommandCounter(prometheus.NewRegistry())
	cmd := &Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r Responder, i *discordgo.InteractionCreate, _ uuid.UUID) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	reg := New(Params{Commands: []*Command{cmd}, Counter: counter})

	for range 3 {
		require.NoError(t, reg.Dispatch(context.Background(), &fakeResponder{}, interaction("ping"), uuid.Nil))
	}
	// Unknown commands are rejected before counting (no label cardinality leak).
	_ = reg.Dispatch(context.Background(), &fakeResponder{}, interaction("nope"), uuid.Nil)

	assert.Equal(t, float64(3), testutil.ToFloat64(counter.WithLabelValues("ping")))
	assert.Equal(t, float64(0), testutil.ToFloat64(counter.WithLabelValues("nope")))
}

func TestRegistry_RecordsDurationByCommand(t *testing.T) {
	duration := newCommandDuration(prometheus.NewRegistry())
	cmd := &Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r Responder, i *discordgo.InteractionCreate, _ uuid.UUID) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	reg := New(Params{Commands: []*Command{cmd}, Duration: duration})

	for range 3 {
		require.NoError(t, reg.Dispatch(context.Background(), &fakeResponder{}, interaction("ping"), uuid.Nil))
	}
	// Unknown commands are rejected before timing, so they record no sample.
	_ = reg.Dispatch(context.Background(), &fakeResponder{}, interaction("nope"), uuid.Nil)

	assert.Equal(t, uint64(3), histSampleCount(t, duration, "ping"))
	assert.Equal(t, uint64(0), histSampleCount(t, duration, "nope"))
}

// histSampleCount returns how many observations the histogram recorded for the
// given command label.
func histSampleCount(t *testing.T, h *prometheus.HistogramVec, command string) uint64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, h.WithLabelValues(command).(prometheus.Histogram).Write(&m))
	return m.GetHistogram().GetSampleCount()
}
