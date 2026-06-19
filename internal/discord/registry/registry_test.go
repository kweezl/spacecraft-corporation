package registry

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
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

func TestRegistry_CountsDispatchByCommand(t *testing.T) {
	counter := newCommandCounter(prometheus.NewRegistry())
	cmd := &Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r Responder, i *discordgo.InteractionCreate) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	reg := New(Params{Commands: []*Command{cmd}, Counter: counter})

	for range 3 {
		require.NoError(t, reg.Dispatch(context.Background(), &fakeResponder{}, interaction("ping")))
	}
	// Unknown commands are rejected before counting (no label cardinality leak).
	_ = reg.Dispatch(context.Background(), &fakeResponder{}, interaction("nope"))

	assert.Equal(t, float64(3), testutil.ToFloat64(counter.WithLabelValues("ping")))
	assert.Equal(t, float64(0), testutil.ToFloat64(counter.WithLabelValues("nope")))
}

func TestRegistry_RecordsDurationByCommand(t *testing.T) {
	duration := newCommandDuration(prometheus.NewRegistry())
	cmd := &Command{
		Def: &discordgo.ApplicationCommand{Name: "ping"},
		Handler: func(_ context.Context, r Responder, i *discordgo.InteractionCreate) error {
			return r.Respond(i.Interaction, "pong")
		},
	}
	reg := New(Params{Commands: []*Command{cmd}, Duration: duration})

	for range 3 {
		require.NoError(t, reg.Dispatch(context.Background(), &fakeResponder{}, interaction("ping")))
	}
	// Unknown commands are rejected before timing, so they record no sample.
	_ = reg.Dispatch(context.Background(), &fakeResponder{}, interaction("nope"))

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
