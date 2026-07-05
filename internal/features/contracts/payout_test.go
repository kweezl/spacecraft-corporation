package contracts

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payoutOf indexes a result's shares by user id.
func payoutOf(t *testing.T, res payoutResult, userID string) Payout {
	t.Helper()
	for _, s := range res.Shares {
		if s.UserID == userID {
			return s
		}
	}
	t.Fatalf("no payout for %s", userID)
	return Payout{}
}

// sumShares asserts the invariant Σ amounts + remainder == pool.
func sumShares(t *testing.T, res payoutResult) {
	t.Helper()
	sum := decimal.Decimal{}
	for _, s := range res.Shares {
		sum = sum.Add(s.Amount)
	}
	assert.Truef(t, sum.Add(res.Remainder).Equal(res.Pool),
		"Σ amounts (%s) + remainder (%s) must equal pool (%s)", sum, res.Remainder, res.Pool)
}

func TestComputePayout(t *testing.T) {
	item := func(name, unit string, required int, parts ...Participant) payoutItem {
		return payoutItem{Name: name, UnitValue: dec(unit), RequiredQty: required, Delivered: parts}
	}
	p := func(user string, delivered int) Participant { return Participant{UserID: user, Delivered: delivered} }

	t.Run("single participant takes the full pool", func(t *testing.T) {
		res := computePayout(dec("100"), dec("50"), []payoutItem{
			item("Iron Ore", "1", 1000, p("alice", 1000)),
		})
		assert.True(t, res.Pool.Equal(dec("50")), "pool = 100 × 50%%, got %s", res.Pool)
		require.Len(t, res.Shares, 1)
		assert.True(t, payoutOf(t, res, "alice").Amount.Equal(dec("50")))
		assert.True(t, payoutOf(t, res, "alice").SharePercent.Equal(dec("100")))
		assert.True(t, res.Remainder.IsZero())
		assert.False(t, res.ZeroValue)
		assert.Empty(t, res.Priceless)
		sumShares(t, res)
	})

	t.Run("three-way even split leaves truncation dust", func(t *testing.T) {
		res := computePayout(dec("100"), dec("100"), []payoutItem{
			item("Ore", "1", 3, p("a", 1), p("b", 1), p("c", 1)),
		})
		for _, u := range []string{"a", "b", "c"} {
			assert.Truef(t, payoutOf(t, res, u).Amount.Equal(dec("33.33")), "%s got %s", u, payoutOf(t, res, u).Amount)
		}
		assert.True(t, res.Remainder.Equal(dec("0.01")), "got %s", res.Remainder)
		sumShares(t, res)
	})

	// The reference case from the feature spec: reward 100, factor 50 → pool 50.
	// Items iron ore (1000 × 1) + solar panel (100 × 100) → total value 11000. A
	// participant delivering 500 ore + 10 panels contributed 1500 → 6.81 (the
	// exact 6.8181… truncates, never rounds up).
	t.Run("multi-participant multi-item with differing costs", func(t *testing.T) {
		res := computePayout(dec("100"), dec("50"), []payoutItem{
			item("Iron Ore", "1", 1000, p("mixed", 500), p("orefan", 500)),
			item("Solar Panel", "100", 100, p("mixed", 10), p("panelpro", 90)),
		})
		assert.True(t, res.Pool.Equal(dec("50")))
		// mixed: 500×1 + 10×100 = 1500/11000 → 50 × 3/22 = 6.8181… → 6.81
		assert.True(t, payoutOf(t, res, "mixed").Amount.Equal(dec("6.81")), "got %s", payoutOf(t, res, "mixed").Amount)
		// orefan: 500/11000 → 2.2727… → 2.27
		assert.True(t, payoutOf(t, res, "orefan").Amount.Equal(dec("2.27")), "got %s", payoutOf(t, res, "orefan").Amount)
		// panelpro: 9000/11000 → 40.909… → 40.90
		assert.True(t, payoutOf(t, res, "panelpro").Amount.Equal(dec("40.90")), "got %s", payoutOf(t, res, "panelpro").Amount)
		assert.True(t, res.Remainder.Equal(dec("0.02")), "got %s", res.Remainder)
		// Share percentages mirror the value weights.
		assert.True(t, payoutOf(t, res, "panelpro").SharePercent.Equal(dec("81.818182")), "got %s", payoutOf(t, res, "panelpro").SharePercent)
		sumShares(t, res)
	})

	t.Run("participant delivering only one of several items", func(t *testing.T) {
		res := computePayout(dec("1000"), dec("10"), []payoutItem{
			item("Ore", "2", 100, p("a", 100)),
			item("Panel", "8", 100, p("b", 100)),
		})
		// den = 200 + 800 = 1000; pool = 100. a: 200 → 20, b: 800 → 80.
		assert.True(t, payoutOf(t, res, "a").Amount.Equal(dec("20")))
		assert.True(t, payoutOf(t, res, "b").Amount.Equal(dec("80")))
		assert.True(t, res.Remainder.IsZero())
		sumShares(t, res)
	})

	t.Run("factor extremes", func(t *testing.T) {
		full := computePayout(dec("77.77"), dec("100"), []payoutItem{item("Ore", "1", 1, p("a", 1))})
		assert.True(t, full.Pool.Equal(dec("77.77")))
		assert.True(t, payoutOf(t, full, "a").Amount.Equal(dec("77.77")))

		tiny := computePayout(dec("100"), dec("0.01"), []payoutItem{item("Ore", "1", 1, p("a", 1))})
		assert.True(t, tiny.Pool.Equal(dec("0.01")))
		assert.True(t, payoutOf(t, tiny, "a").Amount.Equal(dec("0.01")))
		sumShares(t, tiny)
	})

	t.Run("mixed priced and priceless items", func(t *testing.T) {
		res := computePayout(dec("100"), dec("50"), []payoutItem{
			item("Ore", "1", 100, p("a", 100)),
			item("Mystery Box", "0", 10, p("b", 10)), // free-text / unpriced
		})
		assert.False(t, res.ZeroValue)
		assert.Equal(t, []string{"Mystery Box"}, res.Priceless)
		// a takes the whole pool; b delivered only priceless goods → 0, but still listed.
		assert.True(t, payoutOf(t, res, "a").Amount.Equal(dec("50")))
		assert.True(t, payoutOf(t, res, "b").Amount.IsZero())
		assert.True(t, payoutOf(t, res, "b").SharePercent.IsZero())
		sumShares(t, res)
	})

	t.Run("all items priceless flags ZeroValue", func(t *testing.T) {
		res := computePayout(dec("100"), dec("50"), []payoutItem{
			item("Mystery", "0", 10, p("a", 4), p("b", 6)),
		})
		assert.True(t, res.ZeroValue)
		require.Len(t, res.Shares, 2, "deliverers still get (zero) rows so retries know compute happened")
		assert.True(t, payoutOf(t, res, "a").Amount.IsZero())
		assert.True(t, res.Remainder.Equal(res.Pool), "nothing distributes")
	})

	t.Run("billion-credit contracts stay exact", func(t *testing.T) {
		res := computePayout(dec("1000000000.00"), dec("33.33"), []payoutItem{
			item("Ore", "3", 1000000, p("a", 999999), p("b", 1)),
		})
		assert.True(t, res.Pool.Equal(dec("333300000")), "pool got %s", res.Pool)
		// a: 333300000 × 999999/1000000 = 333299666.70 exactly (no truncation)
		assert.True(t, payoutOf(t, res, "a").Amount.Equal(dec("333299666.70")), "got %s", payoutOf(t, res, "a").Amount)
		// b: 333300000 × 1/1000000 = 333.30
		assert.True(t, payoutOf(t, res, "b").Amount.Equal(dec("333.30")), "got %s", payoutOf(t, res, "b").Amount)
		assert.True(t, res.Remainder.IsZero(), "the split is exact, got %s", res.Remainder)
		sumShares(t, res)
	})

	t.Run("deterministic user ordering", func(t *testing.T) {
		res := computePayout(dec("100"), dec("100"), []payoutItem{
			item("Ore", "1", 3, p("zed", 1), p("amy", 1), p("mid", 1)),
		})
		require.Len(t, res.Shares, 3)
		assert.Equal(t, "amy", res.Shares[0].UserID)
		assert.Equal(t, "mid", res.Shares[1].UserID)
		assert.Equal(t, "zed", res.Shares[2].UserID)
	})

	// Regression: a pool so small every AMOUNT truncates to zero is still a
	// normal value-weighted split (positive shares) — payoutFigures must not
	// confuse it with the all-priceless case when re-deriving from stored rows.
	t.Run("tiny pool truncating all amounts is not zero-value", func(t *testing.T) {
		res := computePayout(dec("0.10"), dec("50"), []payoutItem{ // pool 0.05
			item("Ore", "1", 10,
				p("a", 1), p("b", 1), p("c", 1), p("d", 1), p("e", 1),
				p("f", 1), p("g", 1), p("h", 1), p("i", 1), p("j", 1)),
		})
		assert.False(t, res.ZeroValue)
		for _, s := range res.Shares {
			assert.True(t, s.Amount.IsZero())
			assert.True(t, s.SharePercent.IsPositive())
		}

		credits := dec("0.10")
		prog := Progress{Contract: Contract{RewardCredits: &credits, ParticipantRewardFactor: dec("50")}}
		pool, remainder, zeroValue := payoutFigures(prog, res.Shares)
		assert.True(t, pool.Equal(dec("0.05")))
		assert.True(t, remainder.Equal(dec("0.05")), "everything stays as remainder")
		assert.False(t, zeroValue, "positive shares: a split happened, just below a cent each")

		// The genuinely priceless case (all shares zero) still flags.
		zeroRows := []Payout{{UserID: "a"}, {UserID: "b"}}
		_, _, zeroValue = payoutFigures(prog, zeroRows)
		assert.True(t, zeroValue)
	})

	t.Run("zero-delivery reservations are excluded", func(t *testing.T) {
		res := computePayout(dec("100"), dec("100"), []payoutItem{
			item("Ore", "1", 10, p("worker", 10), p("lurker", 0)),
		})
		require.Len(t, res.Shares, 1)
		assert.Equal(t, "worker", res.Shares[0].UserID)
	})
}
