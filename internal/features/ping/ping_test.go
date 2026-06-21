package ping_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/features/ping"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func testLocalizer(t *testing.T) *i18n.Localizer {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

type fakeResponder struct {
	last      string
	lastEmbed *discordgo.MessageEmbed
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
	f.lastEmbed = embed
	return nil
}
func (f *fakeResponder) RespondAutocomplete(_ *discordgo.Interaction, _ []*discordgo.ApplicationCommandOptionChoice) error {
	return nil
}
func (f *fakeResponder) RespondEmbedComponents(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	f.lastEmbed = embed
	return nil
}
func (f *fakeResponder) UpdateMessage(_ *discordgo.Interaction, embed *discordgo.MessageEmbed, _ []discordgo.MessageComponent) error {
	f.lastEmbed = embed
	return nil
}
func (f *fakeResponder) RespondComponentsV2Ephemeral(_ *discordgo.Interaction, _ []discordgo.MessageComponent) error {
	return nil
}
func (f *fakeResponder) UpdateComponentsV2(_ *discordgo.Interaction, _ []discordgo.MessageComponent) error {
	return nil
}
func (f *fakeResponder) RespondModal(_ *discordgo.Interaction, _, _ string, _ []discordgo.MessageComponent) error {
	return nil
}

// discordEpoch is the start of Discord's snowflake epoch (2015-01-01), used to
// synthesize an interaction ID whose timestamp the handler decodes back.
const discordEpoch = 1420070400000

func snowflakeID(at time.Time) string {
	return strconv.FormatInt((at.UnixMilli()-discordEpoch)<<22, 10)
}

func guildInteraction(guildID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: guildID,
		Member:  &discordgo.Member{User: &discordgo.User{ID: userID}},
		Data:    discordgo.ApplicationCommandInteractionData{Name: "ping"},
	}}
}

func TestPingHandler_RepliesWithLatencyEmbed(t *testing.T) {
	srv := uuid.New() // the resolved servers.id the session would pass in
	cmd := ping.NewCommand(testLocalizer(t))
	require.NotNil(t, cmd)

	i := guildInteraction("g1", "u1")
	// An interaction created ~40ms ago, so the round-trip latency is positive.
	i.ID = snowflakeID(time.Now().Add(-40 * time.Millisecond))

	resp := &fakeResponder{}
	err := cmd.Handler(context.Background(), resp, i, srv)
	require.NoError(t, err)

	require.NotNil(t, resp.lastEmbed)
	assert.Equal(t, "Pong", resp.lastEmbed.Title)
	require.Len(t, resp.lastEmbed.Fields, 2)
	assert.Equal(t, "Handle latency", resp.lastEmbed.Fields[0].Name)
	assert.Equal(t, "Round-trip latency", resp.lastEmbed.Fields[1].Name)
	// Values render adaptively: µs under 1ms, ms above. Called directly (no
	// dispatcher), the handle elapsed is 0 → "0 µs"; the round-trip is non-zero
	// given the ~40ms backdated interaction ID → "<n> ms".
	assert.Equal(t, "0 µs", resp.lastEmbed.Fields[0].Value)
	assert.Regexp(t, `^\d+ ms$`, resp.lastEmbed.Fields[1].Value)
}

// TestModule_RegistersPing verifies the module wires its command into the
// registry. /ping is stateless, so no database or pool is needed.
func TestModule_RegistersPing(t *testing.T) {
	var reg *registry.Registry
	app := fxtest.New(t,
		fx.Provide(prometheus.NewRegistry),
		fx.Supply(testLocalizer(t)),
		ping.Module(),
		registry.Module(),
		fx.Populate(&reg),
	)
	app.RequireStart()
	defer app.RequireStop()

	cmds := reg.Commands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "ping", cmds[0].Name)
}
