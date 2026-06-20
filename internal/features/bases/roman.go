package bases

import (
	"strconv"
	"strings"
)

// planetMin/planetMax bound a planet's position within its system. The game
// numbers planets; we present them as Roman numerals I..X.
const (
	planetMin = 1
	planetMax = 10
)

var romanSymbols = []struct {
	value int
	digit string
}{
	{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
}

// toRoman renders a planet position (1..10) as a Roman numeral. A value outside
// the range renders as its decimal string, so an unexpected value is visible
// rather than silently wrong.
func toRoman(n int) string {
	if n < planetMin || n > planetMax {
		return strconv.Itoa(n)
	}
	var b strings.Builder
	for _, s := range romanSymbols {
		for n >= s.value {
			b.WriteString(s.digit)
			n -= s.value
		}
	}
	return b.String()
}
