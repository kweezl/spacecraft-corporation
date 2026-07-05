package supply

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/outbox"
)

// Registrations are the outbox handlers this feature contributes. Each performs
// the Discord side effect for one task kind; all are idempotent (a task may run
// more than once after a lease re-claim).
func (h *Feature) Registrations() []outbox.Registration {
	return []outbox.Registration{
		{Kind: taskCreate, Handler: h.taskCreate},
		{Kind: taskRefresh, Handler: h.taskRefresh},
		{Kind: taskClose, Handler: h.taskClose},
	}
}

func decodePayload(t outbox.Task) (taskPayload, error) {
	var p taskPayload
	err := json.Unmarshal(t.Payload, &p)
	return p, err
}

// taskCreate creates the request's forum thread (with the card as its starter
// message), records the thread id, and edits the original reply. Idempotent: if
// the thread already exists it re-delivers the outcome without a second thread.
func (h *Feature) taskCreate(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.RequestID)
	if errors.Is(err, ErrNotFound) {
		return outbox.Permanent(err)
	}
	if err != nil {
		return err
	}
	if prog.ThreadID != "" {
		h.notifyCreated(ctx, p, prog)
		return nil
	}
	forumCh, ok := h.forum.SupplyForumChannelID(ctx, prog.ServerID)
	if !ok {
		h.notify(ctx, p, prog.ServerID, "supply.create.failed_forum")
		return outbox.Permanent(errors.New("supply: no forum configured"))
	}
	threadID, err := h.gw.CreateForumPost(forumCh, truncate(prog.Title, 100), h.postComponents(ctx, prog.ServerID, prog, true))
	if err != nil {
		if isPermanentDiscordError(err) {
			h.notify(ctx, p, prog.ServerID, "supply.create.failed_perms")
			return outbox.Permanent(err)
		}
		return err // transient: retry
	}
	if err := h.repo.SetThreadID(ctx, p.RequestID, threadID); err != nil {
		// The thread exists but we could not record its id; a retry would see an
		// empty ThreadID and create a duplicate. Best-effort delete the orphan so
		// the retry starts clean, then surface the (transient) error to retry.
		if delErr := h.gw.DeletePost(threadID); delErr != nil {
			h.log.Warn("supply: delete orphaned thread after SetThreadID failure",
				zap.String("request_id", p.RequestID.String()),
				zap.String("thread_id", threadID), zap.Error(delErr))
		}
		return err
	}
	prog.ThreadID = threadID
	h.notifyCreated(ctx, p, prog)
	return nil
}

// taskRefresh re-renders the card. No-op until the thread exists and once the
// request is terminal (the close task already rendered the final card).
func (h *Feature) taskRefresh(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.RequestID)
	if errors.Is(err, ErrNotFound) {
		return outbox.Permanent(err)
	}
	if err != nil {
		return err
	}
	if prog.ThreadID == "" || prog.Status != StatusOpen {
		return nil
	}
	err = h.gw.EditPost(prog.ThreadID, h.postComponents(ctx, prog.ServerID, prog, true))
	if isDeletedPost(err) {
		return h.repo.RecreatePost(ctx, p.RequestID)
	}
	return permanentIfDiscord(err)
}

// taskClose writes the final card (no buttons) and locks/archives the thread.
func (h *Feature) taskClose(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.RequestID)
	if errors.Is(err, ErrNotFound) {
		return outbox.Permanent(err)
	}
	if err != nil {
		return err
	}
	if prog.ThreadID == "" {
		return nil
	}
	err = h.gw.ClosePost(prog.ThreadID, h.postComponents(ctx, prog.ServerID, prog, false))
	if isDeletedPost(err) {
		return nil
	}
	return permanentIfDiscord(err)
}

// notifyCreated edits the original reply with the created thread link.
func (h *Feature) notifyCreated(ctx context.Context, p taskPayload, prog Progress) {
	if p.Token == "" {
		return
	}
	msg := h.loc.Render(ctx, prog.ServerID, "supply.create.ok", map[string]any{"Title": prog.Title, "Thread": prog.ThreadID})
	if err := h.gw.EditOriginalResponse(p.AppID, p.Token, msg); err != nil {
		h.log.Warn("supply: notify create outcome", zap.String("request_id", p.RequestID.String()), zap.Error(err))
	}
}

// notify edits the interaction's original reply with a keyed message, when the
// task carries a token. Best-effort.
func (h *Feature) notify(ctx context.Context, p taskPayload, serverID uuid.UUID, key string) {
	if p.Token == "" {
		return
	}
	if err := h.gw.EditOriginalResponse(p.AppID, p.Token, h.loc.Render(ctx, serverID, key, nil)); err != nil {
		h.log.Warn("supply: notify create outcome", zap.String("request_id", p.RequestID.String()), zap.Error(err))
	}
}

// isPermanentDiscordError reports Discord REST errors retrying cannot fix.
func isPermanentDiscordError(err error) bool {
	var re *discordgo.RESTError
	if errors.As(err, &re) && re.Message != nil {
		switch re.Message.Code {
		case discordgo.ErrCodeMissingAccess, discordgo.ErrCodeMissingPermissions, discordgo.ErrCodeUnknownChannel:
			return true
		}
	}
	return false
}

// isDeletedPost reports a Discord error meaning the forum post/thread is gone.
func isDeletedPost(err error) bool {
	var re *discordgo.RESTError
	return errors.As(err, &re) && re.Message != nil &&
		(re.Message.Code == discordgo.ErrCodeUnknownMessage || re.Message.Code == discordgo.ErrCodeUnknownChannel)
}

// permanentIfDiscord marks a permanent Discord error so the worker abandons the
// task instead of retrying; other errors stay transient.
func permanentIfDiscord(err error) error {
	if err != nil && isPermanentDiscordError(err) {
		return outbox.Permanent(err)
	}
	return err
}
