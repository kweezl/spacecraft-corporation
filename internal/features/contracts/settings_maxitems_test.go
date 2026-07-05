package contracts

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeItemCap is an in-memory ItemCap recording writes.
type fakeItemCap struct {
	limit int
	set   bool
	sets  int
}

func (f *fakeItemCap) ContractsMaxItems(context.Context, uuid.UUID) (int, bool) {
	return f.limit, f.set
}
func (f *fakeItemCap) SetContractsMaxItems(_ context.Context, _ uuid.UUID, limit int) error {
	f.limit, f.set, f.sets = limit, true, f.sets+1
	return nil
}

func maxItemsButtonInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		Data: discordgo.MessageComponentInteractionData{CustomID: maxItemsCustomID},
	}}
}

func maxItemsModalInteraction(value string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: maxItemsModalCustomID,
			Components: []discordgo.MessageComponent{&discordgo.Label{Component: &discordgo.TextInput{
				CustomID: inMaxItems, Value: value,
			}}},
		},
	}}
}

// TestMaxItemsFor resolves the per-server cap at apply time, falling back to
// DefaultMaxItems when unset (the ItemCap contract the pick destinations use).
func TestMaxItemsFor(t *testing.T) {
	ctx := context.Background()
	unset := &Feature{itemCap: &fakeItemCap{set: false}}
	assert.Equal(t, DefaultMaxItems, unset.maxItemsFor(ctx, sid))

	set := &Feature{itemCap: &fakeItemCap{limit: 40, set: true}}
	assert.Equal(t, 40, set.maxItemsFor(ctx, sid))
}

// TestMaxItemsSection_UnsetShowsDefault renders DefaultMaxItems when the server
// has no per-server cap, and claims both CustomIDs.
func TestMaxItemsSection_UnsetShowsDefault(t *testing.T) {
	sec := newMaxItemsSection(&fakeItemCap{set: false}, testLoc(t))

	rows := sec.Rows(context.Background(), sid)
	require.NotEmpty(t, rows)
	td := rows[0].(discordgo.TextDisplay)
	assert.Contains(t, td.Content, "25", "unset falls back to DefaultMaxItems")

	assert.True(t, sec.Owns(maxItemsCustomID))
	assert.True(t, sec.Owns(maxItemsModalCustomID))
	assert.False(t, sec.Owns("settings:forum"))
}

// TestMaxItemsSection_ButtonOpensPrefilledModal prefills the set value.
func TestMaxItemsSection_ButtonOpensPrefilledModal(t *testing.T) {
	sec := newMaxItemsSection(&fakeItemCap{limit: 40, set: true}, testLoc(t))
	r := &sectionResponder{}

	require.NoError(t, sec.Handle(context.Background(), r, maxItemsButtonInteraction(), sid, nil))
	assert.Equal(t, maxItemsModalCustomID, r.modalCustomID)
	require.Len(t, r.modalComps, 1)
	in := r.modalComps[0].(discordgo.Label).Component.(discordgo.TextInput)
	assert.Equal(t, "40", in.Value)
}

// TestMaxItemsSection_SubmitPersistsAndRerenders persists a valid positive int.
func TestMaxItemsSection_SubmitPersistsAndRerenders(t *testing.T) {
	c := &fakeItemCap{}
	sec := newMaxItemsSection(c, testLoc(t))
	r := &sectionResponder{}
	rerender := func() []discordgo.MessageComponent {
		return []discordgo.MessageComponent{discordgo.TextDisplay{Content: "panel"}}
	}

	require.NoError(t, sec.Handle(context.Background(), r, maxItemsModalInteraction("50"), sid, rerender))
	assert.Equal(t, 1, c.sets)
	assert.Equal(t, 50, c.limit)
	assert.True(t, r.updated)
}

// TestMaxItemsSection_BadValueRejected rejects a non-positive/non-numeric value.
func TestMaxItemsSection_BadValueRejected(t *testing.T) {
	for _, bad := range []string{"", "0", "-3", "abc"} {
		c := &fakeItemCap{}
		sec := newMaxItemsSection(c, testLoc(t))
		r := &sectionResponder{}
		require.NoError(t, sec.Handle(context.Background(), r, maxItemsModalInteraction(bad), sid, nil))
		assert.Zerof(t, c.sets, "no write for %q", bad)
		assert.NotEmptyf(t, r.ephemeral, "bad value %q gets an error reply", bad)
	}
}
