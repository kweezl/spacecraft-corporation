package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

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
		{Kind: taskNotify, Handler: h.taskNotify},
	}
}

// notifyMentionCap bounds how many participants are @-mentioned in the closing-
// soon notice, so the message stays within Discord's length / mention limits; the
// rest collapse into a localized "+N more".
const notifyMentionCap = 50

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
	threadID, err := h.gw.CreateForumPost(forumCh, truncate(prog.Title, 100), embed, h.panelComponents(ctx, prog.ServerID))
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
	err = h.gw.EditPost(prog.ThreadID, h.renderEmbed(ctx, prog.ServerID, prog), h.panelComponents(ctx, prog.ServerID))
	if isDeletedPost(err) {
		// The forum post was deleted out from under us — recreate it (the retry
		// fix): drop the stale thread id and re-enqueue a create.
		return h.recreatePost(ctx, p.ContractID)
	}
	return permanentIfDiscord(err)
}

// recreatePost responds to a deleted forum post by clearing the stale thread id
// and enqueuing a fresh create-thread task; taskCreateThread's empty-thread guard
// then re-posts. Errors are transient so the worker retries.
func (h *Feature) recreatePost(ctx context.Context, id uuid.UUID) error {
	if err := h.repo.ClearThreadID(ctx, id); err != nil {
		return err
	}
	return h.repo.RequeueCreate(ctx, id)
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
	err = h.gw.ClosePost(prog.ThreadID, h.renderEmbed(ctx, prog.ServerID, prog))
	if isDeletedPost(err) {
		return nil // the post is already gone — nothing left to close
	}
	return permanentIfDiscord(err)
}

// taskNotify posts the one-shot "closing soon" comment. It pings only members who
// still owe delivery (reserved > delivered) — someone who has delivered everything
// they reserved has nothing left to do. When every participant has already
// delivered, it posts an informational notice that pings no one (the latch fired
// because the contract has participants, so there is still a comment to make).
// No-op once the thread is gone or the contract is no longer open.
func (h *Feature) taskNotify(ctx context.Context, t outbox.Task) error {
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
	// A deadline-less contract is never notified (and never reaches here via the
	// sweeper, which excludes NULL deadlines) — guard defensively.
	if prog.ThreadID == "" || prog.Status != StatusOpen || prog.Deadline == nil {
		return nil
	}
	ids, err := h.repo.OutstandingParticipantUserIDs(ctx, p.ContractID)
	if err != nil {
		return err
	}
	var content string
	var mentions []string
	if len(ids) == 0 {
		// All participants have delivered what they reserved — nobody to nudge, so
		// just leave an informational notice (no pings).
		content = h.loc.Render(ctx, prog.ServerID, "contracts.notify.closing_soon_done",
			map[string]any{"Left": formatTimeLeft(time.Until(*prog.Deadline))})
	} else {
		content, mentions = h.notifyContent(ctx, prog, ids)
	}
	return permanentIfDiscord(h.gw.CommentPost(prog.ThreadID, content, mentions))
}

// notifyContent renders the closing-soon message and the capped list of user ids
// to pass through AllowedMentions. The mention list is capped at notifyMentionCap
// (the overflow becomes a localized "+N more"); only the rendered ids are allowed
// to ping, so the "+N more" never silently mass-pings beyond the cap.
func (h *Feature) notifyContent(ctx context.Context, prog Progress, ids []string) (string, []string) {
	mentioned := ids
	if len(mentioned) > notifyMentionCap {
		mentioned = mentioned[:notifyMentionCap]
	}
	mentions := make([]string, len(mentioned))
	for i, id := range mentioned {
		mentions[i] = "<@" + id + ">"
	}
	list := strings.Join(mentions, " ")
	if len(ids) > len(mentioned) {
		list += " " + h.loc.Render(ctx, prog.ServerID, "contracts.notify.and_more",
			map[string]any{"Count": len(ids) - len(mentioned)})
	}
	content := h.loc.Render(ctx, prog.ServerID, "contracts.notify.closing_soon", map[string]any{
		"Mentions": list,
		"Left":     formatTimeLeft(time.Until(*prog.Deadline)),
	})
	return content, mentioned
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

// isDeletedPost reports a Discord error meaning the forum post/thread no longer
// exists (deleted out from under us). Distinct from isPermanentDiscordError: a
// deleted post is recoverable by re-posting, so taskRefresh recreates it rather
// than abandoning the task. Checked before permanentIfDiscord (UnknownChannel is
// permanent for a create against a missing forum, but recoverable for an edit
// against a missing thread).
func isDeletedPost(err error) bool {
	var re *discordgo.RESTError
	return errors.As(err, &re) && re.Message != nil &&
		(re.Message.Code == discordgo.ErrCodeUnknownMessage || re.Message.Code == discordgo.ErrCodeUnknownChannel)
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
