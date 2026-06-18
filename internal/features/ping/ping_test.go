package ping_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/kweezl/spacecraft-cadet/internal/features/ping"
	"github.com/kweezl/spacecraft-cadet/internal/features/ping/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

func TestNewCommand_Disabled(t *testing.T) {
	cmd := ping.NewCommand(ping.Config{Enabled: false}, mocks.NewMockRepository(t), zap.NewNop())
	assert.Nil(t, cmd)
}

func TestPingHandler_RecordsAndReplies(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Record(mock.Anything, "g1", "u1").Return(nil).Once()
	repo.EXPECT().Count(mock.Anything, "g1").Return(int64(3), nil).Once()

	cmd := ping.NewCommand(ping.Config{Enabled: true}, repo, zap.NewNop())
	require.NotNil(t, cmd)

	resp := &fakeResponder{}
	err := cmd.Handler(context.Background(), resp, guildInteraction("g1", "u1"))
	require.NoError(t, err)
	assert.Equal(t, "pong (#3)", resp.last)
}
