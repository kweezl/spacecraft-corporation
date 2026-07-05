package gamedata

import (
	"math"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// The special reward ids: corporation currencies a contract pays out. They are
// real catalog items (so they carry a localized name/icon) but are surfaced
// through the dedicated Reward* methods, not RewardItems.
const (
	GDCorpoCredits    schema.GDID = "CorpoCredits"
	GDCorpoReputation schema.GDID = "CorpoReputation"
	GDLicensePoints   schema.GDID = "LicensePoints"
)

// isCurrency reports whether id is one of the corporation-currency reward ids.
func isCurrency(id schema.GDID) bool {
	switch id {
	case GDCorpoCredits, GDCorpoReputation, GDLicensePoints:
		return true
	}
	return false
}

// ContractItem is one contract line resolved against a Catalog: the underlying
// item (embedded, so its ID / IconName / stats are available), its localized
// display name, and the quantity the contract specifies — Qty is a requirement's
// RequestItem.Qty or a reward's RewardItem.Count.
type ContractItem struct {
	schema.Item
	Name string
	Qty  int
}

// ContractView binds a contract to the Catalog and language that resolve its
// item ids — the parent-chain lookups and localization the bare schema.Contract
// can't do on its own. Obtain one via Catalog.ContractView; it is an immutable
// value, safe to copy and use concurrently.
type ContractView struct {
	contract schema.Contract
	cat      *Catalog
	lang     i18n.Language
}

// ContractView resolves a contract id to a view bound to lang, or false if the
// id is unknown in this version. A missing translation falls back to DefaultLang.
func (c *Catalog) ContractView(id schema.GDID, lang i18n.Language) (ContractView, bool) {
	ct, ok := c.Contract(id)
	if !ok {
		return ContractView{}, false
	}
	return ContractView{contract: ct, cat: c, lang: lang}, true
}

// Contract returns the underlying contract template.
func (v ContractView) Contract() schema.Contract { return v.contract }

// RequiredItems lists the items the contract asks the player to deliver,
// localized and with icons, each carrying its required quantity. Lines whose
// item id is unknown in this version are skipped.
func (v ContractView) RequiredItems() []ContractItem {
	out := make([]ContractItem, 0, len(v.contract.Items))
	for _, ri := range v.contract.Items {
		if ci, ok := v.resolve(ri.Item, ri.Qty); ok {
			out = append(out, ci)
		}
	}
	return out
}

// RewardItems lists the item rewards, localized and with icons, each carrying
// its count. The corporation currencies (credits / reputation / license points)
// are excluded — read them via the Reward* methods. Unknown ids are skipped.
func (v ContractView) RewardItems() []ContractItem {
	out := make([]ContractItem, 0, len(v.contract.Rewards))
	for _, rw := range v.contract.Rewards {
		if isCurrency(rw.Item) {
			continue
		}
		if ci, ok := v.resolve(rw.Item, rw.Count); ok {
			out = append(out, ci)
		}
	}
	return out
}

// RewardCorpoCredits returns the contract's corporation-credit payout scaled by
// the corporation bonus factor (0.2 = +20%); 0 when the contract pays none.
func (v ContractView) RewardCorpoCredits(factor float64) int {
	return scaleReward(v.rewardCount(GDCorpoCredits), factor)
}

// RewardCorpoReputation returns the corporation-reputation payout scaled by the
// bonus factor; 0 when the contract pays none.
func (v ContractView) RewardCorpoReputation(factor float64) int {
	return scaleReward(v.rewardCount(GDCorpoReputation), factor)
}

// RewardCorpoLicensePoints returns the license-points payout scaled by the bonus
// factor; 0 when the contract pays none.
func (v ContractView) RewardCorpoLicensePoints(factor float64) int {
	return scaleReward(v.rewardCount(GDLicensePoints), factor)
}

// resolve turns an id + quantity into a localized ContractItem, reporting false
// for an id this version doesn't define.
func (v ContractView) resolve(id schema.GDID, qty int) (ContractItem, bool) {
	it, ok := v.cat.Item(id)
	if !ok {
		return ContractItem{}, false
	}
	return ContractItem{Item: it, Name: v.cat.Name(id, v.lang), Qty: qty}, true
}

// rewardCount returns the unscaled reward count for id, or 0 if not rewarded.
func (v ContractView) rewardCount(id schema.GDID) int {
	for _, rw := range v.contract.Rewards {
		if rw.Item == id {
			return rw.Count
		}
	}
	return 0
}

// scaleReward applies the corporation bonus: base × (1 + factor), rounded to the
// nearest integer. The factor is a bonus, so a negative value is clamped to 0
// (no bonus) rather than reducing the payout.
func scaleReward(base int, factor float64) int {
	if factor < 0 {
		factor = 0
	}
	return int(math.Round(float64(base) * (1 + factor)))
}
