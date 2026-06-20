package contracts_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
)

// handlerFor returns the registered outbox handler for a kind.
func handlerFor(t *testing.T, f *contracts.Feature, kind string) outbox.Handler {
	t.Helper()
	for _, r := range f.Registrations() {
		if r.Kind == kind {
			return r.Handler
		}
	}
	t.Fatalf("no outbox handler for kind %q", kind)
	return nil
}

func payload(t *testing.T, cid uuid.UUID, appID, token string) outbox.Task {
	t.Helper()
	b, err := json.Marshal(map[string]any{"contract_id": cid, "app_id": appID, "token": token})
	require.NoError(t, err)
	return outbox.Task{ID: uuid.New(), Kind: "test", Version: 1, Payload: b}
}

func openProgress(cid uuid.UUID, threadID string) contracts.Progress {
	return contracts.Progress{Contract: contracts.Contract{
		ID: cid, ServerID: gid, ThreadID: threadID, Title: "Steel Run",
		Status: contracts.StatusOpen, Deadline: time.Now().Add(time.Hour),
	}}
}

func TestTaskCreateThread_CreatesAndNotifies(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	forum := mocks.NewMockForumConfig(t)

	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, ""), nil).Once()
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("forum-1", true).Once()
	gw.EXPECT().CreateForumPost("forum-1", "Steel Run", mock.Anything).Return("thread-9", nil).Once()
	repo.EXPECT().SetThreadID(mock.Anything, cid, "thread-9").Return(nil).Once()
	gw.EXPECT().EditOriginalResponse("app", "tok", mock.Anything).Return(nil).Once()

	f := newFeature(t, repo, gw, forum)
	require.NoError(t, handlerFor(t, f, "contracts.thread.create")(context.Background(), payload(t, cid, "app", "tok")))
}

func TestTaskCreateThread_Idempotent(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	// Thread already exists (re-claimed task): no creation, just re-notify.
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, "thread-9"), nil).Once()
	gw.EXPECT().EditOriginalResponse("app", "tok", mock.Anything).Return(nil).Once()

	f := newFeature(t, repo, gw, mocks.NewMockForumConfig(t))
	require.NoError(t, handlerFor(t, f, "contracts.thread.create")(context.Background(), payload(t, cid, "app", "tok")))
}

func TestTaskCreateThread_NoForumIsPermanent(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	forum := mocks.NewMockForumConfig(t)

	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, ""), nil).Once()
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("", false).Once()
	gw.EXPECT().EditOriginalResponse("app", "tok", mock.Anything).Return(nil).Once()

	f := newFeature(t, repo, gw, forum)
	err := handlerFor(t, f, "contracts.thread.create")(context.Background(), payload(t, cid, "app", "tok"))
	require.Error(t, err, "no forum is a permanent failure so the worker stops retrying")
}

func TestTaskRefresh_EditsEmbed(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, "thread-9"), nil).Once()
	gw.EXPECT().EditPost("thread-9", mock.Anything).Return(nil).Once()

	f := newFeature(t, repo, gw, mocks.NewMockForumConfig(t))
	require.NoError(t, handlerFor(t, f, "contracts.thread.refresh")(context.Background(), payload(t, cid, "", "")))
}

func TestTaskRefresh_TerminalNoOp(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	prog := openProgress(cid, "thread-9")
	prog.Status = contracts.StatusCompleted
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
	// A refresh landing after close must not touch the (locked) thread: the
	// Gateway has no EditPost expectation, so a call would fail the test.
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, handlerFor(t, f, "contracts.thread.refresh")(context.Background(), payload(t, cid, "", "")))
}

func TestTaskRefresh_NoThreadYet(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	// Thread not created yet -> no-op (no EditPost).
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, ""), nil).Once()

	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, handlerFor(t, f, "contracts.thread.refresh")(context.Background(), payload(t, cid, "", "")))
}

func TestTaskClose_LocksThread(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	prog := openProgress(cid, "thread-9")
	prog.Status = contracts.StatusExpired
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
	gw.EXPECT().ClosePost("thread-9", mock.Anything).Return(nil).Once()

	f := newFeature(t, repo, gw, mocks.NewMockForumConfig(t))
	require.NoError(t, handlerFor(t, f, "contracts.thread.close")(context.Background(), payload(t, cid, "", "")))
}

func TestTaskCreateThread_TransientErrorRetries(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	forum := mocks.NewMockForumConfig(t)

	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, ""), nil).Once()
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("forum-1", true).Once()
	// A non-Discord transient error bubbles up for retry (not Permanent, no notify).
	gw.EXPECT().CreateForumPost("forum-1", "Steel Run", mock.Anything).Return("", errors.New("503")).Once()

	f := newFeature(t, repo, gw, forum)
	err := handlerFor(t, f, "contracts.thread.create")(context.Background(), payload(t, cid, "app", "tok"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

// Permission errors are recognized as permanent (verified via the discordgo
// REST error code path).
func TestTaskCreateThread_PermissionIsPermanent(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	forum := mocks.NewMockForumConfig(t)

	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(openProgress(cid, ""), nil).Once()
	forum.EXPECT().ContractsForumChannelID(mock.Anything, gid).Return("forum-1", true).Once()
	restErr := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeMissingPermissions}}
	gw.EXPECT().CreateForumPost("forum-1", "Steel Run", mock.Anything).Return("", restErr).Once()
	gw.EXPECT().EditOriginalResponse("app", "tok", mock.Anything).Return(nil).Once()

	f := newFeature(t, repo, gw, forum)
	err := handlerFor(t, f, "contracts.thread.create")(context.Background(), payload(t, cid, "app", "tok"))
	require.Error(t, err)
}
