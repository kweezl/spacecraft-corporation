package contracts_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/emoji"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
)

// component builds a message-component interaction (the public-post button click).
func component(channelID string, m *discordgo.Member, customID string, values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: "g", ChannelID: channelID, Member: m,
		Data: discordgo.MessageComponentInteractionData{CustomID: customID, Values: values},
	}}
}

// modalSubmit builds a V2 modal-submit interaction: the item select and the
// quantity input each come back wrapped in a *Label (pointers, as discordgo
// unmarshals them), matching how the handler reads them.
func modalSubmit(channelID string, m *discordgo.Member, customID, item, qty string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: channelID, Member: m,
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: customID,
			Components: []discordgo.MessageComponent{
				&discordgo.Label{Component: &discordgo.SelectMenu{CustomID: "item", Values: []string{item}}},
				&discordgo.Label{Component: &discordgo.TextInput{CustomID: "qty", Value: qty}},
			},
		},
	}}
}

// modalSubmitNoSelect builds a modal submit whose item select carries no value —
// the case where a client submits the pre-selected (Default:true) single option
// without echoing it back. The quantity input is still present.
func modalSubmitNoSelect(channelID string, m *discordgo.Member, customID, qty string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: "g", ChannelID: channelID, Member: m,
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: customID,
			Components: []discordgo.MessageComponent{
				&discordgo.Label{Component: &discordgo.SelectMenu{CustomID: "item"}},
				&discordgo.Label{Component: &discordgo.TextInput{CustomID: "qty", Value: qty}},
			},
		},
	}}
}

func dispatch(t *testing.T, f *contracts.Feature, r *capture, i *discordgo.InteractionCreate) {
	t.Helper()
	require.NoError(t, f.Component().Handler(context.Background(), r, i, gid))
}

// sentModalSelect returns the item select the handler put in the modal it opened,
// failing if the modal carries none.
func sentModalSelect(t *testing.T, comps []discordgo.MessageComponent) discordgo.SelectMenu {
	t.Helper()
	for _, c := range comps {
		if l, ok := c.(discordgo.Label); ok {
			if s, ok := l.Component.(discordgo.SelectMenu); ok {
				return s
			}
		}
	}
	t.Fatalf("no item select in modal %#v", comps)
	return discordgo.SelectMenu{}
}

// sentModalQty returns the pre-filled value of the quantity input in the modal the
// handler opened (empty when not pre-filled).
func sentModalQty(comps []discordgo.MessageComponent) string {
	for _, c := range comps {
		if l, ok := c.(discordgo.Label); ok {
			if ti, ok := l.Component.(discordgo.TextInput); ok {
				return ti.Value
			}
		}
	}
	return ""
}

// defaulted reports whether exactly the options marked Default are pre-selected.
func defaultedCount(opts []discordgo.SelectMenuOption) int {
	n := 0
	for _, o := range opts {
		if o.Default {
			n++
		}
	}
	return n
}

// TestPanel_ParticipateSingleItem_OpensModal covers the shortest path: with one
// needable item the button opens one modal (a centered overlay, no ephemeral
// message to scroll to) whose only option is pre-selected and whose quantity is
// pre-filled with the live remaining, so submitting reserves that amount.
func TestPanel_ParticipateSingleItem_OpensModal(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}
	m := member("u1")

	prog := contracts.Progress{
		Contract: contracts.Contract{ServerID: gid, ThreadID: thread, Status: contracts.StatusOpen},
		Items:    []contracts.Item{{Name: "Steel", RequiredQty: 100}},
	}
	repo.EXPECT().Progress(mock.Anything, gid, thread).Return(prog, nil).Once()
	dispatch(t, f, r, component(thread, m, "contract:panel:participate"))
	require.Empty(t, r.components, "single item should not post an ephemeral panel")
	assert.Equal(t, "contract:qty:participate", r.modalCustomID)
	sel := sentModalSelect(t, r.modalComponents)
	require.Len(t, sel.Options, 1)
	assert.Equal(t, 1, defaultedCount(sel.Options), "the only item is pre-selected")
	assert.Equal(t, "100", sentModalQty(r.modalComponents), "quantity pre-filled with the live remaining")

	// Submit the defaults (Steel, 100) -> repo.Participate with the item and invoker.
	repo.EXPECT().Participate(mock.Anything, gid, thread, "Steel", "u1", 100).Return(nil).Once()
	dispatch(t, f, r, modalSubmit(thread, m, r.modalCustomID, "Steel", "100"))
	assert.Contains(t, r.content, "Steel")
}

