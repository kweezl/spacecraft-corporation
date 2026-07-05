package contracts_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
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
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/outbox"
)

const guildSnowflake = "guild-1"

// reportsChan is the configured reports channel the payout task posts to in tests.
const reportsChan = "reports-9"

// pricedGDID is some compiled-in gamedata item with a positive price — which
// one is irrelevant: with a single item the value cancels out of the share
// (delivered/required), the tests only need "priced vs priceless".
var pricedGDID = func() string {
	for _, it := range testRegistry.Latest().Items() {
		if it.Price > 0 {
			return string(it.ID)
		}
	}
	panic("no priced item in the compiled-in gamedata")
}()

func payoutTask(t *testing.T, cid uuid.UUID, repost bool) outbox.Task {
	t.Helper()
	b, err := json.Marshal(map[string]any{"contract_id": cid, "repost": repost})
	require.NoError(t, err)
	return outbox.Task{ID: uuid.New(), Kind: "contracts.reward.payout", Version: 1, Payload: b}
}

// completedPayoutProgress is a completed 100-credit, 50%-factor contract with
// one fully delivered priced item: u1 delivered 60, u2 delivered 40.
func completedPayoutProgress(cid uuid.UUID, threadID string) contracts.Progress {
	credits := decimal.RequireFromString("100")
	return contracts.Progress{
		Contract: contracts.Contract{
			ID: cid, ServerID: gid, ServerDiscordID: guildSnowflake, ThreadID: threadID,
			Title: "Steel Run", Status: contracts.StatusCompleted, PostVersion: contracts.CurrentPostVersion,
			RewardCredits:           &credits,
			ParticipantRewardFactor: decimal.RequireFromString("50"),
		},
		Items: []contracts.Item{{
			ID: uuid.New(), Name: "Steel", GDID: pricedGDID, RequiredQty: 100,
			DeliveredQty: 100, ReservedQty: 100,
			Participants: []contracts.Participant{
				{UserID: "u1", Reserved: 60, Delivered: 60},
				{UserID: "u2", Reserved: 40, Delivered: 40},
			},
		}},
	}
}

func TestTaskPayout_ComputesSavesAndPosts(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	rc := mocks.NewMockReportsConfig(t)

	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(completedPayoutProgress(cid, "thread-9"), nil).Once()
	repo.EXPECT().Payouts(mock.Anything, cid).Return(nil, nil).Once()
	// Display names snapshot: u1 resolves, u2 left the server → raw id fallback.
	gw.EXPECT().MemberDisplayName(guildSnowflake, "u1").Return("Alice", true).Once()
	gw.EXPECT().MemberDisplayName(guildSnowflake, "u2").Return("", false).Once()
	repo.EXPECT().SavePayouts(mock.Anything, cid, mock.MatchedBy(func(rows []contracts.Payout) bool {
		return len(rows) == 2 &&
			rows[0].UserID == "u1" && rows[0].UserName == "Alice" && rows[0].Amount.Equal(decimal.RequireFromString("30")) &&
			rows[1].UserID == "u2" && rows[1].UserName == "u2" && rows[1].Amount.Equal(decimal.RequireFromString("20"))
	})).Return(nil).Once()
	// A fresh report (no stored message id) posts to the configured reports channel.
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return(reportsChan, true).Once()
	gw.EXPECT().PostChannelMessage(reportsChan,
		mock.MatchedBy(func(content string) bool {
			// Pool + factor in the header, one line per participant with amounts.
			return strings.Contains(content, "50.00") && strings.Contains(content, "50%") &&
				strings.Contains(content, "<@u1>") && strings.Contains(content, "30.00") &&
				strings.Contains(content, "<@u2>") && strings.Contains(content, "20.00")
		}),
		[]string{"u1", "u2"},
		mock.MatchedBy(func(files []*discordgo.File) bool {
			if len(files) != 1 || files[0].Name != "payout.csv" {
				return false
			}
			raw, err := io.ReadAll(files[0].Reader)
			if err != nil || !bytes.HasPrefix(raw, []byte("\uFEFF")) {
				return false
			}
			recs, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\uFEFF")))).ReadAll()
			// Header + two participants. Columns: contract id, contract title,
			// participant, user id, per-item qty, share, amount.
			return err == nil && len(recs) == 3 &&
				recs[1][0] == cid.String() && recs[1][1] == "Steel Run" &&
				recs[1][2] == "Alice" && recs[1][4] == "60" && recs[1][6] == "30.00" &&
				recs[2][2] == "u2" && recs[2][4] == "40" && recs[2][6] == "20.00"
		}),
		mock.MatchedBy(func(comps []discordgo.MessageComponent) bool {
			return len(buttonIDs(comps)) == 2 // View + Mark-paid
		}),
	).Return("msg-1", nil).Once()
	repo.EXPECT().MarkPayoutPosted(mock.Anything, cid, reportsChan, "msg-1", mock.Anything).Return(nil).Once()

	f := newFeatureReports(t, repo, gw, rc)
	require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false)))
}

