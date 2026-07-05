package roman

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNumeral(t *testing.T) {
	want := map[int]string{
		1: "I", 2: "II", 3: "III", 4: "IV", 5: "V",
		6: "VI", 7: "VII", 8: "VIII", 9: "IX", 10: "X",
		14: "XIV", 15: "XV", 40: "XL", 49: "XLIX", 90: "XC",
		400: "CD", 2026: "MMXXVI",
	}
	for n, r := range want {
		assert.Equalf(t, r, Numeral(n), "Numeral(%d)", n)
	}
}

func TestNumeral_NonPositiveRendersDecimal(t *testing.T) {
	assert.Equal(t, "0", Numeral(0))
	assert.Equal(t, "-1", Numeral(-1))
}
