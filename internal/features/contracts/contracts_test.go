package contracts

import (
	"context"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func testLoc(t *testing.T) *i18n.Localizer {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
}

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

func TestParseDHM(t *testing.T) {
	// All blank or all zero -> no deadline (nil), no error.
	for _, c := range [][3]string{{"", "", ""}, {"0", "0", "0"}, {"", "0", ""}} {
		got, err := parseDHM(c[0], c[1], c[2])
		require.NoError(t, err)
		assert.Nil(t, got, "blank/zero d/h/m means no deadline")
	}

	// A positive total -> now + that duration.
	got, err := parseDHM("1", "2", "3")
	require.NoError(t, err)
	require.NotNil(t, got)
	want := time.Now().Add(26*time.Hour + 3*time.Minute)
	assert.WithinDuration(t, want, *got, 5*time.Second)

	// Blanks count as zero between set fields.
	got, err = parseDHM("", "5", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.WithinDuration(t, time.Now().Add(5*time.Hour), *got, 5*time.Second)

	// Garbage / negatives are rejected.
	for _, c := range [][3]string{{"x", "", ""}, {"-1", "", ""}, {"", "1.5", ""}} {
		_, err := parseDHM(c[0], c[1], c[2])
		assert.ErrorIs(t, err, ErrBadDuration, "parseDHM(%v) should error", c)
	}
}

func TestParseDHMMinutes(t *testing.T) {
	cases := []struct {
		d, h, m string
		want    int
	}{
		{"", "", "", 0},
		{"0", "0", "0", 0},
		{"1", "2", "3", 26*60 + 3},
		{"", "5", "", 5 * 60},
		{"2", "", "", 2 * 24 * 60},
	}
	for _, c := range cases {
		got, err := parseDHMMinutes(c.d, c.h, c.m)
		require.NoError(t, err)
		assert.Equalf(t, c.want, got, "parseDHMMinutes(%q,%q,%q)", c.d, c.h, c.m)
	}
	for _, c := range [][3]string{{"x", "", ""}, {"-1", "", ""}, {"", "1.5", ""}} {
		_, err := parseDHMMinutes(c[0], c[1], c[2])
		assert.ErrorIs(t, err, ErrBadDuration, "parseDHMMinutes(%v) should error", c)
	}
}

func TestDHMStrings(t *testing.T) {
	d, h, m := dhmStrings(26*60 + 3)
	assert.Equal(t, []string{"1", "2", "3"}, []string{d, h, m})

	// Zero minutes prefill as all-blank (reads as "none" in the modal), and the
	// round-trip through parseDHMMinutes lands back on zero.
	d, h, m = dhmStrings(0)
	assert.Equal(t, []string{"", "", ""}, []string{d, h, m})
	got, err := parseDHMMinutes(d, h, m)
	require.NoError(t, err)
	assert.Zero(t, got)

	// A positive value round-trips exactly.
	d, h, m = dhmStrings(3*24*60 + 11*60 + 10)
	got, err = parseDHMMinutes(d, h, m)
	require.NoError(t, err)
	assert.Equal(t, 3*24*60+11*60+10, got)
}

func TestParseCredits(t *testing.T) {
	// Blank clears the reward.
	for _, in := range []string{"", "  "} {
		got, err := parseCredits(in)
		require.NoErrorf(t, err, "parseCredits(%q)", in)
		assert.Nilf(t, got, "parseCredits(%q)", in)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"12", "12"},
		{"12.50", "12.5"},
		{"12,50", "12.5"}, // comma accepted as the separator
		{"12.5", "12.5"},
		{"1234567890.99", "1234567890.99"},
	}
	for _, c := range cases {
		got, err := parseCredits(c.in)
		require.NoErrorf(t, err, "parseCredits(%q)", c.in)
		require.NotNilf(t, got, "parseCredits(%q)", c.in)
		assert.Truef(t, got.Equal(decimal.RequireFromString(c.want)), "parseCredits(%q) = %s, want %s", c.in, got, c.want)
	}
	for _, in := range []string{"-1", "1.234", "x", "1e3", "12345678901", ".5", "1.", "1,2,3"} {
		_, err := parseCredits(in)
		assert.ErrorIsf(t, err, ErrBadReward, "parseCredits(%q) should error", in)
	}
}

func TestParseFactor(t *testing.T) {
	// Blank means zero (the column is NOT NULL — there is no "unset").
	for _, in := range []string{"", "  "} {
		got, err := parseFactor(in)
		require.NoErrorf(t, err, "parseFactor(%q)", in)
		assert.Truef(t, got.IsZero(), "parseFactor(%q) = %s", in, got)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"100", "100"},
		{"100.00", "100"},
		{"33,33", "33.33"}, // comma accepted as the separator
		{"12.5", "12.5"},
		{"0.01", "0.01"},
	}
	for _, c := range cases {
		got, err := parseFactor(c.in)
		require.NoErrorf(t, err, "parseFactor(%q)", c.in)
		assert.Truef(t, got.Equal(decimal.RequireFromString(c.want)), "parseFactor(%q) = %s, want %s", c.in, got, c.want)
	}
	for _, in := range []string{"-1", "101", "100.01", "1.234", "x", "1e3", ".5", "1.", "1,2,3"} {
		_, err := parseFactor(in)
		assert.ErrorIsf(t, err, ErrBadReward, "parseFactor(%q) should error", in)
	}
}