func TestTaskPayout_AlreadyPostedIsNoop(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)

	prog := completedPayoutProgress(cid, "thread-9")
	posted := time.Now()
	prog.PayoutPostedAt = &posted
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
	// No Payouts read, no save, no gateway call: the latch stops everything.

	f := newFeature(t, repo, gw, mocks.NewMockForumConfig(t))
	require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false)))
}

// TestTaskPayout_RepostEditsInPlace covers the Reprint path when a report was
// already posted: the stored message is edited in place (no duplicate), from the
// persisted rows.
func TestTaskPayout_RepostEditsInPlace(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	rc := mocks.NewMockReportsConfig(t)

	prog := completedPayoutProgress(cid, "thread-9")
	posted := time.Now()
	prog.PayoutPostedAt = &posted
	prog.PayoutReportChannelID = reportsChan
	prog.PayoutReportMessageID = "msg-1"
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
	// Stored rows only — no recompute, no name lookups, no SavePayouts.
	repo.EXPECT().Payouts(mock.Anything, cid).Return([]contracts.Payout{
		{UserID: "u1", UserName: "Alice", Amount: decimal.RequireFromString("30"), SharePercent: decimal.RequireFromString("60")},
		{UserID: "u2", UserName: "u2", Amount: decimal.RequireFromString("20"), SharePercent: decimal.RequireFromString("40")},
	}, nil).Once()
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return(reportsChan, true).Once()
	gw.EXPECT().EditChannelMessage(reportsChan, "msg-1", mock.MatchedBy(func(content string) bool {
		return strings.Contains(content, "<@u1>") && strings.Contains(content, "30.00")
	}), mock.MatchedBy(func(files []*discordgo.File) bool {
		// The CSV is re-attached on Reprint so a language change refreshes it.
		return len(files) == 1 && files[0].Name == "payout.csv"
	}), mock.Anything).Return(nil).Once()
	repo.EXPECT().MarkPayoutPosted(mock.Anything, cid, reportsChan, "msg-1", mock.Anything).Return(nil).Once()

	f := newFeatureReports(t, repo, gw, rc)
	require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, true)))
}

func TestTaskPayout_AllPricelessPostsExplanation(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	rc := mocks.NewMockReportsConfig(t)

	prog := completedPayoutProgress(cid, "thread-9")
	prog.Items[0].GDID = "" // legacy free-text item: no gamedata value
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
	repo.EXPECT().Payouts(mock.Anything, cid).Return(nil, nil).Once()
	gw.EXPECT().MemberDisplayName(guildSnowflake, "u1").Return("Alice", true).Once()
	gw.EXPECT().MemberDisplayName(guildSnowflake, "u2").Return("Bob", true).Once()
	// Zero-amount rows still persist (the compute latch), and the message
	// explains instead of listing zero payouts; nobody is pinged.
	repo.EXPECT().SavePayouts(mock.Anything, cid, mock.MatchedBy(func(rows []contracts.Payout) bool {
		return len(rows) == 2 && rows[0].Amount.IsZero() && rows[1].Amount.IsZero()
	})).Return(nil).Once()
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return(reportsChan, true).Once()
	gw.EXPECT().PostChannelMessage(reportsChan, mock.MatchedBy(func(content string) bool {
		return strings.Contains(content, "could not be split") && !strings.Contains(content, "<@u1>")
	}), []string(nil), mock.Anything, mock.Anything).Return("msg-1", nil).Once()
	repo.EXPECT().MarkPayoutPosted(mock.Anything, cid, reportsChan, "msg-1", mock.Anything).Return(nil).Once()

	f := newFeatureReports(t, repo, gw, rc)
	require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false)))
}

