package servers

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/servers/mocks"
)

func guildCreate(id, name string) *discordgo.GuildCreate {
	return &discordgo.GuildCreate{Guild: &discordgo.Guild{ID: id, Name: name}}
}

func guildDelete(id string, unavailable bool) *discordgo.GuildDelete {
	return &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: id, Unavailable: unavailable}}
}

func TestManager_OnGuildCreate_NewApprovedServer_LogsJoin(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Upsert(mock.Anything, "g1", "Approved Server", true).Return(true, nil)
	repo.EXPECT().LogEvent(mock.Anything, "g1", EventJoined).Return(nil)

	m := newManager(Config{ApprovedServerID: []string{"g1"}}, repo, zap.NewNop())
	m.OnGuildCreate(guildCreate("g1", "Approved Server"))
}

func TestManager_OnGuildCreate_NewUnlistedServer_NotApproved(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Not in the allowlist -> inList false; still a new row so it logs the join.
	repo.EXPECT().Upsert(mock.Anything, "g2", "Other", false).Return(true, nil)
	repo.EXPECT().LogEvent(mock.Anything, "g2", EventJoined).Return(nil)

	m := newManager(Config{ApprovedServerID: []string{"g1"}}, repo, zap.NewNop())
	m.OnGuildCreate(guildCreate("g2", "Other"))
}

func TestManager_OnGuildCreate_KnownServer_NoJoinEvent(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Existing row (isNew=false) -> no join event logged (GuildCreate fires on
	// every reconnect for servers we already know).
	repo.EXPECT().Upsert(mock.Anything, "g1", "Approved Server", true).Return(false, nil)

	m := newManager(Config{ApprovedServerID: []string{"g1"}}, repo, zap.NewNop())
	m.OnGuildCreate(guildCreate("g1", "Approved Server"))
}

func TestManager_OnGuildCreate_UpsertError_NoEvent(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Upsert(mock.Anything, "g1", "X", false).Return(false, errors.New("boom"))

	m := newManager(Config{}, repo, zap.NewNop())
	m.OnGuildCreate(guildCreate("g1", "X"))
}

func TestManager_OnGuildDelete_RealRemoval_Logs(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().LogEvent(mock.Anything, "g1", EventRemoved).Return(nil)

	m := newManager(Config{}, repo, zap.NewNop())
	m.OnGuildDelete(guildDelete("g1", false))
}

func TestManager_OnGuildDelete_Outage_Ignored(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Unavailable == true is a gateway outage, not a removal: no event expected.
	m := newManager(Config{}, repo, zap.NewNop())
	m.OnGuildDelete(guildDelete("g1", true))
}

func TestManager_IsApproved_DelegatesToRepo(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().IsApproved(mock.Anything, "g1").Return(true, nil)

	m := newManager(Config{}, repo, zap.NewNop())
	ok, err := m.IsApproved(context.Background(), "g1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestNewManager_TrimsAndDropsEmptyAllowlistEntries(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	m := newManager(Config{ApprovedServerID: []string{" g1 ", "", "g2"}}, repo, zap.NewNop())
	assert.Equal(t, map[string]bool{"g1": true, "g2": true}, m.approved)
}
