// Package numfmt renders numbers for human-facing display. Its sole helper
// groups thousands with a space (1000000 → "1 000 000"), the readable form used
// in user-facing messages. It is deliberately dependency-light: no locale
// library ships a guaranteed space group separator with fixed precision, and the
// grouping rule is a few lines, so hand-rolling it beats pulling in x/text.
package numfmt

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Fixed formats d with exactly places fractional digits, truncating toward zero
// (never rounding up) — so a non-negative monetary value is always shown rounded
// DOWN, matching how the payout computation itself truncates. No thousands
// grouping; use Grouped for the human-readable form.
func Fixed(d decimal.Decimal, places int32) string {
	return d.Truncate(places).StringFixed(places)
}

// Grouped is Fixed with a single space as the thousands separator on the integer
// part. Examples: places 0 → "1 000 000", places 2 → "1 000 000.00". Like Fixed
// it truncates toward zero (never rounds up). A leading minus sign is preserved
// and never grouped; the fractional part is left ungrouped.
func Grouped(d decimal.Decimal, places int32) string {
	s := Fixed(d, places)

	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	grouped := groupThousands(intPart)

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(grouped)
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(fracPart)
	}
	return b.String()
}

// groupThousands inserts a space every three digits from the right of a
// non-negative integer string (no sign, no decimal point).
func groupThousands(digits string) string {
	n := len(digits)
	if n <= 3 {
		return digits
	}
	// First group is the leftmost 1–3 digits; the rest split into full triples.
	lead := n % 3
	if lead == 0 {
		lead = 3
	}
	var b strings.Builder
	b.WriteString(digits[:lead])
	for i := lead; i < n; i += 3 {
		b.WriteByte(' ')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
