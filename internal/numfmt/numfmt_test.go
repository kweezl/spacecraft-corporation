package numfmt

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestGrouped(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		places int32
		want   string
	}{
		{"zero places0", "0", 0, "0"},
		{"zero places2", "0", 2, "0.00"},
		{"under thousand", "999", 0, "999"},
		{"exact thousand", "1000", 0, "1 000"},
		{"four digits", "1234", 0, "1 234"},
		{"five digits", "12345", 0, "12 345"},
		{"six digits", "123456", 0, "123 456"},
		{"million", "1000000", 0, "1 000 000"},
		{"million places2", "1000000", 2, "1 000 000.00"},
		{"decimals kept", "1234567.89", 2, "1 234 567.89"},
		{"one place", "1234.5", 1, "1 234.5"},
		// Truncation (round DOWN), never rounding up — the payout guarantee.
		{"truncate .99 to int", "1234.99", 0, "1 234"},
		{"truncate .999 to int", "999.999", 0, "999"},
		{"truncate to one place", "1000.99", 1, "1 000.9"},
		{"truncate to two places", "1234.5678", 2, "1 234.56"},
		{"negative", "-1234567", 0, "-1 234 567"},
		{"negative decimals", "-1000.5", 1, "-1 000.5"},
		{"small negative", "-5", 0, "-5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := decimal.NewFromString(c.in)
			assert.NoError(t, err)
			assert.Equal(t, c.want, Grouped(d, c.places))
		})
	}
}

// TestFix verifies Fixed truncates toward zero (never rounds up) at each
// precision — the CSV amount path relies on this for round-down payouts.
func TestFixed(t *testing.T) {
	cases := []struct {
		in     string
		places int32
		want   string
	}{
		{"6.8181", 2, "6.81"}, // would round to 6.82 under StringFixed
		{"33.99", 0, "33"},    // would round to 34
		{"0.999", 2, "0.99"},  // would round to 1.00
		{"1000000.995", 2, "1000000.99"},
		{"10", 0, "10"},
		{"10", 2, "10.00"},
	}
	for _, c := range cases {
		d, err := decimal.NewFromString(c.in)
		assert.NoError(t, err)
		assert.Equalf(t, c.want, Fixed(d, c.places), "Fixed(%s, %d)", c.in, c.places)
	}
}
