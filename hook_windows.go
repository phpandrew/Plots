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
	id        string
	idx       int
	rect      Rect
	title     Rect
	chevron   Rect
	tabs      []Rect
	edge      int
	collapsed bool // rolled up (and not peeking)
	rolled    bool // rolled up in config (peeking or not)
	scrolls   bool
	folder    bool // live-folder plot: Plots handles clicks on its contents
}

// drag state, owned by the hook thread
var (
	dragID         string
	dragZone       int
	dragActive     bool
	dragStart      Point
	dragLatest     Point
	dragPosted     bool
	swallowUp      bool // eat the button-up that belongs to a click we handled
	swallowRUp     bool
	lastTitleClick time.Time
	lastTitleID    string
	peekID         string
)

// tab drag state (written by the hook thread, read by the main thread)
var (
	tabDragPlot   string
	tabDragIdx    int
	tabDragStart  Point
	tabDragPt     Point
	tabDragActive bool
)

// tabDragInfo returns the tab being dragged and the cursor; done clears it.
func tabDragInfo(done bool) (id string, idx int, pt Point) {
	hookMu.Lock()
	defer hookMu.Unlock()
	id, idx, pt = tabDragPlot, tabDragIdx, tabDragPt
	if done {
		tabDragPlot, tabDragActive = "", false
	}
	return
}

// plotIndex finds a plot zone by id.
func plotZoneByID(st *hookState, id string) *plotZone {
	for i := range st.plots {
		if st.plots[i].id == id {
			return &st.plots[i]
		}
	}
	return nil
}

