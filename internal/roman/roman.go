// Package roman renders positive integers as Roman numerals. It is a
// dependency-free leaf utility shared by feature modules (bases planet
// positions, supply request planet lines) so neither feature imports the other.
package roman

import (
	"strconv"
	"strings"
)

var symbols = []struct {
	value int
	digit string
}{
	{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
	{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
	{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
}

// Numeral renders n as a Roman numeral (1 → "I", 4 → "IV", 15 → "XV"). A value
// below 1 has no Roman form, so it renders as its decimal string — an
// unexpected value stays visible rather than becoming silently empty or wrong.
func Numeral(n int) string {
	if n < 1 {
		return strconv.Itoa(n)
	}
	var b strings.Builder
	for _, s := range symbols {
		for n >= s.value {
			b.WriteString(s.digit)
			n -= s.value
		}
	}
	return b.String()
}
