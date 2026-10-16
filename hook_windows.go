//go:build windows

package main

import (
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The low-level mouse hook runs on its own thread so desktop work on the UI
// thread can never make the mouse stutter. It only reads a snapshot.

type titleRect struct {
	id   string
	rect Rect
}

type hookState struct {
	valid, editing, hidden, dblHide bool
	direct                          bool // move/resize plots without edit mode
	desk                            Desktop
	titles                          []titleRect
	plots                           []plotZone
	icons                           []Rect
}

type plotZone struct {
	id    string
	rect  Rect
	title Rect
	edge  int
}

// drag state, owned by the hook thread
var (
	dragID     string
	dragZone   int
	dragActive bool
	dragStart  Point
	dragLatest Point
	dragPosted bool
)

// zoneAt: 0 = none, 9 = move (title bar), otherwise WMSZ_* edge codes.
func zoneAt(st *hookState, pt Point) (string, int) {
	for i := len(st.plots) - 1; i >= 0; i-- {
		z := st.plots[i]
		r := z.rect
		if !r.Contains(pt) {
			continue
		}
		left, right := pt.X < r.L+z.edge, pt.X >= r.R-z.edge
		bottom := pt.Y >= r.B-z.edge
		switch {
		case bottom && left:
			return z.id, 7
		case bottom && right:
			return z.id, 8
		case left:
			return z.id, 1
		case right:
			return z.id, 2
		case bottom:
			return z.id, 6
		case z.title.Contains(pt):
			return z.id, 9
		}
		return "", 0
	}
	return "", 0
}

func dragInfo() (id string, zone int, start, latest Point) {
	hookMu.Lock()
	defer hookMu.Unlock()
	dragPosted = false
	return dragID, dragZone, dragStart, dragLatest
}

var (
	hookMu      sync.Mutex
	hookSt      = &hookState{}
	hookClicks  []ClickHint
	hookMenuID  string
	hookMainWnd HWND
	lastDown    time.Time
	lastDownPt  Point
)

func setHookState(s *hookState) {
	hookMu.Lock()
	hookSt = s
	hookMu.Unlock()
}

func drainClicks() []ClickHint {
	hookMu.Lock()
	defer hookMu.Unlock()
	c := hookClicks
	hookClicks = nil
	return c
}

func hookMenuPlot() string {
	hookMu.Lock()
	defer hookMu.Unlock()
	return hookMenuID
}

func startMouseHook(main HWND) {
	hookMainWnd = main
	go func() {
		runtime.LockOSThread()
		cb := windows.NewCallback(func(code int32, wp, lp uintptr) uintptr { return mouseHook(code, wp, lp) })
		h := call(pSetWindowsHookExW, WH_MOUSE_LL, cb, hInstance(), 0)
		if h == 0 {
			return
		}
		var m MSG
		for call(pGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0) > 0 {
			call(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
			call(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
		}
		call(pUnhookWindowsHookEx, h)
	}()
}

func pointArg(p Point) uintptr { return uintptr(uint32(int32(p.X))) | uintptr(uint32(int32(p.Y)))<<32 }

func mouseHook(code int32, wp, lp uintptr) uintptr {
	if code >= 0 {
		hookMu.Lock()
		st := hookSt
		hookMu.Unlock()
		if st.valid && (!st.editing || dragActive) {
			ms := (*MSLLHOOKSTRUCT)(unsafe.Pointer(lp))
			pt := Point{int(ms.Pt.X), int(ms.Pt.Y)}
			if swallow := handleMouse(st, uint32(wp), pt); swallow {
				return 1
			}
		}
	}
	return call(pCallNextHookEx, 0, uintptr(code), wp, lp)
}

func handleMouse(st *hookState, msg uint32, pt Point) bool {
	// an active plot drag owns the mouse until the button is released
	if dragActive {
		hookMu.Lock()
		dragLatest = pt
		post := !dragPosted
		dragPosted = true
		hookMu.Unlock()
		switch msg {
		case WM_MOUSEMOVE:
			if post {
				postMessage(hookMainWnd, msgDragMove, 0, 0)
			}
			return false
		case WM_LBUTTONUP:
			dragActive = false
			postMessage(hookMainWnd, msgDragEnd, 0, 0)
			return true
		}
		return false
	}
	switch msg {
	case WM_RBUTTONDOWN, WM_RBUTTONUP, WM_LBUTTONUP, WM_LBUTTONDOWN:
	default:
		return false
	}
	onDesk := st.desk.IsDesktopWindow(call(pWindowFromPoint, pointArg(pt)))
	if !onDesk {
		if msg == WM_LBUTTONDOWN {
			lastDown = time.Time{}
		}
		return false
	}
	switch msg {
	case WM_RBUTTONDOWN, WM_RBUTTONUP:
		if st.hidden {
			return false
		}
		for i := len(st.titles) - 1; i >= 0; i-- {
			if st.titles[i].rect.Contains(pt) {
				if msg == WM_RBUTTONUP {
					hookMu.Lock()
					hookMenuID = st.titles[i].id
					hookMu.Unlock()
					postMessage(hookMainWnd, msgPlotMenu, 0, 0)
				}
				return true // our menu instead of Explorer's
			}
		}
		if msg == WM_RBUTTONDOWN {
			addClick(ClickHint{pt, time.Now(), 60 * time.Second})
		}
	case WM_LBUTTONUP:
		if !st.hidden {
			addClick(ClickHint{pt, time.Now(), 4 * time.Second})
		}
	case WM_LBUTTONDOWN:
		if st.direct && !st.hidden {
			onIcon := false
			for _, r := range st.icons {
				if r.Contains(pt) {
					onIcon = true
				}
			}
			if id, z := zoneAt(st, pt); z != 0 && (z == 9 || !onIcon) {
				hookMu.Lock()
				dragID, dragZone, dragStart, dragLatest, dragPosted = id, z, pt, pt, false
				hookMu.Unlock()
				dragActive = true
				lastDown = time.Time{}
				postMessage(hookMainWnd, msgDragStart, 0, 0)
				return true // don't let Explorer start a selection box
			}
		}
		if !st.dblHide {
			return false
		}
		if !st.hidden {
			for _, r := range st.icons {
				if r.Contains(pt) {
					lastDown = time.Time{}
					return false
				}
			}
		}
		now := time.Now()
		dt := time.Duration(call(pGetDoubleClickTime)) * time.Millisecond
		tol := math.Max(4, float64(call(pGetSystemMetrics, 36 /*SM_CXDOUBLECLK*/)))
		if now.Sub(lastDown) <= dt && dist(pt, lastDownPt) <= tol {
			lastDown = time.Time{}
			postMessage(hookMainWnd, msgToggleHide, 0, 0)
		} else {
			lastDown, lastDownPt = now, pt
		}
	}
	return false
}

func addClick(c ClickHint) {
	hookMu.Lock()
	hookClicks = append(hookClicks, c)
	if len(hookClicks) > 50 {
		hookClicks = hookClicks[len(hookClicks)-50:]
	}
	hookMu.Unlock()
}