// TestTaskPayout_CSVLocalizedHeaders covers Update C: the user-id header is
// translated and each item column header uses the gamedata localized name (not
// the raw stored snapshot), in the server's language.
func TestTaskPayout_CSVLocalizedHeaders(t *testing.T) {
	catName := func(gdid string, lang i18n.Language) string {
		return testRegistry.Latest().Name(gamedata.GDID(gdid), lang)
	}

	readCSV := func(t *testing.T, lang i18n.Language, mutate func(*contracts.Progress)) [][]string {
		t.Helper()
		cid := uuid.New()
		repo := mocks.NewMockRepository(t)
		gw := mocks.NewMockGateway(t)
		rc := mocks.NewMockReportsConfig(t)
		prog := completedPayoutProgress(cid, "thread-9")
		if mutate != nil {
			mutate(&prog)
		}
		repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
		repo.EXPECT().Payouts(mock.Anything, cid).Return(nil, nil).Once()
		gw.EXPECT().MemberDisplayName(mock.Anything, mock.Anything).Return("", false).Maybe()
		repo.EXPECT().SavePayouts(mock.Anything, cid, mock.Anything).Return(nil).Once()
		rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return(reportsChan, true).Once()
		var records [][]string
		gw.EXPECT().PostChannelMessage(reportsChan, mock.Anything, mock.Anything,
			mock.MatchedBy(func(files []*discordgo.File) bool {
				raw, err := io.ReadAll(files[0].Reader)
				require.NoError(t, err)
				recs, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\uFEFF")))).ReadAll()
				require.NoError(t, err)
				records = recs
				return true
			}), mock.Anything).Return("msg-1", nil).Once()
		repo.EXPECT().MarkPayoutPosted(mock.Anything, cid, reportsChan, "msg-1", mock.Anything).Return(nil).Once()

		f := newFeatureDeps(t, repo, gw, mocks.NewMockForumConfig(t), featureDeps{lang: lang, reports: rc})
		require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false)))
		return records
	}

	t.Run("english localized headers", func(t *testing.T) {
		recs := readCSV(t, i18n.LanguageEN, nil)
		require.NotEmpty(t, recs)
		assert.Equal(t, "Contract ID", recs[0][0])
		assert.Equal(t, "Contract", recs[0][1])
		assert.Equal(t, "Discord User ID", recs[0][3])
		require.NotEmpty(t, catName(pricedGDID, i18n.LanguageEN))
		assert.Equal(t, catName(pricedGDID, i18n.LanguageEN), recs[0][4], "item header is the localized catalog name, not the stored snapshot")
		assert.Equal(t, "Steel Run", recs[1][1], "each row carries the contract title for cross-contract merges")
	})

	t.Run("russian localized headers", func(t *testing.T) {
		recs := readCSV(t, i18n.LanguageRU, nil)
		require.NotEmpty(t, recs)
		assert.Equal(t, "ID Контракта", recs[0][0])
		assert.Equal(t, "Контракт", recs[0][1])
		assert.Equal(t, "Discord ID Участника", recs[0][3])
		require.NotEmpty(t, catName(pricedGDID, i18n.LanguageRU))
		assert.Equal(t, catName(pricedGDID, i18n.LanguageRU), recs[0][4])
	})

	t.Run("free-text item keeps its raw name", func(t *testing.T) {
		recs := readCSV(t, i18n.LanguageEN, func(p *contracts.Progress) { p.Items[0].GDID = "" })
		require.NotEmpty(t, recs)
		assert.Equal(t, "Steel", recs[0][4], "a free-text item has no gdid, so the header stays the stored name")
	})
}

