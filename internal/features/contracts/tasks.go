package contracts

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
		{Kind: taskCreateThread, Handler: h.taskCreateThread},
		{Kind: taskRefresh, Handler: h.taskRefresh},
		{Kind: taskClose, Handler: h.taskClose},
	}
}

func decodePayload(t outbox.Task) (taskPayload, error) {
	var p taskPayload
	err := json.Unmarshal(t.Payload, &p)
	return p, err
}

// taskCreateThread creates the contract's forum thread (with the progress embed
// as its starter message), records the thread id, and edits the original reply
// with the outcome. Idempotent: if the thread already exists (a re-run task),
// it re-delivers the outcome without creating a second thread.
func (h *Feature) taskCreateThread(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.ContractID)
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

	forumCh, ok := h.forum.ContractsForumChannelID(ctx, prog.ServerID)
	if !ok {
		h.notify(ctx, p, prog.ServerID, "contracts.create.failed_forum", nil)
		return outbox.Permanent(errors.New("contracts: no forum configured"))
	}

	embed := h.renderEmbed(ctx, prog.ServerID, prog)
	threadID, err := h.gw.CreateForumPost(forumCh, truncate(prog.Title, 100), embed)
	if err != nil {
		if isPermanentDiscordError(err) {
			h.notify(ctx, p, prog.ServerID, "contracts.create.failed_perms", nil)
			return outbox.Permanent(err)
		}
		return err // transient: retry
	}
	// Small non-idempotent window: a crash between here and SetThreadID would make
	// a retry create a second thread (orphaning this one). Saving immediately keeps
	// it tiny; Discord has no idempotency key for thread creation.
	if err := h.repo.SetThreadID(ctx, p.ContractID, threadID); err != nil {
		return err
	}
	prog.ThreadID = threadID
	h.notifyCreated(ctx, p, prog)
	return nil
}

// taskRefresh re-renders the progress embed. No-op until the thread exists, and
// once the contract is terminal — a refresh that lands after the close task (e.g.
// tasks spilled across batches) would otherwise try to edit a locked/archived
// thread and fail; the close task already rendered the final embed.
func (h *Feature) taskRefresh(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.ContractID)
	if errors.Is(err, ErrNotFound) {
		return outbox.Permanent(err)
	}
	if err != nil {
		return err
	}
	if prog.ThreadID == "" || prog.Status != StatusOpen {
		return nil
	}
	return permanentIfDiscord(h.gw.EditPost(prog.ThreadID, h.renderEmbed(ctx, prog.ServerID, prog)))
}

// taskClose writes the final embed and locks/archives the thread.
func (h *Feature) taskClose(ctx context.Context, t outbox.Task) error {
	p, err := decodePayload(t)
	if err != nil {
		return outbox.Permanent(err)
	}
	prog, err := h.repo.ProgressByID(ctx, p.ContractID)
	if errors.Is(err, ErrNotFound) {
		return outbox.Permanent(err)
	}
	if err != nil {
		return err
	}
	if prog.ThreadID == "" {
		return nil
	}
	return permanentIfDiscord(h.gw.ClosePost(prog.ThreadID, h.renderEmbed(ctx, prog.ServerID, prog)))
}

// notifyCreated edits the original reply with the created thread (a <#thread>
// mention links to it without needing the guild id). Best-effort.
func (h *Feature) notifyCreated(ctx context.Context, p taskPayload, prog Progress) {
	h.notify(ctx, p, prog.ServerID, "contracts.create.ok", map[string]any{
		"Title": prog.Title, "Thread": prog.ThreadID,
	})
}

// notify edits the interaction's original reply, when the task carries a token.
// Best-effort: a failure (e.g. the ~15-min token has expired) is logged, never
// retried — the thread itself is the durable signal.
func (h *Feature) notify(ctx context.Context, p taskPayload, serverID uuid.UUID, key string, data map[string]any) {
	if p.Token == "" {
		return
	}
	msg := h.loc.Render(ctx, serverID, key, data)
	if err := h.gw.EditOriginalResponse(p.AppID, p.Token, msg); err != nil {
		h.log.Warn("contracts: notify create outcome",
			zap.String("contract_id", p.ContractID.String()), zap.Error(err))
	}
}

// isPermanentDiscordError reports Discord REST errors that retrying cannot fix:
// missing access/permissions, or the forum channel no longer existing.
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

// permanentIfDiscord marks a permanent Discord error so the worker abandons the
// task instead of retrying (e.g. editing a locked/archived thread returns
// MissingPermissions — retrying can't help). Other errors stay transient.
func permanentIfDiscord(err error) error {
	if err != nil && isPermanentDiscordError(err) {
		return outbox.Permanent(err)
	}
	return err
}