// TestPanel_ParticipateMultiItem_Modal walks the multi-item path: the button opens
// one modal with a select over every needable item (none pre-selected) and an
// empty quantity; submitting the picked item + amount lands on the same repository
// method the slash leaf uses.
func TestPanel_ParticipateMultiItem_Modal(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}
	m := member("u1")

	prog := contracts.Progress{
		Contract: contracts.Contract{ServerID: gid, ThreadID: thread, Status: contracts.StatusOpen},
		Items:    []contracts.Item{{Name: "Steel", RequiredQty: 100}, {Name: "Iron", RequiredQty: 50}},
	}
	repo.EXPECT().Progress(mock.Anything, gid, thread).Return(prog, nil).Once()
	dispatch(t, f, r, component(thread, m, "contract:panel:participate"))
	assert.Equal(t, "contract:qty:participate", r.modalCustomID)
	sel := sentModalSelect(t, r.modalComponents)
	require.Len(t, sel.Options, 2)
	assert.Equal(t, 0, defaultedCount(sel.Options), "with several items none is pre-selected")
	assert.Empty(t, sentModalQty(r.modalComponents), "quantity left blank when the item is unknown")

	// Submit Steel x50 -> repo.Participate with the picked item and invoker.
	repo.EXPECT().Participate(mock.Anything, gid, thread, "Steel", "u1", 50).Return(nil).Once()
	dispatch(t, f, r, modalSubmit(thread, m, r.modalCustomID, "Steel", "50"))
	assert.Contains(t, r.content, "Steel")
}

// TestPanel_ModalOptionIcons covers Update A: a gamedata-linked item's modal
// option carries its catalog icon emoji (resolved from the stamped version), while
// a free-text item renders plain. "Actuator" is a real catalog item whose icon
// name the test emoji store carries.
func TestPanel_ModalOptionIcons(t *testing.T) {
	emo := emoji.StaticStore(map[string]string{"Actuator": "<:Actuator:1234567890>"})

	t.Run("participate options show gdid icons, free-text plain", func(t *testing.T) {
		repo := mocks.NewMockRepository(t)
		f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{emo: emo})
		r := &capture{}
		m := member("u1")
		prog := contracts.Progress{
			Contract: contracts.Contract{ServerID: gid, ThreadID: thread, Status: contracts.StatusOpen},
			Items: []contracts.Item{
				{Name: "Steel", GDID: "Actuator", RequiredQty: 100},
				{Name: "Mystery Ore", RequiredQty: 50}, // free-text, no gdid
			},
		}
		repo.EXPECT().Progress(mock.Anything, gid, thread).Return(prog, nil).Once()
		dispatch(t, f, r, component(thread, m, "contract:panel:participate"))
		opts := sentModalSelect(t, r.modalComponents).Options
		require.Len(t, opts, 2)
		require.NotNil(t, opts[0].Emoji, "the gamedata item's option carries its catalog icon")
		assert.Equal(t, "Actuator", opts[0].Emoji.Name)
		assert.Equal(t, "1234567890", opts[0].Emoji.ID)
		assert.Nil(t, opts[1].Emoji, "a free-text item's option renders plain")
	})

	t.Run("deliver options carry the gdid icon from MemberOutstanding", func(t *testing.T) {
		repo := mocks.NewMockRepository(t)
		f := newFeatureDeps(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t), featureDeps{emo: emo})
		r := &capture{}
		m := member("u1")
		out := []contracts.MemberItem{{Name: "Steel", GDID: "Actuator", Reserved: 10}}
		repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(out, nil).Once()
		dispatch(t, f, r, component(thread, m, "contract:panel:deliver"))
		opts := sentModalSelect(t, r.modalComponents).Options
		require.Len(t, opts, 1)
		require.NotNil(t, opts[0].Emoji, "the reserved item's icon rides through MemberOutstanding")
		assert.Equal(t, "Actuator", opts[0].Emoji.Name)
	})
}

