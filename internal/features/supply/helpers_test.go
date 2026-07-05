package supply

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusMaskRoundTrip(t *testing.T) {
	// Empty selection defaults to open.
	assert.Equal(t, defaultMask, maskFromValues(nil))
	assert.Equal(t, defaultMask, maskFromValues([]string{}))
	assert.Equal(t, []Status{StatusOpen}, statusesFromMask(0))

	// Fold values → mask → statuses round-trips.
	m := maskFromValues([]string{"open", "cancelled"})
	assert.Equal(t, maskOpen|maskCancelled, m)
	assert.ElementsMatch(t, []Status{StatusOpen, StatusCancelled}, statusesFromMask(m))

	// Unknown values are ignored; an all-unknown selection defaults to open.
	assert.Equal(t, defaultMask, maskFromValues([]string{"bogus"}))

	// All three.
	all := maskFromValues([]string{"open", "completed", "cancelled"})
	assert.ElementsMatch(t, []Status{StatusOpen, StatusCompleted, StatusCancelled}, statusesFromMask(all))
}

func TestParsePlanet(t *testing.T) {
	// Empty is valid and clears (nil).
	p, ok := parsePlanet("")
	assert.True(t, ok)
	assert.Nil(t, p)

	p, ok = parsePlanet("  15 ")
	assert.True(t, ok)
	if assert.NotNil(t, p) {
		assert.Equal(t, 15, *p)
	}

	for _, bad := range []string{"0", "-1", "x", "1.5"} {
		_, ok := parsePlanet(bad)
		assert.Falsef(t, ok, "planet %q should be rejected", bad)
	}
}

func TestParseQty(t *testing.T) {
	n, err := parseQty(" 7 ")
	assert.NoError(t, err)
	assert.Equal(t, 7, n)
	for _, bad := range []string{"", "0", "-3", "abc"} {
		_, err := parseQty(bad)
		assert.Errorf(t, err, "qty %q should be rejected", bad)
	}
}

func TestConsoleErrorKey(t *testing.T) {
	key, ok := consoleErrorKey(ErrNotFound)
	assert.True(t, ok)
	assert.Equal(t, "supply.console.not_found", key)
	key, ok = consoleErrorKey(ErrItemExists)
	assert.True(t, ok)
	assert.Equal(t, "supply.console.item_exists", key)
	// ErrMaxItems/ErrLimit are handled by callers (need the limit), not mapped here.
	_, ok = consoleErrorKey(ErrMaxItems)
	assert.False(t, ok)
}

func TestPanelErrorKey(t *testing.T) {
	cases := map[error]string{
		ErrNotFound:       "supply.error.not_in_thread",
		ErrClosed:         "supply.error.closed",
		ErrOverCap:        "supply.reserve.over_cap",
		ErrOverReserved:   "supply.deliver.over_reserved",
		ErrNoReservation:  "supply.release.no_reservation",
		ErrBelowDelivered: "supply.release.below_delivered",
	}
	for err, want := range cases {
		got, ok := panelErrorKey(err)
		assert.Truef(t, ok, "%v should map", err)
		assert.Equal(t, want, got)
	}
}