func TestCreditsSet(t *testing.T) {
	pos := decimal.RequireFromString("0.01")
	zero := decimal.Zero
	assert.True(t, creditsSet(&pos))
	assert.False(t, creditsSet(&zero), "zero counts as no reward")
	assert.False(t, creditsSet(nil))
}

func TestParseRewardInt(t *testing.T) {
	got, err := parseRewardInt("")
	require.NoError(t, err)
	assert.Nil(t, got, "blank clears the reward")

	got, err = parseRewardInt(" 42 ")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 42, *got)

	got, err = parseRewardInt("0")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Zero(t, *got)

	for _, in := range []string{"-1", "x", "1.5"} {
		_, err := parseRewardInt(in)
		assert.ErrorIsf(t, err, ErrBadReward, "parseRewardInt(%q) should error", in)
	}
}

func TestSplitDHM(t *testing.T) {
	d, h, m := splitDHM(26*time.Hour + 3*time.Minute)
	assert.Equal(t, 1, d)
	assert.Equal(t, 2, h)
	assert.Equal(t, 3, m)
	d, h, m = splitDHM(-time.Hour)
	assert.Equal(t, 0, d)
	assert.Equal(t, 0, h)
	assert.Equal(t, 0, m)
}

func TestFormatTimeLeft(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{3*24*time.Hour + 11*time.Hour + 10*time.Minute, "3d 11h 10m"},
		{5 * time.Hour, "5h"},
		{41 * time.Minute, "41m"},
		{0, "0m"},
		{-time.Hour, "0m"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, formatTimeLeft(c.d), "formatTimeLeft(%s)", c.d)
	}
}

func TestBuildIDParseID(t *testing.T) {
	cid := uuid.New().String()
	user := "123456789012345678" // snowflake
	cases := []struct {
		seg   string
		parts []string
	}{
		{segView, []string{cid}},
		{segListPage, []string{"7", "2"}},
		{segPEdit, []string{cid, user}},
		{segCBack, nil},
	}
	for _, c := range cases {
		id := buildID(c.seg, c.parts...)
		assert.Lessf(t, len(id), 100, "id %q must fit Discord's 100-char limit", id)
		seg, parts, ok := parseID(id)
		require.Truef(t, ok, "parseID(%q)", id)
		assert.Equal(t, c.seg, seg)
		assert.Equal(t, c.parts, nilIfEmpty(parts))
	}
	// Foreign prefix is rejected.
	_, _, ok := parseID("settings:theme")
	assert.False(t, ok)
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestMaskRoundTrip(t *testing.T) {
	// Empty selection defaults to open.
	assert.Equal(t, []Status{StatusOpen}, statusesFromMask(maskFromValues(nil)))
	// All four values -> all statuses.
	all := statusesFromMask(maskFromValues([]string{"open", "completed", "expired", "cancelled"}))
	assert.ElementsMatch(t, []Status{StatusOpen, StatusCompleted, StatusExpired, StatusCancelled}, all)
	// Subset round-trips.
	m := maskFromValues([]string{"completed", "cancelled"})
	assert.ElementsMatch(t, []Status{StatusCompleted, StatusCancelled}, statusesFromMask(m))
	// Unknown value alone -> default open.
	assert.Equal(t, []Status{StatusOpen}, statusesFromMask(maskFromValues([]string{"bogus"})))
}

func TestIsDeletedPost(t *testing.T) {
	del := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage}}
	delCh := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownChannel}}
	perm := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeMissingPermissions}}

	assert.True(t, isDeletedPost(del))
	assert.True(t, isDeletedPost(delCh))
	assert.False(t, isDeletedPost(perm))
	assert.False(t, isDeletedPost(nil))
	// A deleted-message error is NOT treated as a generic permanent error, so the
	// refresh path can recreate instead of abandoning.
	assert.False(t, isPermanentDiscordError(del))
}

func TestStatusLine_NoDeadline(t *testing.T) {
	f := &Feature{loc: testLoc(t)}
	ctx := context.Background()
	sid := uuid.New()

	deadline := time.Now().Add(2 * time.Hour)
	withDL := f.statusLine(ctx, sid, Progress{Contract: Contract{Status: StatusOpen, Deadline: &deadline}})
	noDL := f.statusLine(ctx, sid, Progress{Contract: Contract{Status: StatusOpen, Deadline: nil}})

	assert.Contains(t, withDL, "<t:", "a deadline renders a Discord timestamp")
	assert.NotContains(t, noDL, "<t:", "no deadline renders no timestamp")
	assert.NotEqual(t, withDL, noDL)
	assert.NotEmpty(t, noDL)
}
