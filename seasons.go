package main

import (
	"image/color"
	"strings"
	"time"
)

// Seasonal themes: holiday and season looks for every plot. Each theme has
// colours for the panel, title band and title text, a drawn ornament, and
// the days it shows when Plots picks themes automatically.

type Season struct {
	Key, Name           string
	Body, Header, Title color.NRGBA
	Ornament            string
	Window              func(year int) (from, to time.Time) // inclusive days; may start the year before
	DefaultOn           bool                                // allowed for Automatic unless turned off
	Region              string                              // "CA" / "US": only on by default there
}

func ymd(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }

func fixedWindow(m1 time.Month, d1 int, m2 time.Month, d2 int) func(int) (time.Time, time.Time) {
	return func(y int) (time.Time, time.Time) {
		from := ymd(y, m1, d1)
		if m1 > m2 { // crosses New Year: belongs to the year it ends in
			from = ymd(y-1, m1, d1)
		}
		return from, ymd(y, m2, d2)
	}
}

// around is a window of days before..after a date that moves each year.
func around(date func(int) time.Time, before, after int) func(int) (time.Time, time.Time) {
	return func(y int) (time.Time, time.Time) {
		d := date(y)
		if d.IsZero() {
			return time.Time{}, time.Time{}
		}
		return d.AddDate(0, 0, -before), d.AddDate(0, 0, after)
	}
}

// Easter Sunday (Western), anonymous Gregorian algorithm.
func Easter(y int) time.Time {
	a, b, c := y%19, y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return ymd(y, time.Month(month), day)
}

// nthWeekday is the n-th given weekday of a month (n = 1..5).
func nthWeekday(y int, m time.Month, wd time.Weekday, n int) time.Time {
	d := ymd(y, m, 1)
	for d.Weekday() != wd {
		d = d.AddDate(0, 0, 1)
	}
	return d.AddDate(0, 0, 7*(n-1))
}

// Lunar New Year's day; the lunar calendar has no simple formula.
var lunarNewYear = map[int]time.Time{
	2026: ymd(2026, 2, 17), 2027: ymd(2027, 2, 6), 2028: ymd(2028, 1, 26), 2029: ymd(2029, 2, 13),
	2030: ymd(2030, 2, 3), 2031: ymd(2031, 1, 23), 2032: ymd(2032, 2, 11), 2033: ymd(2033, 1, 31),
	2034: ymd(2034, 2, 19), 2035: ymd(2035, 2, 8), 2036: ymd(2036, 1, 28), 2037: ymd(2037, 2, 15),
	2038: ymd(2038, 2, 4), 2039: ymd(2039, 1, 24), 2040: ymd(2040, 2, 12),
}

func hx(s string) color.NRGBA { return ParseHex(s) }

