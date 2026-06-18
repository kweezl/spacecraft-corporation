package ping_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kweezl/spacecraft-cadet/internal/discord/registry"
	"github.com/kweezl/spacecraft-cadet/internal/features/ping"
	"github.com/kweezl/spacecraft-cadet/internal/features/ping/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type fakeResponder struct{ last string }

func (f *fakeResponder) Respond(_ *discordgo.Interaction, content string) error {
	f.last = content
	return nil
}

func guildInteraction(guildID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: guildID,
		Member:  &discordgo.Member{User: &discordgo.User{ID: userID}},
		Data:    discordgo.ApplicationCommandInteractionData{Name: "ping"},
	}}
}

func TestPingHandler_RecordsAndReplies(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Record(mock.Anything, "g1", "u1").Return(nil).Once()
	repo.EXPECT().Count(mock.Anything, "g1").Return(int64(3), nil).Once()

	cmd := ping.NewCommand(repo)
	require.NotNil(t, cmd)

	resp := &fakeResponder{}
	err := cmd.Handler(context.Background(), resp, guildInteraction("g1", "u1"))
	require.NoError(t, err)
	assert.Equal(t, "pong (#3)", resp.last)
}

// TestModule_Disabled_ContributesNothing verifies the feature gate: with
// FEATURE_PING_ENABLED=false the module is a no-op and registers no command.
func TestModule_Disabled_ContributesNothing(t *testing.T) {
	t.Setenv("FEATURE_PING_ENABLED", "false")

	var reg *registry.Registry
	app := fxtest.New(t,
		ping.Module(),
		registry.Module(),
		fx.Populate(&reg),
	)
	app.RequireStart()
	defer app.RequireStop()

	assert.Empty(t, reg.Commands())
}

// TestModule_Enabled_RegistersPing verifies the enabled path wires the command
// into the registry. A pool is provided for the repository dependency; pgxpool
// connects lazily, so no live database is needed for graph construction.
func TestModule_Enabled_RegistersPing(t *testing.T) {
	t.Setenv("FEATURE_PING_ENABLED", "true")

	pool, err := pgxpool.New(context.Background(), "postgres://user:pass@localhost:5432/db")
	require.NoError(t, err)
	defer pool.Close()

	var reg *registry.Registry
	app := fxtest.New(t,
		fx.Provide(func() *pgxpool.Pool { return pool }),
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
