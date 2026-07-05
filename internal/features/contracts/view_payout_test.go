package contracts_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/features/contracts"
	"github.com/kweezl/spacecraft-corporation/internal/features/contracts/mocks"
)

// allText concatenates every TextDisplay in a Components V2 tree (containers
// and sections included) so tests can assert on rendered copy.
func allText(comps []discordgo.MessageComponent) string {
	var b strings.Builder
	var walk func(cs []discordgo.MessageComponent)
	walk = func(cs []discordgo.MessageComponent) {
		for _, c := range cs {
			switch v := c.(type) {
			case discordgo.TextDisplay:
				b.WriteString(v.Content)
				b.WriteString("\n")
			case discordgo.Container:
				walk(v.Components)
			case discordgo.Section:
				walk(v.Components)
			case discordgo.ActionsRow:
				walk(v.Components)
			}
		}
	}
	walk(comps)
	return b.String()
}

// completedConsoleProgress is a completed contract with a credit reward and a
// positive participant reward factor, as the console loads it.
func completedConsoleProgress(cid uuid.UUID) contracts.Progress {
	credits := decimal.RequireFromString("100")
	return contracts.Progress{
		Contract: contracts.Contract{
			ID: cid, ServerID: gid, ThreadID: "t9", Title: "Steel Run",
			Status: contracts.StatusCompleted, Kind: contracts.KindCustom, LastRefreshedAt: time.Now(),
			RewardCredits:           &credits,
			ParticipantRewardFactor: decimal.RequireFromString("50"),
		},
		Items: []contracts.Item{{ID: uuid.New(), Name: "Steel", RequiredQty: 100, DeliveredQty: 100}},
	}
}

// TestConsole_CompletedShowsPayoutButtons renders Reprint + Mark-paid on a
// completed rewarded contract — and none of the open-contract edit buttons.
func TestConsole_CompletedShowsPayoutButtons(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(completedConsoleProgress(cid), nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:view:"+cid.String()), gid))

	ids := buttonIDs(r.components)
	assert.True(t, has(ids, "contract:payrep:"+cid.String()), "completed + rewarded shows Reprint")
	assert.True(t, has(ids, "contract:paypaid:"+cid.String()), "not yet paid shows Mark-paid")
	assert.False(t, has(ids, "contract:cedit:"+cid.String()), "terminal contracts stay read-only")
	assert.False(t, has(ids, "contract:crepub:"+cid.String()))
}

// TestConsole_PaidHidesMarkButton: once marked, only Reprint remains and the
// facts carry who paid.
func TestConsole_PaidHidesMarkButton(t *testing.T) {
	cid := uuid.New()
	prog := completedConsoleProgress(cid)
	paid := time.Now()
	prog.PayoutsPaidAt = &paid
	prog.PayoutsPaidByUserID = "officer-7"
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(prog, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:view:"+cid.String()), gid))

	ids := buttonIDs(r.components)
	assert.True(t, has(ids, "contract:payrep:"+cid.String()))
	assert.False(t, has(ids, "contract:paypaid:"+cid.String()), "already paid: the button is gone")
	assert.Contains(t, allText(r.components), "<@officer-7>", "the facts name who paid")
}

// TestConsole_NoRewardHidesPayoutRow: a completed contract without a factor (or
// credits) never had a payout, so neither button renders.
func TestConsole_NoRewardHidesPayoutRow(t *testing.T) {
	cid := uuid.New()
	prog := completedConsoleProgress(cid)
	prog.ParticipantRewardFactor = decimal.Decimal{}
	repo := mocks.NewMockRepository(t)
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(prog, nil).Once()

	r := &capture{}
	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:view:"+cid.String()), gid))

	ids := buttonIDs(r.components)
	assert.False(t, has(ids, "contract:payrep:"+cid.String()))
	assert.False(t, has(ids, "contract:paypaid:"+cid.String()))
}

// TestConsole_PayoutReprint enqueues the repost (a reports channel is configured)
// and confirms ephemerally.
func TestConsole_PayoutReprint(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	rc := mocks.NewMockReportsConfig(t)
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return(reportsChan, true).Once()
	repo.EXPECT().RequestPayoutRepost(mock.Anything, gid, cid).Return(nil).Once()

	r := &capture{}
	f := newFeatureReports(t, repo, mocks.NewMockGateway(t), rc)
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:payrep:"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "reprint answers with ephemeral feedback")
}

// TestConsole_PayoutReprintNoChannel declines when no reports channel is set,
// without enqueuing anything.
func TestConsole_PayoutReprintNoChannel(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t) // RequestPayoutRepost must NOT be called
	rc := mocks.NewMockReportsConfig(t)
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return("", false).Once()

	r := &capture{}
	f := newFeatureReports(t, repo, mocks.NewMockGateway(t), rc)
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:payrep:"+cid.String()), gid))
	assert.NotEmpty(t, r.content, "declined with an explanation")
}

// TestConsole_PayoutMarkPaid re-renders the view on the winning press, edits the
// already-posted report in place, and notices the loser.
func TestConsole_PayoutMarkPaid(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	repo.EXPECT().MarkPayoutsPaid(mock.Anything, gid, cid, "officer", mock.Anything).Return(true, nil).Once()
	paidProg := completedConsoleProgress(cid)
	now := time.Now()
	paidProg.PayoutsPaidAt = &now
	paidProg.PayoutsPaidByUserID = "officer"
	paidProg.PayoutReportChannelID = reportsChan
	paidProg.PayoutReportMessageID = "msg-1"
	// Loaded twice: once by editReportAfterPaid, once by the console re-render.
	repo.EXPECT().ProgressByIDScoped(mock.Anything, gid, cid).Return(paidProg, nil).Times(2)
	repo.EXPECT().Payouts(mock.Anything, cid).Return([]contracts.Payout{
		{UserID: "u1", Amount: decimal.RequireFromString("30")},
	}, nil).Once()
	// The public report is edited in place (paid line, Mark-paid dropped).
	gw.EXPECT().EditChannelMessage(reportsChan, "msg-1", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	r := &capture{}
	f := newFeature(t, repo, gw, mocks.NewMockForumConfig(t))
	require.NoError(t, f.Component().Handler(context.Background(), r, component("", member("officer"), "contract:paypaid:"+cid.String()), gid))
	assert.True(t, r.updated, "the winner re-renders the contract view")

	// Second press: the SQL guard reports false → "already paid" notice, no re-render.
	repo2 := mocks.NewMockRepository(t)
	repo2.EXPECT().MarkPayoutsPaid(mock.Anything, gid, cid, "officer", mock.Anything).Return(false, nil).Once()

	r2 := &capture{}
	f2 := newFeature(t, repo2, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	require.NoError(t, f2.Component().Handler(context.Background(), r2, component("", member("officer"), "contract:paypaid:"+cid.String()), gid))
	assert.False(t, r2.updated)
	assert.NotEmpty(t, r2.content, "the loser gets the already-paid notice")
}