// Seasons in calendar order. Short holidays win over the long seasons they
// fall in (see ActiveSeason).
var Seasons = []*Season{
	{Key: "newyear", Name: N("New Year"), Body: hx("#14142B"), Header: hx("#7A6114"), Title: hx("#FFF3C4"), Ornament: "star-gold",
		Window: fixedWindow(time.December, 31, time.January, 2), DefaultOn: true},
	{Key: "winter", Name: N("Winter"), Body: hx("#A9CCE3"), Header: hx("#4F7FA8"), Title: hx("#FFFFFF"), Ornament: "snowflake",
		Window: fixedWindow(time.December, 21, time.March, 19)},
	{Key: "lunarnewyear", Name: N("Lunar New Year"), Body: hx("#A3121B"), Header: hx("#7E0D14"), Title: hx("#FFD45A"), Ornament: "lantern",
		Window: around(func(y int) time.Time { return lunarNewYear[y] }, 3, 14), DefaultOn: true},
	{Key: "valentines", Name: N("Valentine's Day"), Body: hx("#F4A7C1"), Header: hx("#D63A6A"), Title: hx("#FFFFFF"), Ornament: "heart",
		Window: fixedWindow(time.February, 7, time.February, 14), DefaultOn: true},
	{Key: "stpatricks", Name: N("St. Patrick's Day"), Body: hx("#1D5E3A"), Header: hx("#14482B"), Title: hx("#FFFFFF"), Ornament: "shamrock",
		Window: fixedWindow(time.March, 10, time.March, 17), DefaultOn: true},
	{Key: "spring", Name: N("Spring"), Body: hx("#CDEBC0"), Header: hx("#5E9E50"), Title: hx("#FFFFFF"), Ornament: "flower",
		Window: fixedWindow(time.March, 20, time.June, 20)},
	{Key: "easter", Name: N("Easter"), Body: hx("#D9ECFA"), Header: hx("#9C80D8"), Title: hx("#FFFFFF"), Ornament: "egg",
		Window: around(Easter, 7, 1), DefaultOn: true},
	{Key: "canadaday", Name: N("Canada Day"), Body: hx("#D52B1E"), Header: hx("#A51C14"), Title: hx("#FFFFFF"), Ornament: "maple-white",
		Window: fixedWindow(time.June, 24, time.July, 1), DefaultOn: true, Region: "CA"},
	{Key: "july4", Name: N("Independence Day (US)"), Body: hx("#1D3A73"), Header: hx("#B22234"), Title: hx("#FFFFFF"), Ornament: "star-white",
		Window: fixedWindow(time.June, 28, time.July, 4), DefaultOn: true, Region: "US"},
	{Key: "summer", Name: N("Summer"), Body: hx("#FFE7A3"), Header: hx("#E0952A"), Title: hx("#FFFFFF"), Ornament: "sun",
		Window: fixedWindow(time.June, 21, time.September, 21)},
	{Key: "autumn", Name: N("Autumn"), Body: hx("#A04A16"), Header: hx("#6E3110"), Title: hx("#FFFFFF"), Ornament: "maple-orange",
		Window: fixedWindow(time.September, 22, time.December, 20)},
	{Key: "thanksgiving-ca", Name: N("Thanksgiving (Canada)"), Body: hx("#5A2E10"), Header: hx("#8A4A1A"), Title: hx("#FFFFFF"), Ornament: "maple-orange",
		Window: around(func(y int) time.Time { return nthWeekday(y, time.October, time.Monday, 2) }, 6, 0), DefaultOn: true, Region: "CA"},
	{Key: "halloween", Name: N("Halloween"), Body: hx("#2A1B3D"), Header: hx("#E8741C"), Title: hx("#FFFFFF"), Ornament: "pumpkin",
		Window: fixedWindow(time.October, 17, time.October, 31), DefaultOn: true},
	{Key: "thanksgiving-us", Name: N("Thanksgiving (US)"), Body: hx("#5A2E10"), Header: hx("#8A4A1A"), Title: hx("#FFFFFF"), Ornament: "maple-orange",
		Window: around(func(y int) time.Time { return nthWeekday(y, time.November, time.Thursday, 4) }, 7, 0), DefaultOn: true, Region: "US"},
	{Key: "christmas", Name: N("Christmas"), Body: hx("#7A1420"), Header: hx("#1E6B35"), Title: hx("#FFFFFF"), Ornament: "tree",
		Window: fixedWindow(time.December, 1, time.December, 26), DefaultOn: true},
}

func SeasonByKey(key string) *Season {
	for _, s := range Seasons {
		if s.Key == key {
			return s
		}
	}
	return nil
}

// SeasonsOffByDefault is the starting list of themes Automatic skips: the
// long seasons, and national holidays outside their country.
func SeasonsOffByDefault(country string) []string {
	country = strings.ToUpper(country)
	var off []string
	for _, s := range Seasons {
		if !s.DefaultOn || (s.Region != "" && s.Region != country) {
			off = append(off, s.Key)
		}
	}
	return off
}

// occurrence is s's window that contains day, or the next one after it.
func (s *Season) occurrence(day time.Time) (from, to time.Time, ok bool) {
	for y := day.Year(); y <= day.Year()+1; y++ {
		f, t := s.Window(y)
		if f.IsZero() || t.Before(day) {
			continue
		}
		return f, t, true
	}
	return time.Time{}, time.Time{}, false
}

func dayOf(t time.Time) time.Time { return ymd(t.Year(), t.Month(), t.Day()) }

// ActiveSeason picks the theme for now. mode is "" (off), "auto" or a theme
// key (always that theme). In auto mode, of the allowed themes showing
// today, the one that ends soonest wins, so Halloween beats Autumn and
// Christmas beats Winter. until is the last day it shows (zero if forced).
func ActiveSeason(mode string, off []string, now time.Time) (s *Season, until time.Time) {
	switch mode {
	case "":
		return nil, time.Time{}
	case "auto":
	default:
		return SeasonByKey(mode), time.Time{}
	}
	day := dayOf(now)
	skip := map[string]bool{}
	for _, k := range off {
		skip[k] = true
	}
	for _, c := range Seasons {
		if skip[c.Key] {
			continue
		}
		if f, t, ok := c.occurrence(day); ok && !f.After(day) && (s == nil || t.Before(until)) {
			s, until = c, t
		}
	}
	return s, until
}

// NextSeason is the allowed theme that starts soonest after today.
func NextSeason(off []string, now time.Time) (*Season, time.Time) {
	day := dayOf(now)
	skip := map[string]bool{}
	for _, k := range off {
		skip[k] = true
	}
	var best *Season
	var at time.Time
	for _, c := range Seasons {
		if skip[c.Key] {
			continue
		}
		f, _, ok := c.occurrence(day.AddDate(0, 0, 1))
		if ok && f.After(day) && (best == nil || f.Before(at)) {
			best, at = c, f
		}
	}
	return best, at
}

// SeasonDates is when s next shows (or is showing), for the settings list.
func SeasonDates(s *Season, now time.Time) (from, to time.Time, ok bool) {
	return s.occurrence(dayOf(now))
}
