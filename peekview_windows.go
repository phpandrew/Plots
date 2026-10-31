//go:build windows

package main

import (
	"image"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Peek view: the Peek shortcut shows every plot above all windows, over a
// dimmed screen. Plots draws the plots and their icons itself (the real
// desktop icons can't be lifted above other windows). Double-click an icon to
// open it, right-click for its Explorer menu, click a tab to switch; Esc, the
// shortcut again, or a click on the dimmed area closes it.

type peekWin struct {
	hwnd    HWND
	plotID  string
	rect    Rect
	entries []folderEntry // what's drawn, in cell order
}

var (
	peekProc     uintptr
	peekBackdrop []HWND
	peekWins     []*peekWin
	peekSel      string
	peekLastPath string
	peekLastTime time.Time
	displayNames = map[string]string{}

	IID_IShellItem = windows.GUID{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

func (a *App) peekOpen() bool { return len(peekWins) > 0 || len(peekBackdrop) > 0 }

func (a *App) togglePeek() {
	if a.peekOpen() {
		a.closePeek()
	} else {
		a.openPeek()
	}
}

func (a *App) openPeek() {
	if a.desk == nil || a.cfg.Hidden {
		return
	}
	if peekProc == 0 {
		peekProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr { return a.peekWndProc(h, msg, wp, lp) })
		registerClass("PlotsPeek", peekProc, 0)
	}
	// dim every monitor
	for _, m := range a.mons {
		r := m.Rect
		h := createWindow(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST, "PlotsPeek", "Plots", WS_POPUP, r.L, r.T, r.W(), r.H(), 0, 0)
		call(pSetLayeredWindowAttributes, h, 0, 120, LWA_ALPHA)
		call(pShowWindow, h, SW_SHOWNA)
		peekBackdrop = append(peekBackdrop, h)
	}
	for _, v := range a.eng.Views() {
		p := a.cfg.Plot(v.ID)
		if p == nil {
			continue
		}
		h := createWindow(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST|WS_EX_NOACTIVATE, "PlotsPeek", p.Title, WS_POPUP,
			v.Rect.L, v.Rect.T, v.Rect.W(), v.Rect.H(), 0, 0)
		pw := &peekWin{hwnd: h, plotID: p.ID, rect: v.Rect}
		peekWins = append(peekWins, pw)
		a.renderPeek(pw)
		call(pSetWindowPos, h, ^uintptr(0), 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW)
	}
	if len(peekBackdrop) > 0 {
		// keyboard focus for Esc; losing it to another app closes the peek
		call(pSetForegroundWindow, peekBackdrop[0])
		call(pSetFocus, peekBackdrop[0])
	}
}

func (a *App) closePeek() {
	wins, bds := peekWins, peekBackdrop
	peekWins, peekBackdrop = nil, nil
	for _, w := range wins {
		call(pDestroyWindow, w.hwnd)
	}
	for _, h := range bds {
		call(pDestroyWindow, h)
	}
	peekSel = ""
	if len(wins) > 0 {
		releaseMemory()
	}
}

// refreshPeek re-renders the peek view (new thumbnails, tab switch).
func (a *App) refreshPeek() {
	for _, w := range peekWins {
		if v := a.eng.View(w.plotID); v != nil && v.Rect == w.rect {
			a.renderPeek(w)
		}
	}
}

func (a *App) peekEntries(p *Plot, v *PlotView) []folderEntry {
	if p.Folder != "" {
		return nil // drawn by drawFolder
	}
	out := make([]folderEntry, 0, len(v.Paths))
	for _, path := range v.Paths {
		out = append(out, folderEntry{Path: path, Name: shellDisplayName(path)})
	}
	return out
}

func (a *App) renderPeek(w *peekWin) {
	p := a.cfg.Plot(w.plotID)
	v := a.eng.View(w.plotID)
	if p == nil || v == nil {
		return
	}
	o := a.renderOpts(p, v, false)
	o.Grip = false
	o.Opacity = max(o.Opacity, 88) // readable over busy windows
	img := RenderPlot(o)
	if p.Folder != "" {
		a.drawFolder(img, p, v)
	} else if !v.Collapsed {
		w.entries = a.peekEntries(p, v)
		px := a.iconPx(v.Scale)
		cells := make([]FolderCell, 0, len(w.entries))
		for i, e := range w.entries {
			col, row := i%max(1, v.Cols), i/max(1, v.Cols)
			var icon image.Image
			if t := a.thumb(e, px); t != nil {
				icon = t
			}
			cells = append(cells, FolderCell{Icon: icon, Name: e.Name,
				X: v.Grid.X - v.Rect.L + col*v.Cell.X, Y: v.Grid.Y - v.Rect.T + row*v.Cell.Y, W: v.Cell.X, H: v.Cell.Y,
				Selected: e.Path == peekSel})
		}
		DrawFolderCells(img, cells, px, v.Scale)
	}
	pos := Point{v.Rect.L, v.Rect.T}
	pushLayered(w.hwnd, img, &pos)
}

func peekWinFor(h HWND) *peekWin {
	for _, w := range peekWins {
		if w.hwnd == h {
			return w
		}
	}
	return nil
}

// peekHit is the entry under screen point pt in a peek window.
func (a *App) peekHit(w *peekWin, pt Point) *folderEntry {
	p := a.cfg.Plot(w.plotID)
	v := a.eng.View(w.plotID)
	if p == nil || v == nil {
		return nil
	}
	if p.Folder != "" {
		return a.folderHit(p, pt)
	}
	if pt.X < v.Grid.X || pt.Y < v.Grid.Y || v.Cell.X <= 0 || v.Cell.Y <= 0 {
		return nil
	}
	col, row := (pt.X-v.Grid.X)/v.Cell.X, (pt.Y-v.Grid.Y)/v.Cell.Y
	if col >= v.Cols {
		return nil
	}
	i := row*v.Cols + col
	if i < 0 || i >= len(w.entries) {
		return nil
	}
	return &w.entries[i]
}

func (a *App) peekWndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_KEYDOWN:
		if wp == 0x1B { // Esc
			a.closePeek()
			return 0
		}
	case 0x001C: // WM_ACTIVATEAPP: another app took over
		if wp == 0 && a.peekOpen() {
			postMessage(a.main, msgClosePeek, 0, 0)
		}
	case WM_LBUTTONDOWN, WM_RBUTTONUP:
		w := peekWinFor(h)
		pt := cursorPos()
		if w == nil {
			if msg == WM_LBUTTONDOWN {
				a.closePeek() // clicked the dimmed area
			}
			return 0
		}
		v := a.eng.View(w.plotID)
		p := a.cfg.Plot(w.plotID)
		if v == nil || p == nil {
			return 0
		}
		if msg == WM_LBUTTONDOWN {
			for i, r := range v.Tabs {
				if r.Contains(pt) && i != p.ActiveTab {
					p.ActiveTab = i
					a.markDirty()
					a.tick()
					a.renderPeek(w)
					return 0
				}
			}
		}
		e := a.peekHit(w, pt)
		if e == nil {
			if peekSel != "" {
				peekSel = ""
				a.refreshPeek()
			}
			return 0
		}
		path := e.Path
		if msg == WM_RBUTTONUP {
			peekSel = path
			a.renderPeek(w)
			showShellMenu(h, path, pt)
			return 0
		}
		dbl := time.Duration(call(pGetDoubleClickTime)) * time.Millisecond
		if peekLastPath == path && time.Since(peekLastTime) <= dbl {
			peekLastTime = time.Time{}
			a.closePeek()
			openURL(path)
			return 0
		}
		peekLastPath, peekLastTime, peekSel = path, time.Now(), path
		a.refreshPeek()
		return 0
	case 0x0021: // WM_MOUSEACTIVATE: plots don't take focus from the backdrop
		if peekWinFor(h) != nil {
			return 3 // MA_NOACTIVATE
		}
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

// shellDisplayName is the name Explorer shows for a desktop item.
func shellDisplayName(path string) string {
	if n, ok := displayNames[path]; ok {
		return n
	}
	name := ""
	var item comObj
	hr := call(pSHCreateItemFromParsingName, uintptr(unsafe.Pointer(u16(path))), 0, uintptr(unsafe.Pointer(&IID_IShellItem)), uintptr(unsafe.Pointer(&item)))
	if int32(hr) >= 0 && item != 0 {
		var psz *uint16
		if hr := item.vcall(5, 0 /*SIGDN_NORMALDISPLAY*/, uintptr(unsafe.Pointer(&psz))); int32(hr) >= 0 && psz != nil {
			name = windows.UTF16PtrToString(psz)
			call(pCoTaskMemFree, uintptr(unsafe.Pointer(psz)))
		}
		item.Release()
	}
	if name == "" {
		name = path[strings.LastIndexAny(path, `\/`)+1:]
		name = displayName(name, false)
	}
	if len(displayNames) > 5000 {
		displayNames = map[string]string{}
	}
	displayNames[path] = name
	return name
}
