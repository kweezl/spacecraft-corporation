package contracts

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDefaults is an in-memory RewardDefaults recording writes.
type fakeDefaults struct {
	factor decimal.Decimal
	sets   int
}

func (f *fakeDefaults) ContractsRewardFactor(context.Context, uuid.UUID) decimal.Decimal {
	return f.factor
}

func (f *fakeDefaults) SetContractsRewardFactor(_ context.Context, _ uuid.UUID, factor decimal.Decimal) error {
	f.factor = factor
	f.sets++
	return nil
}

// sectionResponder records the responses the section sends.
type sectionResponder struct {
	ephemeral     string
	modalCustomID string
	modalComps    []discordgo.MessageComponent
	updatedComps  []discordgo.MessageComponent
	updated       bool
}

func (c *sectionResponder) Respond(*discordgo.Interaction, string) error { return nil }
func (c *sectionResponder) RespondEphemeral(_ *discordgo.Interaction, s string) error {
	c.ephemeral = s
	return nil
}
func (c *sectionResponder) RespondEmbed(*discordgo.Interaction, *discordgo.MessageEmbed) error {
	return nil
}
func (c *sectionResponder) RespondAutocomplete(*discordgo.Interaction, []*discordgo.ApplicationCommandOptionChoice) error {
	return nil
}
func (c *sectionResponder) RespondEmbedComponents(*discordgo.Interaction, *discordgo.MessageEmbed, []discordgo.MessageComponent) error {
	return nil
}
func (c *sectionResponder) RespondEmbedComponentsEphemeral(*discordgo.Interaction, *discordgo.MessageEmbed, []discordgo.MessageComponent) error {
	return nil
}
func (c *sectionResponder) UpdateMessage(*discordgo.Interaction, *discordgo.MessageEmbed, []discordgo.MessageComponent) error {
	return nil
}
func (c *sectionResponder) RespondComponentsV2Ephemeral(*discordgo.Interaction, []discordgo.MessageComponent) error {
	return nil
}
func (c *sectionResponder) UpdateComponentsV2(_ *discordgo.Interaction, comps []discordgo.MessageComponent) error {
	c.updatedComps, c.updated = comps, true
	return nil
}
func (c *sectionResponder) RespondModal(_ *discordgo.Interaction, customID, _ string, comps []discordgo.MessageComponent) error {
	c.modalCustomID, c.modalComps = customID, comps
	return nil
}

var sid = uuid.New()

func factorButtonInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		Data: discordgo.MessageComponentInteractionData{CustomID: factorCustomID},
	}}
}

func factorModalInteraction(value string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: factorModalCustomID,
			// Pointer components: how discordgo unmarshals a real modal submit
			// (modalTextValue matches on *Label / *TextInput).
			Components: []discordgo.MessageComponent{&discordgo.Label{Component: &discordgo.TextInput{
				CustomID: inFactor, Value: value,
			}}},
		},
	}}
}

// TestFactorSection_RowsAndOwnership renders the current default and claims
// both the button and the modal CustomIDs.
func TestFactorSection_RowsAndOwnership(t *testing.T) {
	sec := newFactorSection(&fakeDefaults{factor: decimal.RequireFromString("12.5")}, testLoc(t))

	rows := sec.Rows(context.Background(), sid)
	require.NotEmpty(t, rows)
	td, ok := rows[0].(discordgo.TextDisplay)
	require.True(t, ok)
	assert.Contains(t, td.Content, "12.5")

	assert.True(t, sec.Owns(factorCustomID))
	assert.True(t, sec.Owns(factorModalCustomID))
	assert.False(t, sec.Owns("settings:forum"))
}

// TestFactorSection_ButtonOpensPrefilledModal opens the one-field modal with
// the current factor prefilled.
func TestFactorSection_ButtonOpensPrefilledModal(t *testing.T) {
	sec := newFactorSection(&fakeDefaults{factor: decimal.RequireFromString("25")}, testLoc(t))
	r := &sectionResponder{}

	require.NoError(t, sec.Handle(context.Background(), r, factorButtonInteraction(), sid, nil))
	assert.Equal(t, factorModalCustomID, r.modalCustomID)
	require.Len(t, r.modalComps, 1)
	in := r.modalComps[0].(discordgo.Label).Component.(discordgo.TextInput)
	assert.Equal(t, "25", in.Value)
}

// TestFactorSection_SubmitPersistsAndRerenders persists a valid value and
// re-renders the panel in place.
func TestFactorSection_SubmitPersistsAndRerenders(t *testing.T) {
	d := &fakeDefaults{}
	sec := newFactorSection(d, testLoc(t))
	r := &sectionResponder{}
	rerender := func() []discordgo.MessageComponent {
		return []discordgo.MessageComponent{discordgo.TextDisplay{Content: "panel"}}
	}

	require.NoError(t, sec.Handle(context.Background(), r, factorModalInteraction("33,33"), sid, rerender))
	assert.Equal(t, 1, d.sets)
	assert.True(t, d.factor.Equal(decimal.RequireFromString("33.33")), "comma-separated input persists, got %s", d.factor)
	assert.True(t, r.updated, "the panel re-renders in place")
	require.Len(t, r.updatedComps, 1)
}

// TestFactorSection_BadValueRejected replies ephemerally without writing and
// leaves the panel untouched.
func TestFactorSection_BadValueRejected(t *testing.T) {
	d := &fakeDefaults{}
	sec := newFactorSection(d, testLoc(t))
	r := &sectionResponder{}

	require.NoError(t, sec.Handle(context.Background(), r, factorModalInteraction("101"), sid, nil))
	assert.Zero(t, d.sets, "no write for out-of-range input")
	assert.False(t, r.updated)
	assert.True(t, strings.Contains(r.ephemeral, "0") && strings.Contains(r.ephemeral, "100"),
		"the error names the accepted range: %q", r.ephemeral)
}
