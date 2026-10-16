//go:build windows

package main

import (
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Desktop locates Explorer's desktop windows and the layer behind the icons
// where plot panels are hosted.
type Desktop struct {
	Progman     HWND
	DefView     HWND
	ListView    HWND
	Host        HWND // parent for plot panels
	InsertAfter HWND // z-order anchor inside Host (0 = top)
}

var enumCb uintptr
var enumFound HWND

func init() {
	enumCb = windows.NewCallback(func(h HWND, _ uintptr) uintptr {
		if findWindowEx(h, 0, "SHELLDLL_DefView") != 0 {
			enumFound = h
			return 0
		}
		return 1
	})
}

func FindDesktop() *Desktop {
	d := &Desktop{Progman: findWindow("Progman")}
	if d.Progman == 0 {
		return nil
	}
	// Ask Progman to spawn the WorkerW that sits behind the desktop icons.
	var res uintptr
	call(pSendMessageTimeoutW, d.Progman, 0x052C, 0xD, 0x1, SMTO_NORMAL, 1000, uintptr(unsafe.Pointer(&res)))
	call(pSendMessageTimeoutW, d.Progman, 0x052C, 0, 0, SMTO_NORMAL, 1000, uintptr(unsafe.Pointer(&res)))

	if dv := findWindowEx(d.Progman, 0, "SHELLDLL_DefView"); dv != 0 {
		// Windows 11 24H2+ layout: Progman { SHELLDLL_DefView, WorkerW }.
		// Panels become children of Progman placed just below DefView.
		d.DefView = dv
		d.Host = d.Progman
		d.InsertAfter = dv
	} else {
		// Classic layout: a top-level WorkerW holds DefView; the next WorkerW
		// in z-order draws the wallpaper behind it.
		enumFound = 0
		call(pEnumWindows, enumCb, 0)
		if enumFound == 0 {
			return nil
		}
		d.DefView = findWindowEx(enumFound, 0, "SHELLDLL_DefView")
		d.Host = findWindowEx(0, enumFound, "WorkerW")
		if d.Host == 0 {
			d.Host = d.Progman
		}
	}
	d.ListView = findWindowEx(d.DefView, 0, "SysListView32")
	return d
}

func (d *Desktop) Valid() bool {
	return d != nil && isWindow(d.Host) && isWindow(d.ListView)
}

// ListOrigin is the screen position of the icon view's (0,0).
func (d *Desktop) ListOrigin() Point { return clientToScreen(d.ListView, Point{}) }

func (d *Desktop) IsDesktopWindow(h HWND) bool {
	if h == 0 {
		return false
	}
	if h == d.ListView || h == d.DefView || h == d.Progman || h == d.Host {
		return true
	}
	switch className(h) {
	case "Progman", "WorkerW", "SHELLDLL_DefView", "SysListView32":
		root := call(pGetAncestor, h, GA_ROOT)
		c := className(root)
		return c == "Progman" || c == "WorkerW"
	}
	return false
}

// ---------------------------------------------------------------- monitors

type Monitor struct {
	Name    string
	Rect    Rect
	Work    Rect
	Primary bool
	Scale   float64
}

var monCb uintptr
var monList []Monitor

func init() {
	monCb = windows.NewCallback(func(hm, hdc, lprc, data uintptr) uintptr {
		var mi MONITORINFOEXW
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		if call(pGetMonitorInfoW, hm, uintptr(unsafe.Pointer(&mi))) == 0 {
			return 1
		}
		var dx, dy uint32 = 96, 96
		if pGetDpiForMonitor.Find() == nil {
			call(pGetDpiForMonitor, hm, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy)))
		}
		r, w := mi.RcMonitor, mi.RcWork
		monList = append(monList, Monitor{
			Name:    windows.UTF16ToString(mi.SzDevice[:]),
			Rect:    Rect{int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)},
			Work:    Rect{int(w.Left), int(w.Top), int(w.Right), int(w.Bottom)},
			Primary: mi.DwFlags&MONITORINFOF_PRIMARY != 0,
			Scale:   math.Max(1, float64(dx)/96),
		})
		return 1
	})
}

func Monitors() []Monitor {
	monList = nil
	call(pEnumDisplayMonitors, 0, 0, monCb, 0)
	out := monList
	monList = nil
	return out
}

func primaryMonitor(ms []Monitor) Monitor {
	for _, m := range ms {
		if m.Primary {
			return m
		}
	}
	if len(ms) > 0 {
		return ms[0]
	}
	return Monitor{Name: "", Rect: Rect{0, 0, 1920, 1080}, Work: Rect{0, 0, 1920, 1040}, Scale: 1}
}

func monitorByName(ms []Monitor, name string) (Monitor, bool) {
	for _, m := range ms {
		if m.Name == name {
			return m, true
		}
	}
	return primaryMonitor(ms), false
}

func monitorAt(ms []Monitor, p Point) Monitor {
	for _, m := range ms {
		if m.Rect.Contains(p) {
			return m
		}
	}
	best, bd := primaryMonitor(ms), math.MaxFloat64
	for _, m := range ms {
		if d := dist(m.Rect.Center(), p); d < bd {
			best, bd = m, d
		}
	}
	return best
}