// TestPanel_DeliverSingleItem_OpensModal covers the deliver path: with one
// outstanding reservation the modal opens pre-filled with the member's outstanding
// qty; an edited submit delivers that amount against the member's own reservation.
func TestPanel_DeliverSingleItem_OpensModal(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}
	m := member("u1")

	out := []contracts.MemberItem{{Name: "Steel", Reserved: 10}}
	repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(out, nil).Once()
	dispatch(t, f, r, component(thread, m, "contract:panel:deliver"))
	assert.Equal(t, "contract:qty:deliver", r.modalCustomID)
	assert.Equal(t, "10", sentModalQty(r.modalComponents), "pre-filled with the outstanding qty")

	repo.EXPECT().Deliver(mock.Anything, gid, thread, "Steel", "u1", 5).Return(false, nil).Once()
	dispatch(t, f, r, modalSubmit(thread, m, r.modalCustomID, "Steel", "5"))
	assert.Contains(t, r.content, "Steel")
}

// TestPanel_DeliverMultiItem_NoPrefill covers the multi-reservation deliver path:
// because a static modal's quantity field cannot follow the picked select, nothing
// is pre-selected and the amount is left blank — so a stale amount can't ride from
// one item onto another (the iron-10 / copper-5 footgun). An untouched (empty)
// select is then ambiguous and asks the member to pick again, mutating nothing.
func TestPanel_DeliverMultiItem_NoPrefill(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}
	m := member("u1")

	out := []contracts.MemberItem{{Name: "Iron", Reserved: 10}, {Name: "Copper", Reserved: 5}}
	repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(out, nil).Once()
	dispatch(t, f, r, component(thread, m, "contract:panel:deliver"))
	sel := sentModalSelect(t, r.modalComponents)
	require.Len(t, sel.Options, 2)
	assert.Equal(t, 0, defaultedCount(sel.Options), "with several reservations none is pre-selected")
	assert.Empty(t, sentModalQty(r.modalComponents), "quantity left blank so it can't carry across items")

	// An empty/untouched select is ambiguous with several items -> ask again, no mutation.
	repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(out, nil).Once()
	dispatch(t, f, r, modalSubmitNoSelect(thread, m, "contract:qty:deliver", "10"))
	assert.NotEmpty(t, r.content, "ambiguous empty select asks the member to pick again")
}

// TestPanel_ReleaseSingleItem_OpensModal covers the release path: with one
// outstanding reservation the modal pre-selects it and pre-fills the quantity with
// reserved-minus-delivered (the releasable max, same basis as deliver); submitting
// releases that amount against the member's own pledge (target == actor == invoker).
func TestPanel_ReleaseSingleItem_OpensModal(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}
	m := member("u1")

	// Reserved 10, delivered 4 -> outstanding 6 is the releasable max.
	out := []contracts.MemberItem{{Name: "Steel", Reserved: 10, Delivered: 4}}
	repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(out, nil).Once()
	dispatch(t, f, r, component(thread, m, "contract:panel:release"))
	assert.Equal(t, "contract:qty:release", r.modalCustomID)
	sel := sentModalSelect(t, r.modalComponents)
	require.Len(t, sel.Options, 1)
	assert.Equal(t, 1, defaultedCount(sel.Options), "the only reservation is pre-selected")
	assert.Equal(t, "6", sentModalQty(r.modalComponents), "pre-filled with reserved minus delivered")

	// Submit (Steel, 6) -> Release with target and actor both the invoker.
	repo.EXPECT().Release(mock.Anything, gid, thread, "Steel", "u1", 6, "u1").Return(nil).Once()
	dispatch(t, f, r, modalSubmit(thread, m, r.modalCustomID, "Steel", "6"))
	assert.Contains(t, r.content, "Steel")
}

