package contracts

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReportCSV is an in-memory ReportCSVConfig recording writes.
type fakeReportCSV struct {
	enabled bool
	sets    int
	last    bool
}

func (f *fakeReportCSV) ContractsReportCSV(context.Context, uuid.UUID) bool { return f.enabled }

func (f *fakeReportCSV) SetContractsReportCSV(_ context.Context, _ uuid.UUID, enabled bool) error {
	f.enabled, f.last = enabled, enabled
	f.sets++
	return nil
}

// reportCSVSelect builds a string-select interaction on this section's CustomID.
func reportCSVSelect(t *testing.T, vals ...string) *discordgo.InteractionCreate {
	t.Helper()
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		Data: discordgo.MessageComponentInteractionData{CustomID: reportCSVCustomID, Values: vals},
	}}
}

// selectFrom extracts the SelectMenu from a section's second row.
func selectFrom(t *testing.T, rows []discordgo.MessageComponent) discordgo.SelectMenu {
	t.Helper()
	require.Len(t, rows, 2)
	ar, ok := rows[1].(discordgo.ActionsRow)
	require.True(t, ok)
	sel, ok := ar.Components[0].(discordgo.SelectMenu)
	require.True(t, ok)
	return sel
}

// TestReportCSVSection_RowsMarkCurrentAndOwn: the select marks the current state
// as the default option and claims its CustomID.
func TestReportCSVSection_RowsMarkCurrentAndOwn(t *testing.T) {
	on := selectFrom(t, newReportCSVSection(&fakeReportCSV{enabled: true}, testLoc(t)).Rows(context.Background(), sid))
	assert.Equal(t, reportCSVCustomID, on.CustomID)
	require.Len(t, on.Options, 2)
	assert.Equal(t, reportCSVOn, on.Options[0].Value)
	assert.True(t, on.Options[0].Default, "enabled → the on option is the default")
	assert.False(t, on.Options[1].Default)

	off := selectFrom(t, newReportCSVSection(&fakeReportCSV{enabled: false}, testLoc(t)).Rows(context.Background(), sid))
	assert.False(t, off.Options[0].Default)
	assert.True(t, off.Options[1].Default, "disabled → the off option is the default")

	sec := newReportCSVSection(&fakeReportCSV{}, testLoc(t))
	assert.True(t, sec.Owns(reportCSVCustomID))
	assert.False(t, sec.Owns("settings:reports"))
}

// TestReportCSVSection_HandlePersistsToggle: picking on/off persists the boolean
// and re-renders the panel in place.
func TestReportCSVSection_HandlePersistsToggle(t *testing.T) {
	f := &fakeReportCSV{}
	sec := newReportCSVSection(f, testLoc(t))
	r := &sectionResponder{}
	rerender := func() []discordgo.MessageComponent {
		return []discordgo.MessageComponent{discordgo.TextDisplay{Content: "panel"}}
	}

	require.NoError(t, sec.Handle(context.Background(), r, reportCSVSelect(t, reportCSVOn), sid, rerender))
	assert.Equal(t, 1, f.sets)
	assert.True(t, f.last, "picking on stores true")
	assert.True(t, r.updated)

	require.NoError(t, sec.Handle(context.Background(), r, reportCSVSelect(t, reportCSVOff), sid, rerender))
	assert.Equal(t, 2, f.sets)
	assert.False(t, f.last, "picking off stores false")
}

// TestReportCSVSection_EmptySelectionNoWrite: an empty select re-renders without
// persisting.
func TestReportCSVSection_EmptySelectionNoWrite(t *testing.T) {
	f := &fakeReportCSV{}
	sec := newReportCSVSection(f, testLoc(t))
	r := &sectionResponder{}

	require.NoError(t, sec.Handle(context.Background(), r, reportCSVSelect(t), sid, func() []discordgo.MessageComponent { return nil }))
	assert.Zero(t, f.sets, "no write for an empty selection")
	assert.True(t, r.updated)
}
