package bases

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToRoman_PlanetRange(t *testing.T) {
	want := map[int]string{
		1: "I", 2: "II", 3: "III", 4: "IV", 5: "V",
		6: "VI", 7: "VII", 8: "VIII", 9: "IX", 10: "X",
	}
	for n, r := range want {
		assert.Equalf(t, r, toRoman(n), "toRoman(%d)", n)
	}
}

func TestToRoman_OutOfRangeRendersDecimal(t *testing.T) {
	assert.Equal(t, "0", toRoman(0))
	assert.Equal(t, "11", toRoman(11))
	assert.Equal(t, "-1", toRoman(-1))
}
