package emoji

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeAPI struct {
	connected atomic.Bool // read from the sync goroutine, set from the test
	existing  []*discordgo.Emoji
	listErr   error
	created   []string         // names passed to ApplicationEmojiCreate, in order
	createErr map[string]error // per-name create failure
	deleted   []string         // ids passed to ApplicationEmojiDelete, in order
	deleteErr map[string]error // per-id delete failure
}

func (f *fakeAPI) Connected() bool { return f.connected.Load() }

func (f *fakeAPI) ApplicationEmojis() ([]*discordgo.Emoji, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.existing, nil
}

func (f *fakeAPI) ApplicationEmojiCreate(name, _ string) (*discordgo.Emoji, error) {
	if err := f.createErr[name]; err != nil {
		return nil, err
	}
	f.created = append(f.created, name)
	// Mint an id deterministically from the call order so the token is stable.
	return &discordgo.Emoji{ID: "100" + name, Name: name}, nil
}

func (f *fakeAPI) ApplicationEmojiDelete(id string) error {
	if err := f.deleteErr[id]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

// newTestSyncer builds a Syncer with prod-default toggles (prune on, replace
// off). upload mirrors whether assets were supplied. Tests override the fields
// they exercise.
func newTestSyncer(api emojiAPI, store *Store, assets map[string]string) *Syncer {
	return &Syncer{
		api:    api,
		store:  store,
		assets: assets,
		upload: assets != nil,
		prune:  true,
		log:    zap.NewNop(),
	}
}

func TestSyncUploadDisabledListsOnly(t *testing.T) {
	api := &fakeAPI{existing: []*discordgo.Emoji{{ID: "1", Name: "credits"}}}
	store := newStore()
	s := newTestSyncer(api, store, nil) // upload disabled

	require.NoError(t, s.sync(context.Background()))

	assert.Equal(t, "<:credits:1>", store.Format("credits"))
	assert.Empty(t, api.created, "nothing is uploaded when upload is disabled")
}

func TestSyncUploadsMissingSkipsExisting(t *testing.T) {
	api := &fakeAPI{existing: []*discordgo.Emoji{{ID: "1", Name: "credits"}}}
	store := newStore()
	// credits already exists; iron_bar is new and must be uploaded.
	s := newTestSyncer(api, store, map[string]string{
		"credits":  "data:image/png;base64,AA==",
		"iron_bar": "data:image/png;base64,AA==",
	})

	require.NoError(t, s.sync(context.Background()))

	assert.Equal(t, []string{"iron_bar"}, api.created, "only the missing emoji is uploaded")
	assert.Equal(t, "<:credits:1>", store.Format("credits"), "existing emoji kept as-is")
	assert.Equal(t, "<:iron_bar:100iron_bar>", store.Format("iron_bar"), "uploaded emoji added")
}

func TestSyncListErrorIsReturned(t *testing.T) {
	api := &fakeAPI{listErr: errors.New("boom")}
	store := newStore()
	s := newTestSyncer(api, store, nil)

	err := s.sync(context.Background())
	require.Error(t, err)
	assert.False(t, store.Has("anything"), "store untouched on list failure")
}

func TestSyncUploadErrorIsBestEffort(t *testing.T) {
	api := &fakeAPI{createErr: map[string]error{"bad": errors.New("too big")}}
	store := newStore()
	s := newTestSyncer(api, store, map[string]string{
		"bad":  "data:image/png;base64,AA==",
		"good": "data:image/png;base64,AA==",
	})

	require.NoError(t, s.sync(context.Background()), "a single upload failure does not fail the sync")
	assert.False(t, store.Has("bad"), "failed upload is skipped")
	assert.True(t, store.Has("good"), "other uploads still succeed")
}

func TestSyncPrunesEmojiNotInRepo(t *testing.T) {
	api := &fakeAPI{existing: []*discordgo.Emoji{
		{ID: "1", Name: "credits"},
		{ID: "9", Name: "stale"},
	}}
	store := newStore()
	s := newTestSyncer(api, store, map[string]string{"credits": "data:image/png;base64,AA=="})
	// prune is on by default in newTestSyncer.

	require.NoError(t, s.sync(context.Background()))

	assert.Equal(t, []string{"9"}, api.deleted, "the emoji absent from the repo is pruned")
	assert.True(t, store.Has("credits"))
	assert.False(t, store.Has("stale"), "pruned emoji is dropped from the store")
}

func TestSyncKeepsExtraWhenPruneDisabled(t *testing.T) {
	api := &fakeAPI{existing: []*discordgo.Emoji{
		{ID: "1", Name: "credits"},
		{ID: "9", Name: "stale"},
	}}
	store := newStore()
	s := newTestSyncer(api, store, map[string]string{"credits": "data:image/png;base64,AA=="})
	s.prune = false

	require.NoError(t, s.sync(context.Background()))

	assert.Empty(t, api.deleted, "nothing is deleted when prune is off")
	assert.Equal(t, "<:stale:9>", store.Format("stale"), "unmanaged emoji stays available")
}

func TestSyncReadOnlyNeverDeletes(t *testing.T) {
	// Read-only mode (upload off) must never prune, even if the flag is on, since
	// it has no embedded set to define what should exist.
	api := &fakeAPI{existing: []*discordgo.Emoji{
		{ID: "1", Name: "credits"},
		{ID: "9", Name: "admin_added"},
	}}
	store := newStore()
	s := newTestSyncer(api, store, nil) // upload off
	s.prune = true

	require.NoError(t, s.sync(context.Background()))

	assert.Empty(t, api.deleted, "read-only mode never deletes")
	assert.True(t, store.Has("credits"))
	assert.True(t, store.Has("admin_added"), "admin-uploaded emoji is exposed read-only")
}

func TestSyncReplaceRecreatesExisting(t *testing.T) {
	api := &fakeAPI{existing: []*discordgo.Emoji{{ID: "1", Name: "credits"}}}
	store := newStore()
	s := newTestSyncer(api, store, map[string]string{"credits": "data:image/png;base64,AA=="})
	s.replace = true

	require.NoError(t, s.sync(context.Background()))

	assert.Equal(t, []string{"1"}, api.deleted, "old emoji deleted before recreate")
	assert.Equal(t, []string{"credits"}, api.created, "emoji recreated")
	assert.Equal(t, "<:credits:100credits>", store.Format("credits"), "store points at the new id")
}

func TestSyncReplaceKeepsOldOnDeleteFailure(t *testing.T) {
	api := &fakeAPI{
		existing:  []*discordgo.Emoji{{ID: "1", Name: "credits"}},
		deleteErr: map[string]error{"1": errors.New("nope")},
	}
	store := newStore()
	s := newTestSyncer(api, store, map[string]string{"credits": "data:image/png;base64,AA=="})
	s.replace = true

	require.NoError(t, s.sync(context.Background()))

	assert.Empty(t, api.created, "no recreate when the delete failed")
	assert.Equal(t, "<:credits:1>", store.Format("credits"), "the old emoji is kept on delete failure")
}

func TestRunWaitsForConnectionThenBecomesReady(t *testing.T) {
	api := &fakeAPI{existing: []*discordgo.Emoji{{ID: "1", Name: "credits"}}}
	store := newStore()
	s := newTestSyncer(api, store, nil)

	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	assert.False(t, s.Ready(), "not ready while the gateway is down")

	api.connected.Store(true)
	require.Eventually(t, s.Ready, 2*time.Second, 10*time.Millisecond, "ready after sync completes")
	assert.Equal(t, "<:credits:1>", store.Format("credits"))
}
