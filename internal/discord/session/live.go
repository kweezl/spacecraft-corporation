package session

import (
	"errors"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// forumAutoArchiveMax is Discord's maximum thread auto-archive duration (7 days,
// in minutes). Contracts can outlive shorter windows, so threads are created
// with the max to avoid self-archiving while a contract is still open.
const forumAutoArchiveMax = 10080

// ErrNotConnected is returned by the proactive helpers when there is no live
// gateway session (before Start, or during a reconnect). Callers treat it as
// transient.
var ErrNotConnected = errors.New("session: not connected")

// Live holds the currently-open Discord session and exposes the proactive
// operations (creating/editing/locking threads) that happen outside the
// interaction-response path. It is deliberately a separate, dependency-free type
// rather than a method set on Manager: features that need proactive Discord
// access (contracts) depend on Live, while Manager depends on the registry — and
// the registry collects those features' commands. Routing proactive access
// through Live instead of Manager keeps that out of a dependency cycle.
//
// Manager publishes the session into Live on Start and clears it on Stop; the
// readiness probe and proactive callers read it under the same lock.
type Live struct {
	mu      sync.RWMutex
	session Discord
}

func newLive() *Live { return &Live{} }

func (l *Live) set(d Discord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.session = d
}

func (l *Live) get() Discord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.session
}

// Connected reports whether the gateway is live and past its READY handshake.
func (l *Live) Connected() bool {
	s := l.get()
	return s != nil && s.Connected()
}

// CreateForumPost opens a new forum thread in channelID with title name and a
// Components V2 card as its starter message (the IsComponentsV2 flag means the
// body is components-only — no content/embeds). It returns the thread id, which
// for a forum thread is also the starter message id (used by EditPost/ClosePost).
func (l *Live) CreateForumPost(channelID, name string, components []discordgo.MessageComponent) (string, error) {
	s := l.get()
	if s == nil {
		return "", ErrNotConnected
	}
	th, err := s.ForumThreadStartComplex(channelID,
		&discordgo.ThreadStart{Name: name, AutoArchiveDuration: forumAutoArchiveMax},
		&discordgo.MessageSend{Components: components, Flags: discordgo.MessageFlagsIsComponentsV2})
	if err != nil {
		return "", err
	}
	return th.ID, nil
}

// EditPost replaces the starter message's Components V2 card of a contract thread
// (threadID is both the thread and starter-message id). The IsComponentsV2 flag
// must be set on the edit too (it is immutable, set at creation); a message first
// posted before the V2 migration can't be edited this way and returns
// ErrCodeInvalidFormBody — callers treat that like a deleted post and recreate.
func (l *Live) EditPost(threadID string, components []discordgo.MessageComponent) error {
	s := l.get()
	if s == nil {
		return ErrNotConnected
	}
	edit := discordgo.NewMessageEdit(threadID, threadID)
	edit.Flags = discordgo.MessageFlagsIsComponentsV2
	edit.Components = &components
	_, err := s.ChannelMessageEditComplex(edit)
	return err
}

// ClosePost writes the final card to the contract thread and then archives and
// locks it, so no further messages or commands land on a completed/expired
// contract. The edit happens while the thread is still open.
func (l *Live) ClosePost(threadID string, components []discordgo.MessageComponent) error {
	if err := l.EditPost(threadID, components); err != nil {
		return err
	}
	s := l.get()
	if s == nil {
		return ErrNotConnected
	}
	archived, locked := true, true
	_, err := s.ChannelEditComplex(threadID, &discordgo.ChannelEdit{Archived: &archived, Locked: &locked})
	return err
}

// CommentPost posts a plain message into a contract thread, mentioning
// mentionUserIDs. The ids are passed through AllowedMentions so they actually
// ping (without it Discord renders <@id> as inert text); nothing else is allowed
// to ping. Used for the pre-expiry "closing soon" notice.
func (l *Live) CommentPost(threadID, content string, mentionUserIDs []string) error {
	s := l.get()
	if s == nil {
		return ErrNotConnected
	}
	_, err := s.ChannelMessageSendComplex(threadID, &discordgo.MessageSend{
		Content:         content,
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: mentionUserIDs},
	})
	return err
}

// EditOriginalResponse edits the original reply of an interaction identified by
// its app id and token (the async create outcome). The token is valid ~15 min;
// past that this fails and the caller logs it as best-effort.
func (l *Live) EditOriginalResponse(appID, token, content string) error {
	s := l.get()
	if s == nil {
		return ErrNotConnected
	}
	_, err := s.InteractionResponseEdit(
		&discordgo.Interaction{AppID: appID, Token: token},
		&discordgo.WebhookEdit{Content: &content})
	return err
}
