package contracts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestItemOutstandingReserved(t *testing.T) {
	cases := []struct {
		name      string
		reserved  int
		delivered int
		want      int
	}{
		{"none delivered", 100, 0, 100},
		{"partially delivered", 100, 40, 60},
		{"fully delivered", 100, 100, 0},
		{"over-delivered floors at zero", 100, 120, 0},
		{"nothing reserved", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := Item{ReservedQty: c.reserved, DeliveredQty: c.delivered}
			assert.Equal(t, c.want, it.OutstandingReserved())
		})
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"3d 11h 10m", 3*24*time.Hour + 11*time.Hour + 10*time.Minute, true},
		{"5h", 5 * time.Hour, true},
		{"41m", 41 * time.Minute, true},
		{"4d 21m", 4*24*time.Hour + 21*time.Minute, true},
		{"2D", 2 * 24 * time.Hour, true},
		{"1d2h3m", 26*time.Hour + 3*time.Minute, true},
		{"  7h  ", 7 * time.Hour, true},
		{"", 0, false},
		{"0m", 0, false},
		{"abc", 0, false},
		{"10x", 0, false},
		{"3m 5h", 0, false}, // wrong order
	}
	for _, c := range cases {
		got, err := parseDuration(c.in)
		if !c.ok {
			assert.Errorf(t, err, "parseDuration(%q) should error", c.in)
			continue
		}
		require.NoErrorf(t, err, "parseDuration(%q)", c.in)
		assert.Equalf(t, c.want, got, "parseDuration(%q)", c.in)
	}
}

func TestFormatTimeLeft(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{3*24*time.Hour + 11*time.Hour + 10*time.Minute, "3d 11h 10m"},
		{5 * time.Hour, "5h"},
		{41 * time.Minute, "41m"},
		{4*24*time.Hour + 21*time.Minute, "4d 21m"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
		{0, "0m"},
		{-time.Hour, "0m"},
		{30 * time.Second, "0m"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, formatTimeLeft(c.d), "formatTimeLeft(%s)", c.d)
	}
}
