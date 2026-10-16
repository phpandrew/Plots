//go:build windows

package main

import (
	"fmt"
	"image"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Edit mode: every plot gets a top-most overlay window that can be dragged
// and resized with the normal Windows move/size handling.

type overlay struct {
	handle    bool
	startRect Rect  // window rect when the drag began
	startCur  Point // cursor when the drag began
	id        string
	hwnd      HWND
	key       string
	rect      Rect
	moving    bool
}

var overlayProc, toolbarProc uintptr

const (
	tbNew = 1 + iota
	tbSettings
	tbDone
)

func (a *App) overlayFor(h HWND) *overlay {
	for _, o := range a.overlays {
		if o.hwnd == h {
			return o
		}
	}
	return nil
}

func (a *App) enterEdit() {
	if a.editing {
		return
	}
	if a.cfg.Hidden {
		a.setHidden(false)
	}
	a.editing = true
	registerOverlayClass(a)
	a.tick()
	a.syncOverlays()
	a.showToolbar()
	a.updatePanels()
	a.publishHookState()
	if a.settings != nil {
		a.settings.refreshGeneral()
	}
}

func (a *App) exitEdit() {
	if !a.editing {
		return
	}
	a.editing = false
	for id, o := range a.overlays {
		call(pDestroyWindow, o.hwnd)
		delete(a.overlays, id)
	}
	if a.toolbar != 0 {
		call(pDestroyWindow, a.toolbar)
		a.toolbar = 0
	}
	a.selected = ""
	a.raiseUI()
	// plots drawn over existing icons take them in
	if items, _, err := a.readItems(); err == nil {
		a.eng.AdoptUnder(items)
	}
	a.eng.Relayout()
	a.markDirty()
	if !a.quitting {
		a.tick()
	}
	a.save(true)
	a.updatePanels()
	a.publishHookState()
	if a.settings != nil {
		a.settings.refreshGeneral()
	}
}

// syncOverlays maintains the interactive windows over each plot:
//
//	edit mode   - visible, top-most overlays (move / resize / options)
//	normal mode - invisible "handles" over the title bar and edges, sitting just
//	              above the desktop, so plots can be moved and resized any time
func (a *App) syncOverlays() {
	kind := ""
	switch {
	case a.editing:
		kind = "edit"
	}
	seen := map[string]bool{}
	var anchor uintptr
	if kind == "handle" {
		anchor = a.handleAnchor()
	}
	for _, v := range a.eng.Views() {
		p := a.cfg.Plot(v.ID)
		if p == nil || kind == "" {
			continue
		}
		seen[v.ID] = true
		o := a.overlays[v.ID]
		if o != nil && (o.handle != (kind == "handle") || !isWindow(o.hwnd)) {
			call(pDestroyWindow, o.hwnd)
			o = nil
		}
		if o == nil {
			ex := uintptr(WS_EX_LAYERED | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE)
			if kind == "edit" {
				ex |= WS_EX_TOPMOST
			}
			h := createWindow(ex, "PlotsOverlay", p.Title, WS_POPUP, v.Rect.L, v.Rect.T, v.Rect.W(), v.Rect.H(), 0, 0)
			o = &overlay{id: v.ID, hwnd: h, handle: kind == "handle"}
			a.overlays[v.ID] = o
		}
		if o.moving {
			continue
		}
		opts := a.renderOpts(p, v, true)
		key := fmt.Sprintf("%s|%v|%s|%v", kind, v.Rect, a.lookKey(opts), opts.Selected)
		pos := Point{v.Rect.L, v.Rect.T}
		if key != o.key {
			after := ^uintptr(0) // HWND_TOPMOST
			if o.handle {
				after = anchor
			}
			call(pSetWindowPos, o.hwnd, after, uintptr(pos.X), uintptr(pos.Y), uintptr(v.Rect.W()), uintptr(v.Rect.H()), SWP_NOACTIVATE|SWP_SHOWWINDOW)
			if o.handle {
				pushLayered(o.hwnd, renderHandle(v.Rect.W(), v.Rect.H(), a.cfg.TitleH(v.Scale), int(math.Round(8*v.Scale))), &pos)
			} else {
				pushLayered(o.hwnd, RenderPlot(opts), &pos)
			}
			o.key, o.rect = key, v.Rect
		} else if o.handle {
			// keep handles directly above the desktop, under every app window
			call(pSetWindowPos, o.hwnd, anchor, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
		}
	}
	for id, o := range a.overlays {
		if !seen[id] {
			call(pDestroyWindow, o.hwnd)
			delete(a.overlays, id)
		}
	}
	if kind == "edit" {
		a.raiseUI()
	}
}

// handleAnchor returns the window directly above the desktop in z-order;
// handles are inserted right below it (i.e. just above the desktop).
func (a *App) handleAnchor() uintptr {
	if a.desk == nil {
		return 1 // HWND_BOTTOM
	}
	root := call(pGetAncestor, a.desk.DefView, GA_ROOT)
	prev := call(pGetWindow, root, 3 /*GW_HWNDPREV*/)
	for prev != 0 && a.overlayFor(prev) != nil {
		prev = call(pGetWindow, prev, 3)
	}
	return prev // 0 = HWND_TOP when nothing is above the desktop
}

// renderHandle makes a nearly invisible hit area (alpha 1) over the title bar
// and edges; the body stays fully transparent so clicks reach the icons.
func renderHandle(w, h, th, e int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	set := func(x0, y0, x1, y1 int) {
		for y := max(0, y0); y < min(h, y1); y++ {
			for x := max(0, x0); x < min(w, x1); x++ {
				img.Pix[y*img.Stride+x*4+3] = 1
			}
		}
	}
	set(0, 0, w, th)  // title bar
	set(0, 0, e, h)   // left
	set(w-e, 0, w, h) // right
	set(0, h-e, w, h) // bottom
	return img
}

// raiseUI keeps the toolbar and settings window above the edit overlays.
func (a *App) raiseUI() {
	const flags = SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE
	if a.settings != nil && isWindow(a.settings.hwnd) {
		after := ^uintptr(1) // HWND_NOTOPMOST
		if a.editing {
			after = ^uintptr(0) // HWND_TOPMOST
		}
		call(pSetWindowPos, a.settings.hwnd, after, 0, 0, 0, 0, flags)
	}
	if a.toolbar != 0 {
		call(pSetWindowPos, a.toolbar, ^uintptr(0), 0, 0, 0, 0, flags)
	}
}

func (a *App) renderOverlayLive(o *overlay) {
	p := a.cfg.Plot(o.id)
	if p == nil {
		return
	}
	r := windowRect(o.hwnd)
	m := monitorAt(a.mons, r.Center())
	v := &PlotView{Rect: r, Scale: m.Scale}
	pushLayered(o.hwnd, RenderPlot(a.renderOpts(p, v, true)), nil)
	o.key = ""
}

func (a *App) overlayWndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	o := a.overlayFor(h)
	switch msg {
	case WM_NCHITTEST:
		if o == nil {
			break
		}
		pt := Point{int(getXLParam(lp)), int(getYLParam(lp))}
		r := windowRect(h)
		m := monitorAt(a.mons, pt)
		e := int(math.Round(8 * m.Scale))
		left, right := pt.X < r.L+e, pt.X >= r.R-e
		top, bottom := pt.Y < r.T+e, pt.Y >= r.B-e
		switch {
		case top && left:
			return HTTOPLEFT
		case top && right:
			return HTTOPRIGHT
		case bottom && left:
			return HTBOTTOMLEFT
		case bottom && right:
			return HTBOTTOMRIGHT
		case left:
			return HTLEFT
		case right:
			return HTRIGHT
		case top:
			return HTTOP
		case bottom:
			return HTBOTTOM
		}
		return HTCAPTION
	case 0x0021: // WM_MOUSEACTIVATE
		return 3 // MA_NOACTIVATE
	case 0x00A1: // WM_NCLBUTTONDOWN
		if o != nil && !o.handle && a.selected != o.id {
			a.selected = o.id
			a.syncOverlays()
			if a.settings != nil {
				a.settings.selectPlot(o.id)
			}
		}
	case 0x0231: // WM_ENTERSIZEMOVE
		if o != nil {
			o.moving = true
			o.startRect, o.startCur = windowRect(h), cursorPos()
			if o.handle {
				// show the plot while it is dragged; hide the desktop panel underneath
				a.renderOverlayLive(o)
				if pn := a.panels[o.id]; pn != nil {
					call(pShowWindow, pn.hwnd, SW_HIDE)
					a.repaintDesktop(pn.rect)
				}
			}
		}
	case WM_SIZE:
		if o != nil && o.moving {
			a.renderOverlayLive(o)
		}
	case 0x0214: // WM_SIZING
		if o != nil && a.cfg.SnapToGrid && !altDown() {
			// Windows feeds incremental rects; rebuild from the drag origin so
			// snapping never swallows small mouse movements.
			rc := (*RECT)(unsafe.Pointer(lp))
			d := cursorPos().Sub(o.startCur)
			r := o.startRect
			switch int(wp) {
			case 1, 4, 7:
				r.L += d.X
			case 2, 5, 8:
				r.R += d.X
			}
			switch int(wp) {
			case 3, 4, 5:
				r.T += d.Y
			case 6, 7, 8:
				r.B += d.Y
			}
			*rc = RECT{int32(r.L), int32(r.T), int32(r.R), int32(r.B)}
			a.snapSize(o, int(wp), rc)
		}
		return 1
	case 0x0216: // WM_MOVING
		if o != nil && a.cfg.SnapToGrid && !altDown() {
			rc := (*RECT)(unsafe.Pointer(lp))
			d := cursorPos().Sub(o.startCur)
			r := o.startRect
			*rc = RECT{int32(r.L + d.X), int32(r.T + d.Y), int32(r.R + d.X), int32(r.B + d.Y)}
			a.snapMove(o, rc)
		}
		return 1
	case WM_EXITSIZEMOVE:
		defer a.raiseUI()
		if o != nil {
			o.moving = false
			if p := a.cfg.Plot(o.id); p != nil {
				a.setPlotScreenRect(p, windowRect(h))
				a.eng.Relayout()
				a.tick()
			}
		}
		return 0
	case WM_GETMINMAXINFO:
		m := monitorAt(a.mons, cursorPos())
		mmi := (*[10]int32)(unsafe.Pointer(lp)) // MINMAXINFO: ptMinTrackSize at index 6,7
		mmi[6] = int32(140 * m.Scale)
		mmi[7] = int32(90 * m.Scale)
		return 0
	case WM_NCLBUTTONDBLCLK:
		if o != nil {
			a.runCmd(cmdRename, o.id)
		}
		return 0
	case WM_NCRBUTTONDOWN:
		return 0
	case WM_NCRBUTTONUP:
		if o != nil {
			a.selected = o.id
			a.syncOverlays()
			a.plotMenu(o.id, cursorPos())
		}
		return 0
	case WM_CONTEXTMENU:
		return 0
	case 0x0112: // WM_SYSCOMMAND: block maximize via caption double-click etc.
		if wp&0xFFF0 == 0xF030 {
			return 0
		}
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

// ------------------------------------------------------------------ toolbar

func (a *App) showToolbar() {
	if a.toolbar != 0 && isWindow(a.toolbar) {
		return
	}
	pm := primaryMonitor(a.mons)
	s := pm.Scale
	bw, bh, gap := int(110*s), int(32*s), int(8*s)
	cw := 3*bw + 4*gap
	ch := bh + 2*gap
	// outer size including caption
	w := cw + int(16*s)
	h := ch + int(40*s)
	x := pm.Work.L + (pm.Work.W()-w)/2
	y := pm.Work.T + int(16*s)
	a.toolbar = createWindow(WS_EX_TOPMOST|WS_EX_TOOLWINDOW, "PlotsToolbar", "Plots — edit mode",
		WS_POPUP|WS_CAPTION|WS_SYSMENU|WS_VISIBLE, x, y, w, h, 0, 0)
	var cr RECT
	call(pGetClientRect, a.toolbar, uintptr(unsafe.Pointer(&cr)))
	ox := (int(cr.Right) - cw) / 2
	font := uiFont(int(math.Round(96 * s)))
	for i, lbl := range []string{"+ New plot", "Settings…", "Done"} {
		style := uintptr(WS_CHILD | WS_VISIBLE | WS_TABSTOP | BS_PUSHBUTTON)
		if i == 2 {
			style |= BS_DEFPUSHBUTTON
		}
		b := createWindow(0, "BUTTON", lbl, style, ox+gap+i*(bw+gap), gap, bw, bh, a.toolbar, uintptr(tbNew+i))
		sendMessage(b, WM_SETFONT, font, 1)
	}
}

func (a *App) toolbarWndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_COMMAND:
		switch int(loword(wp)) {
		case tbNew:
			pm := primaryMonitor(a.mons)
			a.newPlot(pm.Work.Center())
		case tbSettings:
			a.showSettings(a.selected)
		case tbDone:
			a.exitEdit()
		}
		return 0
	case WM_CLOSE:
		a.exitEdit()
		return 0
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

// ------------------------------------------------------------------ create / delete

func (a *App) newPlot(near Point) {
	m := monitorAt(a.mons, near)
	s := m.Scale
	w, h := int(400*s), int(240*s)
	x, y := m.Work.L+(m.Work.W()-w)/2, m.Work.T+(m.Work.H()-h)/2
	// cascade so new plots don't stack exactly
	for tries := 0; tries < 20; tries++ {
		clash := false
		for _, v := range a.eng.Views() {
			if v.Rect.L == x && v.Rect.T == y {
				clash = true
			}
		}
		if !clash {
			break
		}
		x += int(30 * s)
		y += int(30 * s)
	}
	p := &Plot{ID: NewID(), Title: fmt.Sprintf("Plot %d", len(a.cfg.Plots)+1), Color: a.cfg.NextColor(), Opacity: 60}
	a.cfg.Plots = append(a.cfg.Plots, p)
	a.setPlotScreenRect(p, RectWH(x, y, w, h))
	a.selected = p.ID
	a.eng.Relayout()
	if !a.editing {
		a.enterEdit()
	} else {
		a.tick()
	}
	a.save(true)
	if a.settings != nil {
		a.settings.refreshList()
		a.settings.selectPlot(p.ID)
	}
}

func (a *App) deletePlot(id string, owner HWND) {
	p := a.cfg.Plot(id)
	if p == nil {
		return
	}
	msg := fmt.Sprintf("Delete the plot \"%s\"?\n\nIts icons stay on the desktop where they are; nothing is deleted from disk.", p.Title)
	if messageBox(owner, msg, "Plots", MB_YESNO|MB_ICONWARNING) != IDYES {
		return
	}
	a.cfg.RemovePlot(id)
	if a.selected == id {
		a.selected = ""
	}
	a.markDirty()
	a.eng.Relayout()
	a.tick()
	a.save(true)
	if a.settings != nil {
		a.settings.refreshList()
	}
}

// ------------------------------------------------------------------ snapping

func altDown() bool { return call(pGetKeyState, 0x12 /*VK_MENU*/)&0x8000 != 0 }

const (
	snapDist = 12
	cellSnap = 16
)

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// snapSize makes plot sizes whole numbers of icon cells (so plots line up and
// match each other) and pulls the dragged edges onto nearby plot edges.
func (a *App) snapSize(o *overlay, edge int, rc *RECT) {
	m := monitorAt(a.mons, Point{int(rc.Left+rc.Right) / 2, int(rc.Top+rc.Bottom) / 2})
	sx, sy := max(a.eng.Spacing.X, 16), max(a.eng.Spacing.Y, 16)
	pad, th := a.cfg.Pad(m.Scale), a.cfg.TitleH(m.Scale)
	w, h := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	// magnet to whole icon cells (uniform plots) only when close; otherwise free
	nw, nh := w, h
	if cw := 2*pad + max(1, int(math.Round(float64(w-2*pad)/float64(sx))))*sx; abs(cw-w) <= cellSnap {
		nw = cw
	}
	if ch := th + 2*pad + max(1, int(math.Round(float64(h-th-2*pad)/float64(sy))))*sy; abs(ch-h) <= cellSnap {
		nh = ch
	}
	left := edge == 1 || edge == 4 || edge == 7
	top := edge == 3 || edge == 4 || edge == 5
	horiz := edge != 3 && edge != 6
	vert := edge != 1 && edge != 2
	if horiz {
		if left {
			rc.Left = rc.Right - int32(nw)
		} else {
			rc.Right = rc.Left + int32(nw)
		}
	}
	if vert {
		if top {
			rc.Top = rc.Bottom - int32(nh)
		} else {
			rc.Bottom = rc.Top + int32(nh)
		}
	}
	// magnet to other plots' edges
	for _, r := range a.otherRects(o.id) {
		if horiz {
			if left {
				snapEdge(&rc.Left, r.L, r.R)
			} else {
				snapEdge(&rc.Right, r.L, r.R)
			}
		}
		if vert {
			if top {
				snapEdge(&rc.Top, r.T, r.B)
			} else {
				snapEdge(&rc.Bottom, r.T, r.B)
			}
		}
	}
}

func snapEdge(v *int32, targets ...int) {
	for _, t := range targets {
		if math.Abs(float64(int(*v)-t)) <= snapDist {
			*v = int32(t)
			return
		}
	}
}

// snapMove aligns a dragged plot with other plots (edges, with a small gap)
// and with the monitor's work area, otherwise to a 10px grid.
func (a *App) snapMove(o *overlay, rc *RECT) {
	w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
	m := monitorAt(a.mons, Point{int(rc.Left+rc.Right) / 2, int(rc.Top+rc.Bottom) / 2})
	gap := int(math.Round(12 * m.Scale))
	x, y := int(rc.Left), int(rc.Top)
	bestX, bestY := snapDist+1, snapDist+1
	nx, ny := x, y
	try := func(cur, target, result int, best *int, out *int) {
		if d := abs(cur - target); d < *best {
			*best, *out = d, result
		}
	}
	xs := []int{m.Work.L, m.Work.L + gap}
	ys := []int{m.Work.T, m.Work.T + gap}
	xe := []int{m.Work.R, m.Work.R - gap}
	ye := []int{m.Work.B, m.Work.B - gap}
	for _, r := range a.otherRects(o.id) {
		xs = append(xs, r.L, r.R+gap)
		xe = append(xe, r.R, r.L-gap)
		ys = append(ys, r.T, r.B+gap)
		ye = append(ye, r.B, r.T-gap)
	}
	for _, t := range xs {
		try(x, t, t, &bestX, &nx)
	}
	for _, t := range xe {
		try(x+int(w), t, t-int(w), &bestX, &nx)
	}
	for _, t := range ys {
		try(y, t, t, &bestY, &ny)
	}
	for _, t := range ye {
		try(y+int(h), t, t-int(h), &bestY, &ny)
	}
	rc.Left, rc.Top = int32(nx), int32(ny)
	rc.Right, rc.Bottom = rc.Left+w, rc.Top+h
}

func (a *App) otherRects(id string) []Rect {
	var out []Rect
	for _, v := range a.eng.Views() {
		if v.ID != id {
			if ov := a.overlays[v.ID]; ov != nil && ov.hwnd != 0 {
				out = append(out, windowRect(ov.hwnd))
			} else {
				out = append(out, v.Rect)
			}
		}
	}
	return out
}

// ------------------------------------------------------------------ move/resize without edit mode
// The mouse hook detects a press on a plot's title bar or edge; the plot is
// then shown as a floating preview that follows the mouse until release.

func (a *App) dragStart() {
	id, zone, start, _ := dragInfo()
	v := a.eng.View(id)
	p := a.cfg.Plot(id)
	if v == nil || p == nil {
		return
	}
	registerOverlayClass(a)
	h := createWindow(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST|WS_EX_NOACTIVATE|WS_EX_TRANSPARENT, "PlotsOverlay", p.Title, WS_POPUP,
		v.Rect.L, v.Rect.T, v.Rect.W(), v.Rect.H(), 0, 0)
	o := &overlay{id: id, hwnd: h, moving: true, startRect: v.Rect, startCur: start, rect: v.Rect}
	a.drag, a.dragZone = o, zone
	pos := Point{v.Rect.L, v.Rect.T}
	call(pSetWindowPos, h, ^uintptr(0), uintptr(pos.X), uintptr(pos.Y), uintptr(v.Rect.W()), uintptr(v.Rect.H()), SWP_NOACTIVATE|SWP_SHOWWINDOW)
	pushLayered(h, RenderPlot(a.renderOpts(p, v, true)), &pos)
	if pn := a.panels[id]; pn != nil {
		call(pShowWindow, pn.hwnd, SW_HIDE)
		a.repaintDesktop(pn.rect)
	}
}

func (a *App) dragMove() {
	o := a.drag
	if o == nil {
		return
	}
	_, _, _, cur := dragInfo()
	d := cur.Sub(o.startCur)
	r := o.startRect
	switch a.dragZone {
	case 9:
		r = Rect{r.L + d.X, r.T + d.Y, r.R + d.X, r.B + d.Y}
	default:
		switch a.dragZone {
		case 1, 7:
			r.L += d.X
		case 2, 8:
			r.R += d.X
		}
		if a.dragZone >= 6 {
			r.B += d.Y
		}
	}
	m := monitorAt(a.mons, r.Center())
	minW, minH := int(140*m.Scale), int(90*m.Scale)
	if r.W() < minW {
		if a.dragZone == 1 || a.dragZone == 7 {
			r.L = r.R - minW
		} else {
			r.R = r.L + minW
		}
	}
	if r.H() < minH {
		r.B = r.T + minH
	}
	rc := RECT{int32(r.L), int32(r.T), int32(r.R), int32(r.B)}
	if a.cfg.SnapToGrid && !altDown() {
		if a.dragZone == 9 {
			a.snapMove(o, &rc)
		} else {
			a.snapSize(o, a.dragZone, &rc)
		}
	}
	r = Rect{int(rc.Left), int(rc.Top), int(rc.Right), int(rc.Bottom)}
	if r == o.rect {
		return
	}
	pos := Point{r.L, r.T}
	if r.W() != o.rect.W() || r.H() != o.rect.H() {
		if p := a.cfg.Plot(o.id); p != nil {
			v := &PlotView{Rect: r, Scale: m.Scale}
			call(pSetWindowPos, o.hwnd, ^uintptr(0), uintptr(pos.X), uintptr(pos.Y), uintptr(r.W()), uintptr(r.H()), SWP_NOACTIVATE)
			pushLayered(o.hwnd, RenderPlot(a.renderOpts(p, v, true)), &pos)
		}
	} else {
		call(pSetWindowPos, o.hwnd, ^uintptr(0), uintptr(pos.X), uintptr(pos.Y), 0, 0, SWP_NOACTIVATE|SWP_NOSIZE)
	}
	o.rect = r
}

func (a *App) dragEnd() {
	a.dragMove()
	o := a.drag
	a.drag = nil
	if o == nil {
		return
	}
	call(pDestroyWindow, o.hwnd)
	if o.rect != o.startRect {
		if p := a.cfg.Plot(o.id); p != nil {
			a.setPlotScreenRect(p, o.rect)
			a.eng.Relayout()
		}
	}
	a.tick()
	a.save(true)
}

func registerOverlayClass(a *App) {
	if overlayProc == 0 {
		overlayProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr { return a.overlayWndProc(h, msg, wp, lp) })
		toolbarProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr { return a.toolbarWndProc(h, msg, wp, lp) })
		registerClass("PlotsOverlay", overlayProc, 0)
		registerClass("PlotsToolbar", toolbarProc, appIcon(32))
	}
}
