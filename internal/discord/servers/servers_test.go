package servers

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
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

func newTestManager(t *testing.T, cfg Config, repo Repository) *Manager {
	t.Helper()
	m, err := newManager(cfg, repo, zap.NewNop())
	require.NoError(t, err)
	return m
}

func TestManager_OnGuildCreate_NewApprovedServer_LogsJoin(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Unseen server (not found) -> insert, prime cache, log the join.
	repo.EXPECT().Get(mock.Anything, "g1").Return(uuid.Nil, false, false, nil)
	repo.EXPECT().Upsert(mock.Anything, "g1", "Approved Server", true).Return(uuid.New(), true, true, nil)
	repo.EXPECT().LogEvent(mock.Anything, "g1", EventJoined).Return(nil)

	m := newTestManager(t, Config{ApprovedServerID: []string{"g1"}}, repo)
	m.OnGuildCreate(guildCreate("g1", "Approved Server"))
}

func TestManager_OnGuildCreate_NewUnlistedServer_NotApproved(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Not in the allowlist -> inList false; still a new row so it logs the join.
	repo.EXPECT().Get(mock.Anything, "g2").Return(uuid.Nil, false, false, nil)
	repo.EXPECT().Upsert(mock.Anything, "g2", "Other", false).Return(uuid.New(), false, true, nil)
	repo.EXPECT().LogEvent(mock.Anything, "g2", EventJoined).Return(nil)

	m := newTestManager(t, Config{ApprovedServerID: []string{"g1"}}, repo)
	m.OnGuildCreate(guildCreate("g2", "Other"))
}

func TestManager_OnGuildCreate_KnownApprovedServer_NoWrite(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Already known and correctly approved: GuildCreate (a reconnect) does no DB
	// write at all — no Upsert, no LogEvent expected.
	repo.EXPECT().Get(mock.Anything, "g1").Return(uuid.New(), true, true, nil)

	m := newTestManager(t, Config{ApprovedServerID: []string{"g1"}}, repo)
	m.OnGuildCreate(guildCreate("g1", "Approved Server"))
}

func TestManager_OnGuildCreate_KnownListedButUnapproved_Promotes(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Known row, not approved yet, but in the allowlist -> promote (Upsert), no
	// join event (it isn't a new row).
	repo.EXPECT().Get(mock.Anything, "g1").Return(uuid.New(), false, true, nil)
	repo.EXPECT().Upsert(mock.Anything, "g1", "Approved Server", true).Return(uuid.New(), true, false, nil)

	m := newTestManager(t, Config{ApprovedServerID: []string{"g1"}}, repo)
	m.OnGuildCreate(guildCreate("g1", "Approved Server"))
}

func TestManager_OnGuildCreate_KnownUnlisted_NoPromote(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Known, unapproved, and NOT in the allowlist -> nothing to do (no write).
	repo.EXPECT().Get(mock.Anything, "g2").Return(uuid.New(), false, true, nil)

	m := newTestManager(t, Config{ApprovedServerID: []string{"g1"}}, repo)
	m.OnGuildCreate(guildCreate("g2", "Other"))
}

func TestManager_OnGuildCreate_GetError_NoWrite(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, "g1").Return(uuid.Nil, false, false, errors.New("boom"))

	m := newTestManager(t, Config{}, repo)
	m.OnGuildCreate(guildCreate("g1", "X"))
}

func TestManager_OnGuildCreate_UpsertError_NoEvent(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, "g1").Return(uuid.Nil, false, false, nil)
	repo.EXPECT().Upsert(mock.Anything, "g1", "X", false).Return(uuid.Nil, false, false, errors.New("boom"))

	m := newTestManager(t, Config{}, repo)
	m.OnGuildCreate(guildCreate("g1", "X"))
}

func TestManager_OnGuildDelete_RealRemoval_Logs(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().LogEvent(mock.Anything, "g1", EventRemoved).Return(nil)

	m := newTestManager(t, Config{}, repo)
	m.OnGuildDelete(guildDelete("g1", false))
}

func TestManager_OnGuildDelete_Outage_Ignored(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	// Unavailable == true is a gateway outage, not a removal: no event expected.
	m := newTestManager(t, Config{}, repo)
	m.OnGuildDelete(guildDelete("g1", true))
}

func TestManager_Resolve_ReturnsIDAndApproval_Caches(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	id := uuid.New()
	// .Once(): the second Resolve is served from the LRU cache, not the repo.
	repo.EXPECT().Get(mock.Anything, "g1").Return(id, true, true, nil).Once()

	m := newTestManager(t, Config{}, repo)
	for range 2 {
		gotID, approved, err := m.Resolve(context.Background(), "g1")
		require.NoError(t, err)
		assert.Equal(t, id, gotID)
		assert.True(t, approved)
	}
}

func TestManager_Resolve_UnknownServer_NotApproved(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().Get(mock.Anything, "nope").Return(uuid.Nil, false, false, nil)

	m := newTestManager(t, Config{}, repo)
	id, approved, err := m.Resolve(context.Background(), "nope")
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, id)
	assert.False(t, approved)
}

func TestNewManager_TrimsAndDropsEmptyAllowlistEntries(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	m := newTestManager(t, Config{ApprovedServerID: []string{" g1 ", "", "g2"}}, repo)
	assert.Equal(t, map[string]bool{"g1": true, "g2": true}, m.approved)
}
