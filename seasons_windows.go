//go:build windows

package main

import (
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pGetUserDefaultGeoName = kernel32.NewProc("GetUserDefaultGeoName")
	pGetDateFormatEx       = kernel32.NewProc("GetDateFormatEx")

	seasonDay, seasonSig string
	seasonCur            *Season
)

// initSeasons gives SeasonsOff its defaults the first time: national
// holidays are on only in their own country (Windows' region setting).
func (a *App) initSeasons() {
	if a.cfg.SeasonsInit {
		return
	}
	a.cfg.SeasonsOff = SeasonsOffByDefault(userCountry())
	a.cfg.SeasonsInit = true
	a.markDirty()
}

// userCountry is the two-letter region from Windows settings, e.g. "CA".
func userCountry() string {
	buf := make([]uint16, 16)
	if pGetUserDefaultGeoName.Find() != nil {
		return ""
	}
	if n, _, _ := pGetUserDefaultGeoName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n == 0 {
		return ""
	}
	return strings.ToUpper(windows.UTF16ToString(buf))
}

// season is the seasonal theme showing now (nil if none). It's worked out
// again when the day or the settings change.
func (a *App) season() *Season {
	c := a.cfg
	day := time.Now().Format("2006-01-02")
	sig := c.Season + "|" + strings.Join(c.SeasonsOff, ",")
	if day != seasonDay || sig != seasonSig {
		seasonDay, seasonSig = day, sig
		seasonCur, _ = ActiveSeason(c.Season, c.SeasonsOff, time.Now())
	}
	return seasonCur
}

// applySeason restyles o for the current seasonal theme.
func (a *App) applySeason(o *RenderOpts) {
	se := a.season()
	if se == nil {
		return
	}
	o.Ornament = se.Ornament
	if !a.cfg.SeasonDecorOnly {
		o.Color, o.Header, o.HeaderCol, o.TitleCol, o.TitleAuto = se.Body, HeaderCustom, se.Header, se.Title, false
	}
}

type systemTime struct {
	Year, Month, DayOfWeek, Day, Hour, Minute, Second, Milliseconds uint16
}

// monthDay formats a date as the user's Windows locale writes month and day
// ("October 25", "25 octobre", "10月25日").
func monthDay(t time.Time) string {
	st := systemTime{Year: uint16(t.Year()), Month: uint16(t.Month()), Day: uint16(t.Day())}
	buf := make([]uint16, 64)
	if pGetDateFormatEx.Find() == nil {
		if n, _, _ := pGetDateFormatEx.Call(0, 0x80 /*DATE_MONTHDAY*/, uintptr(unsafe.Pointer(&st)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); n > 0 {
			return windows.UTF16ToString(buf)
		}
	}
	return t.Format("Jan 2")
}

// seasonRange is "Oct 17 – Oct 31" for when s is (or next is) showing.
func seasonRange(s *Season) string {
	f, t, ok := SeasonDates(s, time.Now())
	if !ok {
		return T("Dates not known yet")
	}
	return monthDay(f) + " – " + monthDay(t)
}
