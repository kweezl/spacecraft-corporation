package supply_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/features/supply"
	"github.com/kweezl/spacecraft-corporation/internal/features/supply/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
)

func testLoc(t *testing.T) *i18n.Localizer {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

// testFeatureWith builds a supply Feature with mock collaborators and the real
// compiled-in gamedata. setup may register forum/limit expectations.
func testFeatureWith(t *testing.T, repo *mocks.MockRepository, gw *mocks.MockGateway, setup func(forum *mocks.MockForumConfig, limit *mocks.MockLimitConfig)) *supply.Feature {
	t.Helper()
	reg, err := gamedata.Load(nil, nil)
	require.NoError(t, err)
	if repo == nil {
		repo = mocks.NewMockRepository(t)
	}
	if gw == nil {
		gw = mocks.NewMockGateway(t)
	}
	forum := mocks.NewMockForumConfig(t)
	limit := mocks.NewMockLimitConfig(t)
	if setup != nil {
		setup(forum, limit)
	}
	search := mocks.NewMockGameSearch(t)
	return supply.New(repo, testLoc(t), gw, forum, limit, search,
		i18n.StaticResolver{Theme: "standard", Lang: "en"}, reg, nil, zap.NewNop())
}

func testFeature(t *testing.T, repo *mocks.MockRepository, gw *mocks.MockGateway) *supply.Feature {
	return testFeatureWith(t, repo, gw, nil)
}

// invokeTask finds the outbox handler for kind and runs it against a payload.
func invokeTask(t *testing.T, f *supply.Feature, kind string, payload any) error {
	t.Helper()
	var handler outbox.Handler
	for _, r := range f.Registrations() {
		if r.Kind == kind {
			handler = r.Handler
		}
	}
	require.NotNil(t, handler, "no handler for %s", kind)
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return handler(context.Background(), outbox.Task{Kind: kind, Payload: raw})
}

func TestCommand_DiscordManagedNoKeys(t *testing.T) {
	f := testFeature(t, nil, nil)
	cmd := f.Command()
	assert.Equal(t, "supply", cmd.Def.Name)
	assert.True(t, cmd.DiscordManaged, "who can run /supply is configured in Discord")
	assert.False(t, cmd.DefaultDeny, "no bot-side coarse gate")
	assert.Empty(t, cmd.ExtraAccessKeys, "supply grants no bot keys — ownership is enforced in SQL")
}

func TestComponent_Prefix(t *testing.T) {
	f := testFeature(t, nil, nil)
	assert.Equal(t, "supply", f.Component().Prefix)
}

// TestTaskCreate_HappyPath posts the forum thread and records its id.
func TestTaskCreate_HappyPath(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	f := testFeatureWith(t, repo, gw, func(forum *mocks.MockForumConfig, _ *mocks.MockLimitConfig) {
		forum.EXPECT().SupplyForumChannelID(mock.Anything, mock.Anything).Return("forum-ch", true)
	})

	rid := uuid.New()
	prog := supply.Progress{Request: supply.Request{ID: rid, Title: "need parts", Status: supply.StatusOpen}}
	repo.EXPECT().ProgressByID(mock.Anything, rid).Return(prog, nil).Once()
	gw.EXPECT().CreateForumPost("forum-ch", "need parts", mock.Anything).Return("thread-77", nil).Once()
	repo.EXPECT().SetThreadID(mock.Anything, rid, "thread-77").Return(nil).Once()

	require.NoError(t, invokeTask(t, f, "supply.thread.create", map[string]any{"request_id": rid.String()}))
}

// TestTaskCreate_DeletesOrphanOnSetThreadIDFailure deletes the just-created
// thread when the id cannot be recorded, so the retry does not create a
// duplicate, and surfaces the transient error to retry.
func TestTaskCreate_DeletesOrphanOnSetThreadIDFailure(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	f := testFeatureWith(t, repo, gw, func(forum *mocks.MockForumConfig, _ *mocks.MockLimitConfig) {
		forum.EXPECT().SupplyForumChannelID(mock.Anything, mock.Anything).Return("forum-ch", true)
	})

	rid := uuid.New()
	prog := supply.Progress{Request: supply.Request{ID: rid, Title: "need parts", Status: supply.StatusOpen}}
	repo.EXPECT().ProgressByID(mock.Anything, rid).Return(prog, nil).Once()
	gw.EXPECT().CreateForumPost("forum-ch", "need parts", mock.Anything).Return("thread-77", nil).Once()
	repo.EXPECT().SetThreadID(mock.Anything, rid, "thread-77").Return(errors.New("db down")).Once()
	gw.EXPECT().DeletePost("thread-77").Return(nil).Once()

	err := invokeTask(t, f, "supply.thread.create", map[string]any{"request_id": rid.String()})
	require.ErrorContains(t, err, "db down", "the persistence error must surface (not be swallowed) so the task retries")
}

// TestTaskCreate_NoForum fails permanently when no forum is configured.
func TestTaskCreate_NoForum(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := testFeatureWith(t, repo, nil, func(forum *mocks.MockForumConfig, _ *mocks.MockLimitConfig) {
		forum.EXPECT().SupplyForumChannelID(mock.Anything, mock.Anything).Return("", false)
	})
	rid := uuid.New()
	repo.EXPECT().ProgressByID(mock.Anything, rid).Return(
		supply.Progress{Request: supply.Request{ID: rid, Status: supply.StatusOpen}}, nil).Once()

	err := invokeTask(t, f, "supply.thread.create", map[string]any{"request_id": rid.String()})
	require.Error(t, err)
}

// TestTaskCreate_Idempotent re-delivers without a second thread when one exists.
func TestTaskCreate_Idempotent(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	f := testFeature(t, repo, gw)
	rid := uuid.New()
	repo.EXPECT().ProgressByID(mock.Anything, rid).Return(
		supply.Progress{Request: supply.Request{ID: rid, ThreadID: "already", Status: supply.StatusOpen}}, nil).Once()
	// No CreateForumPost / SetThreadID expected.
	require.NoError(t, invokeTask(t, f, "supply.thread.create", map[string]any{"request_id": rid.String()}))
}

// TestTaskRefresh_NoThreadOrTerminalNoop skips the edit when there is no thread
// yet or the request is already terminal (the close task rendered the final card).
func TestTaskRefresh_NoThreadOrTerminalNoop(t *testing.T) {
	for _, prog := range []supply.Progress{
		{Request: supply.Request{Status: supply.StatusOpen}},                     // no thread
		{Request: supply.Request{ThreadID: "t", Status: supply.StatusCancelled}}, // terminal
	} {
		repo := mocks.NewMockRepository(t)
		gw := mocks.NewMockGateway(t) // no EditPost expected
		f := testFeature(t, repo, gw)
		rid := uuid.New()
		repo.EXPECT().ProgressByID(mock.Anything, rid).Return(prog, nil).Once()
		require.NoError(t, invokeTask(t, f, "supply.thread.refresh", map[string]any{"request_id": rid.String()}))
	}
}

// TestTaskRefresh_DeletedPostRecreates recreates the post when the live thread
// was deleted out from under us.
func TestTaskRefresh_DeletedPostRecreates(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	f := testFeature(t, repo, gw)
	rid := uuid.New()
	repo.EXPECT().ProgressByID(mock.Anything, rid).Return(
		supply.Progress{Request: supply.Request{ID: rid, ThreadID: "gone", Status: supply.StatusOpen}}, nil).Once()
	deleted := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}
	gw.EXPECT().EditPost("gone", mock.Anything).Return(deleted).Once()
	repo.EXPECT().RecreatePost(mock.Anything, rid).Return(nil).Once()
	require.NoError(t, invokeTask(t, f, "supply.thread.refresh", map[string]any{"request_id": rid.String()}))
}

// TestTaskClose_NoThreadNoop / happy path.
func TestTaskClose(t *testing.T) {
	// No thread → no-op.
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	f := testFeature(t, repo, gw)
	rid := uuid.New()
	repo.EXPECT().ProgressByID(mock.Anything, rid).Return(
		supply.Progress{Request: supply.Request{ID: rid, ThreadID: ""}}, nil).Once()
	require.NoError(t, invokeTask(t, f, "supply.thread.close", map[string]any{"request_id": rid.String()}))

	// Live thread → ClosePost (final card, no buttons).
	repo2 := mocks.NewMockRepository(t)
	gw2 := mocks.NewMockGateway(t)
	f2 := testFeature(t, repo2, gw2)
	rid2 := uuid.New()
	repo2.EXPECT().ProgressByID(mock.Anything, rid2).Return(
		supply.Progress{Request: supply.Request{ID: rid2, ThreadID: "th", Status: supply.StatusCancelled}}, nil).Once()
	gw2.EXPECT().ClosePost("th", mock.Anything).Return(nil).Once()
	require.NoError(t, invokeTask(t, f2, "supply.thread.close", map[string]any{"request_id": rid2.String()}))
}