// TestTaskPayout_CSVQuotesAndEscapes: every value is wrapped in double quotes and
// embedded quotes are doubled, so a contract title (or item name) containing a
// comma or quote can't break the row structure and round-trips through a reader.
func TestTaskPayout_CSVQuotesAndEscapes(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	rc := mocks.NewMockReportsConfig(t)
	prog := completedPayoutProgress(cid, "thread-9")
	prog.Title = `Steel, "Premium"` // comma + embedded quotes
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()
	repo.EXPECT().Payouts(mock.Anything, cid).Return(nil, nil).Once()
	gw.EXPECT().MemberDisplayName(mock.Anything, mock.Anything).Return("", false).Maybe()
	repo.EXPECT().SavePayouts(mock.Anything, cid, mock.Anything).Return(nil).Once()
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return(reportsChan, true).Once()
	var raw []byte
	gw.EXPECT().PostChannelMessage(reportsChan, mock.Anything, mock.Anything, mock.MatchedBy(func(files []*discordgo.File) bool {
		b, err := io.ReadAll(files[0].Reader)
		require.NoError(t, err)
		raw = b
		return true
	}), mock.Anything).Return("msg-1", nil).Once()
	repo.EXPECT().MarkPayoutPosted(mock.Anything, cid, reportsChan, "msg-1", mock.Anything).Return(nil).Once()

	f := newFeatureReports(t, repo, gw, rc)
	require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false)))

	body := bytes.TrimPrefix(raw, []byte("\uFEFF"))
	assert.Contains(t, string(body), `"Steel, ""Premium"""`, "the title with a comma and quotes is force-quoted and quote-doubled")
	recs, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(recs), 2)
	assert.Equal(t, `Steel, "Premium"`, recs[1][1], "the parsed title matches the original despite the special chars")
}

func TestTaskPayout_NotCompletedIsPermanent(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)

	prog := completedPayoutProgress(cid, "thread-9")
	prog.Status = contracts.StatusOpen
	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(prog, nil).Once()

	f := newFeature(t, repo, mocks.NewMockGateway(t), mocks.NewMockForumConfig(t))
	err := handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false))
	require.Error(t, err, "a payout task for a non-completed contract is a bug, not a retry")
}

// TestTaskPayout_NoReportsChannelComputesButSkips: with no reports channel set,
// the payouts still compute and persist, but posting is skipped (a warning logs)
// and — critically — nothing is latched, so a later Reprint (once a channel is
// configured) still delivers the report.
func TestTaskPayout_NoReportsChannelComputesButSkips(t *testing.T) {
	cid := uuid.New()
	repo := mocks.NewMockRepository(t)
	gw := mocks.NewMockGateway(t)
	rc := mocks.NewMockReportsConfig(t)

	repo.EXPECT().ProgressByID(mock.Anything, cid).Return(completedPayoutProgress(cid, "thread-9"), nil).Once()
	repo.EXPECT().Payouts(mock.Anything, cid).Return(nil, nil).Once()
	gw.EXPECT().MemberDisplayName(guildSnowflake, "u1").Return("Alice", true).Once()
	gw.EXPECT().MemberDisplayName(guildSnowflake, "u2").Return("Bob", true).Once()
	repo.EXPECT().SavePayouts(mock.Anything, cid, mock.Anything).Return(nil).Once()
	rc.EXPECT().ContractsReportsChannelID(mock.Anything, gid).Return("", false).Once()
	// No PostChannelMessage / EditChannelMessage and no posted-at latch.

	f := newFeatureReports(t, repo, gw, rc)
	require.NoError(t, handlerFor(t, f, "contracts.reward.payout")(context.Background(), payoutTask(t, cid, false)))
}
