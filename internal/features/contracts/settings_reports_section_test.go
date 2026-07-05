package contracts

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReports is an in-memory ReportsConfig recording writes.
type fakeReports struct {
	channel string
	set     bool
	sets    int
}

func (f *fakeReports) ContractsReportsChannelID(context.Context, uuid.UUID) (string, bool) {
	return f.channel, f.set
}

func (f *fakeReports) SetContractsReportsChannelID(_ context.Context, _ uuid.UUID, ch string) error {
	f.channel, f.set = ch, true
	f.sets++
	return nil
}

func reportsSelect(t *testing.T, ch string, vals ...string) *discordgo.InteractionCreate {
	t.Helper()
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		Data: discordgo.MessageComponentInteractionData{CustomID: ch, Values: vals},
	}}
}

// TestReportsSection_RowsAndOwnership renders a prefilled text-channel select and
// claims its CustomID.
func TestReportsSection_RowsAndOwnership(t *testing.T) {
	sec := newReportsSection(&fakeReports{channel: "chan-9", set: true}, testLoc(t))

	rows := sec.Rows(context.Background(), sid)
	require.Len(t, rows, 2)
	ar, ok := rows[1].(discordgo.ActionsRow)
	require.True(t, ok)
	sel, ok := ar.Components[0].(discordgo.SelectMenu)
	require.True(t, ok)
	assert.Equal(t, reportsCustomID, sel.CustomID)
	require.Len(t, sel.DefaultValues, 1)
	assert.Equal(t, "chan-9", sel.DefaultValues[0].ID)
	assert.Contains(t, sel.ChannelTypes, discordgo.ChannelTypeGuildText, "reports go to a text channel")

	assert.True(t, sec.Owns(reportsCustomID))
	assert.False(t, sec.Owns("settings:forum"))
}

// TestReportsSection_HandlePersistsAndRerenders persists the picked channel and
// re-renders the panel in place.
func TestReportsSection_HandlePersistsAndRerenders(t *testing.T) {
	f := &fakeReports{}
	sec := newReportsSection(f, testLoc(t))
	r := &sectionResponder{}
	rerender := func() []discordgo.MessageComponent {
		return []discordgo.MessageComponent{discordgo.TextDisplay{Content: "panel"}}
	}

	require.NoError(t, sec.Handle(context.Background(), r, reportsSelect(t, reportsCustomID, "picked-1"), sid, rerender))
	assert.Equal(t, 1, f.sets)
	assert.Equal(t, "picked-1", f.channel)
	assert.True(t, r.updated, "the panel re-renders in place")
	require.Len(t, r.updatedComps, 1)
}

// TestReportsSection_EmptySelectionNoWrite: an empty select re-renders without
// persisting.
func TestReportsSection_EmptySelectionNoWrite(t *testing.T) {
	f := &fakeReports{}
	sec := newReportsSection(f, testLoc(t))
	r := &sectionResponder{}

	require.NoError(t, sec.Handle(context.Background(), r, reportsSelect(t, reportsCustomID), sid, func() []discordgo.MessageComponent { return nil }))
	assert.Zero(t, f.sets, "no write for an empty selection")
	assert.True(t, r.updated)
}
