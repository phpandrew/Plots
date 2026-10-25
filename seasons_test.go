package main

import (
	"testing"
	"time"
)

func key(s *Season) string {
	if s == nil {
		return ""
	}
	return s.Key
}

func TestEaster(t *testing.T) {
	for y, want := range map[int]string{2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05", 2027: "2027-03-28", 2030: "2030-04-21"} {
		if got := Easter(y).Format("2006-01-02"); got != want {
			t.Errorf("Easter(%d) = %s, want %s", y, got, want)
		}
	}
	if d := nthWeekday(2026, time.October, time.Monday, 2); d.Day() != 12 {
		t.Errorf("Canadian Thanksgiving 2026 = %v", d)
	}
	if d := nthWeekday(2026, time.November, time.Thursday, 4); d.Day() != 26 {
		t.Errorf("US Thanksgiving 2026 = %v", d)
	}
}

func TestActiveSeason(t *testing.T) {
	ca := SeasonsOffByDefault("CA")
	all := []string{}
	cases := []struct {
		date string
		off  []string
		want string
	}{
		{"2026-10-25", ca, "halloween"},
		{"2026-10-31", ca, "halloween"},
		{"2026-11-01", ca, ""},
		{"2026-11-01", all, "autumn"},
		{"2026-10-25", all, "halloween"}, // beats autumn
		{"2026-11-22", all, "thanksgiving-us"},
		{"2026-11-22", ca, ""}, // US Thanksgiving is off in Canada
		{"2026-10-10", ca, "thanksgiving-ca"},
		{"2026-12-24", all, "christmas"}, // beats winter
		{"2026-12-31", ca, "newyear"},
		{"2027-01-01", ca, "newyear"},
		{"2027-01-02", all, "newyear"},
		{"2027-01-10", all, "winter"},
		{"2026-02-14", ca, "valentines"},
		{"2026-02-18", ca, "lunarnewyear"},
		{"2026-03-17", ca, "stpatricks"},
		{"2026-04-01", ca, "easter"},
		{"2026-06-30", ca, "canadaday"},
		{"2026-07-03", ca, ""},
		{"2026-06-30", all, "canadaday"}, // ends first
		{"2026-07-03", all, "july4"},
		{"2026-08-01", all, "summer"},
	}
	for _, c := range cases {
		now, _ := time.ParseInLocation("2006-01-02", c.date, time.Local)
		now = now.Add(15 * time.Hour)
		if s, _ := ActiveSeason("auto", c.off, now); key(s) != c.want {
			t.Errorf("%s: got %q, want %q", c.date, key(s), c.want)
		}
	}
	if s, _ := ActiveSeason("christmas", ca, time.Now()); key(s) != "christmas" {
		t.Error("forced theme not used")
	}
	if s, _ := ActiveSeason("", nil, time.Now()); s != nil {
		t.Error("off should show nothing")
	}
}

func TestNextSeason(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	s, at := NextSeason(SeasonsOffByDefault("CA"), now)
	if key(s) != "christmas" || at.Day() != 1 || at.Month() != time.December {
		t.Errorf("next = %s %v", key(s), at)
	}
	for _, s := range Seasons {
		if _, _, ok := SeasonDates(s, now); !ok && s.Key != "lunarnewyear" {
			t.Errorf("%s has no next date", s.Key)
		}
	}
}

func TestDefaults(t *testing.T) {
	in := func(list []string, k string) bool {
		for _, x := range list {
			if x == k {
				return true
			}
		}
		return false
	}
	ca, us, fr := SeasonsOffByDefault("ca"), SeasonsOffByDefault("US"), SeasonsOffByDefault("FR")
	if in(ca, "canadaday") || !in(ca, "july4") || !in(us, "canadaday") || in(us, "july4") || !in(fr, "july4") || in(fr, "halloween") || !in(fr, "winter") {
		t.Errorf("defaults: ca=%v us=%v fr=%v", ca, us, fr)
	}
}