// zoneAt: 0 = none, 9 = move (title bar), 10 = roll-up button,
// 100+n = tab n, otherwise WMSZ_* edge codes.
func zoneAt(st *hookState, pt Point) (string, int) {
	for i := len(st.plots) - 1; i >= 0; i-- {
		z := st.plots[i]
		r := z.rect
		if !r.Contains(pt) {
			continue
		}
		if z.chevron.Contains(pt) {
			return z.id, 10
		}
		for i, t := range z.tabs {
			if t.Contains(pt) {
				return z.id, 100 + i
			}
		}
		left, right := pt.X < r.L+z.edge, pt.X >= r.R-z.edge
		bottom := pt.Y >= r.B-z.edge
		if z.collapsed {
			bottom = false // a rolled-up plot can only be moved
		}
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
	hookMu.Lock()
	tabDragging := tabDragPlot != ""
	hookMu.Unlock()
	if tabDragging {
		switch msg {
		case WM_MOUSEMOVE:
			hookMu.Lock()
			tabDragPt = pt
			if !tabDragActive && (abs(pt.X-tabDragStart.X) > 6 || abs(pt.Y-tabDragStart.Y) > 6) {
				tabDragActive = true
			}
			active := tabDragActive
			hookMu.Unlock()
			if active {
				postMessage(hookMainWnd, msgTabDragMove, 0, 0)
			}
			return false
		case WM_LBUTTONUP:
			hookMu.Lock()
			tabDragPt = pt
			active := tabDragActive
			if !active {
				tabDragPlot = ""
			}
			hookMu.Unlock()
			if active {
				postMessage(hookMainWnd, msgTabDrop, 0, 0)
			}
			return true
		}
		return false
	}
	if msg == WM_LBUTTONUP && swallowUp {
		swallowUp = false
		return true
	}
	if msg == WM_MOUSEMOVE {
		hoverPeek(st, pt)
		hoverTrack(st, pt)
		return false
	}
	if msg == 0x020A { // WM_MOUSEWHEEL: scroll the plot under the cursor
		for i := len(st.plots) - 1; i >= 0; i-- {
			z := st.plots[i]
			if z.scrolls && !z.collapsed && z.rect.Contains(pt) && st.desk.IsDesktopWindow(call(pWindowFromPoint, pointArg(pt))) {
				postMessage(hookMainWnd, msgScroll, uintptr(z.idx), uintptr(wheelDelta))
				return true
			}
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
		if z := folderBodyAt(st, pt); z != nil {
			if msg == WM_RBUTTONUP {
				postMessage(hookMainWnd, msgFolderMenu, uintptr(z.idx), packPt(pt))
			}
			return true
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
			if id, z := zoneAt(st, pt); z == 10 || z >= 100 {
				zp := plotZoneByID(st, id)
				if z == 10 {
					postMessage(hookMainWnd, msgToggleRoll, uintptr(zp.idx), 0)
					swallowUp = true
				} else {
					postMessage(hookMainWnd, msgSelectTab, uintptr(zp.idx), uintptr(z-100))
					// the tab may be dragged: to reorder, onto another plot, or out
					hookMu.Lock()
					tabDragPlot, tabDragIdx, tabDragStart, tabDragPt, tabDragActive = id, z-100, pt, pt, false
					hookMu.Unlock()
				}
				lastDown = time.Time{}
				return true
			}
			if id, z := zoneAt(st, pt); z == 9 {
				// double-click the title bar rolls the plot up / down
				now := time.Now()
				if id == lastTitleID && now.Sub(lastTitleClick) <= time.Duration(call(pGetDoubleClickTime))*time.Millisecond {
					lastTitleClick = time.Time{}
					if zp := plotZoneByID(st, id); zp != nil {
						postMessage(hookMainWnd, msgToggleRoll, uintptr(zp.idx), 0)
					}
					swallowUp = true
					return true
				}
				lastTitleClick, lastTitleID = now, id
			}
			if _, z := zoneAt(st, pt); z == 0 {
				if fz := folderBodyAt(st, pt); fz != nil {
					postMessage(hookMainWnd, msgFolderClick, uintptr(fz.idx), packPt(pt))
					swallowUp = true
					lastDown = time.Time{}
					return true
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

var wheelDelta int16

// hoverPeek temporarily unrolls a rolled-up plot while the mouse is over it.
func hoverPeek(st *hookState, pt Point) {
	if peekID != "" {
		z := plotZoneByID(st, peekID)
		if z == nil || !z.rect.Contains(pt) {
			peekID = ""
			postMessage(hookMainWnd, msgPeek, ^uintptr(0), 0)
		}
		return
	}
	for i := len(st.plots) - 1; i >= 0; i-- {
		z := st.plots[i]
		if z.collapsed && z.title.Contains(pt) && !z.chevron.Contains(pt) {
			peekID = z.id
			postMessage(hookMainWnd, msgPeek, uintptr(z.idx), 0)
			return
		}
	}
}

var hoverIdx = -1

// hoverTrack tells the main thread which plot the mouse is over (for fading).
func hoverTrack(st *hookState, pt Point) {
	idx := -1
	for i := len(st.plots) - 1; i >= 0; i-- {
		if st.plots[i].rect.Contains(pt) {
			idx = st.plots[i].idx
			break
		}
	}
	if idx == hoverIdx {
		return
	}
	if idx >= 0 && !st.desk.IsDesktopWindow(call(pWindowFromPoint, pointArg(pt))) {
		idx = -1 // a window is covering the plot
		if idx == hoverIdx {
			return
		}
	}
	hoverIdx = idx
	postMessage(hookMainWnd, msgHover, uintptr(idx), 0)
}

// folderBodyAt returns the live-folder plot whose content area holds pt.
func folderBodyAt(st *hookState, pt Point) *plotZone {
	for i := len(st.plots) - 1; i >= 0; i-- {
		z := &st.plots[i]
		if !z.rect.Contains(pt) {
			continue
		}
		if !z.folder || z.collapsed || z.title.Contains(pt) {
			return nil
		}
		for _, t := range z.tabs {
			if t.Contains(pt) {
				return nil
			}
		}
		return z
	}
	return nil
}
