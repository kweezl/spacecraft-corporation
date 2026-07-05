package session

import (
	"io"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// msgFake is a fakeDiscord that records channel message sends/edits — for the
// payout-report post (PostChannelMessage) and in-place edit (EditChannelMessage).
type msgFake struct {
	fakeDiscord
	sent     *discordgo.MessageSend
	sentBody string // drained file contents of the last send
	edited   *discordgo.MessageEdit
}

func (f *msgFake) ChannelMessageSendComplex(_ string, data *discordgo.MessageSend) (*discordgo.Message, error) {
	f.sent = data
	for _, file := range data.Files {
		b, _ := io.ReadAll(file.Reader)
		f.sentBody = string(b)
	}
	return &discordgo.Message{ID: "msg-1"}, nil
}

func (f *msgFake) ChannelMessageEditComplex(data *discordgo.MessageEdit) (*discordgo.Message, error) {
	f.edited = data
	return &discordgo.Message{ID: data.ID}, nil
}

func liveWith(f Discord) *Live {
	l := newLive()
	l.set(f)
	return l
}

func actionsRow() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{CustomID: "b1"},
	}}}
}

// TestPostChannelMessage passes content, mentions, files, and components through
// and returns the new message id.
func TestPostChannelMessage(t *testing.T) {
	f := &msgFake{}
	id, err := liveWith(f).PostChannelMessage("c1", "hi", []string{"u1"},
		[]*discordgo.File{{Name: "payout.csv", Reader: strings.NewReader("data")}}, actionsRow())
	require.NoError(t, err)
	assert.Equal(t, "msg-1", id)
	require.NotNil(t, f.sent)
	assert.Equal(t, "hi", f.sent.Content)
	require.NotNil(t, f.sent.AllowedMentions)
	assert.Equal(t, []string{"u1"}, f.sent.AllowedMentions.Users)
	assert.Equal(t, "data", f.sentBody, "the CSV is attached")
	assert.Len(t, f.sent.Components, 1)
}

// TestEditChannelMessage with fresh files replaces the attachments (empty
// Attachments list drops the old, Files adds the new) so the CSV refreshes.
func TestEditChannelMessage(t *testing.T) {
	f := &msgFake{}
	files := []*discordgo.File{{Name: "payout.csv", Reader: strings.NewReader("new")}}
	require.NoError(t, liveWith(f).EditChannelMessage("c1", "msg-1", "updated", files, actionsRow()))
	require.NotNil(t, f.edited)
	require.NotNil(t, f.edited.Content)
	assert.Equal(t, "updated", *f.edited.Content)
	require.NotNil(t, f.edited.Components)
	require.NotNil(t, f.edited.Attachments, "attachments field is present to drop the old CSV")
	assert.Empty(t, *f.edited.Attachments, "no existing attachments retained")
	assert.Len(t, f.edited.Files, 1, "the fresh CSV is attached")
}

// TestEditChannelMessage_NilFilesKeepsAttachment leaves the Attachments field
// absent so Discord keeps the existing attachment.
func TestEditChannelMessage_NilFilesKeepsAttachment(t *testing.T) {
	f := &msgFake{}
	require.NoError(t, liveWith(f).EditChannelMessage("c1", "msg-1", "updated", nil, actionsRow()))
	require.NotNil(t, f.edited)
	assert.Nil(t, f.edited.Attachments, "attachments untouched so the existing CSV survives")
}

// TestChannelMessage_NotConnected surfaces ErrNotConnected before a session opens.
func TestChannelMessage_NotConnected(t *testing.T) {
	l := newLive()
	_, err := l.PostChannelMessage("c1", "hi", nil, nil, nil)
	require.ErrorIs(t, err, ErrNotConnected)
	require.ErrorIs(t, l.EditChannelMessage("c1", "m1", "x", nil, nil), ErrNotConnected)
}
