package contracts

import (
	"sort"

	"github.com/shopspring/decimal"
)

// Payout is one participant's computed credit reward for a completed contract —
// both the computation output and the persisted contract_payouts row shape.
// UserName is the display-name snapshot the task handler resolves before
// persisting (computePayout itself leaves it empty).
type Payout struct {
	UserID       string
	UserName     string
	Amount       decimal.Decimal // truncated (never rounded up) to the configured payout precision
	SharePercent decimal.Decimal // the participant's value share of the pool, percent (6dp)
}

// payoutItem is one contract item as the payout computation sees it: the unit
// value resolved from the gamedata catalog (zero for free-text / unknown /
// unpriced items) and who delivered how much of it.
type payoutItem struct {
	Name        string
	UnitValue   decimal.Decimal
	RequiredQty int
	Delivered   []Participant // per-member contribution (Delivered is what counts here)
}

// payoutResult is the full outcome of one payout computation.
type payoutResult struct {
	// Pool is the credits actually distributed: credits × factor / 100, truncated
	// to the configured payout precision (decimals).
	Pool decimal.Decimal
	// Shares are the per-participant payouts, ordered by UserID (deterministic
	// across runs). Every member who delivered anything appears, including those
	// whose delivered value is zero (they delivered only priceless items).
	Shares []Payout
	// Remainder is Pool − Σ Amounts: the rounding dust truncation leaves
	// undistributed (stays with the corp; reported, never re-allocated).
	Remainder decimal.Decimal
	// ZeroValue marks the degenerate case: the pool is positive but every item
	// has zero unit value, so there is nothing to weight shares by. Shares then
	// carry zero amounts (persisted anyway, so retries know compute happened).
	ZeroValue bool
	// Priceless names the items that contributed no value (free-text, unknown
	// gdid, or an unpriced catalog entry) — surfaced in the payout message so a
	// skewed split is explainable.
	Priceless []string
}

// computePayout splits a completed contract's participant pool across its
// deliverers, proportional to the VALUE each delivered. Pure — no I/O, no
// clock.
//
// The algorithm:
//
//	pool   = trunc_d(credits × factor / 100)   (Shift(-2), truncated to decimals)
//	den    = Σ over items of unitValue × requiredQty
//	num(p) = Σ over items of unitValue × delivered(p, item)
//	amount(p) = trunc_d(pool × num(p) / den)   (truncated at `decimals` places —
//	                                            never rounds up, so Σ amounts ≤
//	                                            pool holds structurally)
//	remainder = pool − Σ amounts
//
// decimals is the configured payout precision (0–2); at 0 rewards are whole
// credits. On completion every item is fully delivered (Σ delivered = required),
// so the full pool distributes up to the truncation remainder. Items with zero
// unit value contribute to neither side: their deliverers earn nothing for them,
// and they are listed in Priceless. If EVERY item is priceless, den is zero —
// the result is flagged ZeroValue with zero amounts instead of dividing by zero.
func computePayout(credits, factor decimal.Decimal, items []payoutItem, decimals int32) payoutResult {
	res := payoutResult{Pool: credits.Mul(factor).Shift(-2).Truncate(decimals)}

	den := decimal.Decimal{}
	nums := map[string]decimal.Decimal{} // user id → delivered value
	delivered := map[string]int{}        // user id → total delivered qty (any value)
	for _, it := range items {
		if !it.UnitValue.IsPositive() {
			res.Priceless = append(res.Priceless, it.Name)
			for _, p := range it.Delivered {
				delivered[p.UserID] += p.Delivered
			}
			continue
		}
		den = den.Add(it.UnitValue.Mul(decimal.NewFromInt(int64(it.RequiredQty))))
		for _, p := range it.Delivered {
			delivered[p.UserID] += p.Delivered
			if p.Delivered > 0 {
				nums[p.UserID] = nums[p.UserID].Add(it.UnitValue.Mul(decimal.NewFromInt(int64(p.Delivered))))
			}
		}
	}

	// Every member who delivered anything gets a row, ordered by user id.
	users := make([]string, 0, len(delivered))
	for id, qty := range delivered {
		if qty > 0 {
			users = append(users, id)
		}
	}
	sort.Strings(users)

	res.ZeroValue = !den.IsPositive()
	distributed := decimal.Decimal{}
	for _, id := range users {
		var amount, share decimal.Decimal
		if !res.ZeroValue {
			num := nums[id]
			amount, _ = res.Pool.Mul(num).QuoRem(den, decimals) // exact truncation at `decimals`
			share = num.Mul(oneHundred).DivRound(den, 6)
			distributed = distributed.Add(amount)
		}
		res.Shares = append(res.Shares, Payout{UserID: id, Amount: amount, SharePercent: share})
	}
	res.Remainder = res.Pool.Sub(distributed)
	return res
}