// TestPanel_ReleaseNothingReserved shows the per-user scoping: a member with no
// outstanding reservation gets a "nothing to release" reply and no modal.
func TestPanel_ReleaseNothingReserved(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}

	repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(nil, nil).Once()
	dispatch(t, f, r, component(thread, member("u1"), "contract:panel:release"))

	assert.Empty(t, r.modalCustomID, "no modal when nothing is reserved")
	assert.NotEmpty(t, r.content, "should explain there is nothing to release")
}

// TestPanel_DeliverNothingOutstanding shows the per-user scoping: a member with no
// outstanding reservation gets a "nothing to deliver" reply and no modal.
func TestPanel_DeliverNothingOutstanding(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}

	repo.EXPECT().MemberOutstanding(mock.Anything, gid, thread, "u1").Return(nil, nil).Once()
	dispatch(t, f, r, component(thread, member("u1"), "contract:panel:deliver"))

	assert.Empty(t, r.modalCustomID, "no modal when nothing is outstanding")
	assert.NotEmpty(t, r.content, "should explain there is nothing to deliver")
}

// TestPanel_ParticipateSingleItem_EmptySelectFallback covers the submit-and-go
// path when the client does NOT echo the pre-selected default back: the modal
// submit carries no select value, so the handler re-derives the eligible set and,
// finding exactly one needable item, participates against it anyway (no "expired").
func TestPanel_ParticipateSingleItem_EmptySelectFallback(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}
	m := member("u1")

	prog := contracts.Progress{
		Contract: contracts.Contract{ServerID: gid, ThreadID: thread, Status: contracts.StatusOpen},
		Items:    []contracts.Item{{Name: "Steel", RequiredQty: 100}},
	}
	// The submit re-reads progress to resolve the lone eligible item.
	repo.EXPECT().Progress(mock.Anything, gid, thread).Return(prog, nil).Once()
	repo.EXPECT().Participate(mock.Anything, gid, thread, "Steel", "u1", 100).Return(nil).Once()
	dispatch(t, f, r, modalSubmitNoSelect(thread, m, "contract:qty:participate", "100"))
	assert.Contains(t, r.content, "Steel", "single item resolved despite the empty select")
}

// TestPanel_ParticipateMultiItem_EmptySelectExpired shows the fallback does not
// guess: with several needable items an empty select is ambiguous, so the member
// is asked to pick again and nothing is mutated (no Participate expectation).
func TestPanel_ParticipateMultiItem_EmptySelectExpired(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}

	prog := contracts.Progress{
		Contract: contracts.Contract{ServerID: gid, ThreadID: thread, Status: contracts.StatusOpen},
		Items:    []contracts.Item{{Name: "Steel", RequiredQty: 100}, {Name: "Iron", RequiredQty: 50}},
	}
	repo.EXPECT().Progress(mock.Anything, gid, thread).Return(prog, nil).Once()
	dispatch(t, f, r, modalSubmitNoSelect(thread, member("u1"), "contract:qty:participate", "10"))
	assert.NotEmpty(t, r.content, "ambiguous empty select asks the member to pick again")
}

// TestPanel_ModalBadQty rejects a non-numeric quantity without mutating: the mock
// repository has no Participate expectation, so a stray call would fail the test.
func TestPanel_ModalBadQty(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}

	dispatch(t, f, r, modalSubmit(thread, member("u1"), "contract:qty:participate", "Steel", "lots"))
	assert.NotEmpty(t, r.content, "a bad quantity is reported, not acted on")
}

// TestPanel_ModalBadOp rejects a modal id with an unknown op (a malformed/forged
// submit) as an internal error rather than acting on it.
func TestPanel_ModalBadOp(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	r := &capture{}

	err := f.Component().Handler(context.Background(), r, modalSubmit(thread, member("u1"), "contract:qty:bogus", "Steel", "5"), gid)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "bad modal id"))
}
