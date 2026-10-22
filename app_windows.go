//go:build windows

package main

import (
	"errors"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	msgTray         = WM_APP + 1
	msgPlotMenu     = WM_APP + 2
	msgToggleHide   = WM_APP + 3
	msgShowSettings = WM_APP + 4
	msgDragStart    = WM_APP + 20
	msgDragMove     = WM_APP + 21
	msgDragEnd      = WM_APP + 22
	msgToggleRoll   = WM_APP + 23
	msgSelectTab    = WM_APP + 24
	msgScroll       = WM_APP + 25
	msgPeek         = WM_APP + 26
	msgHover        = WM_APP + 27
	msgTabDragMove  = WM_APP + 28
	msgTabDrop      = WM_APP + 29
	msgClosePeek    = WM_APP + 35
	timerFade       = 3

	timerTick = 1
)

type panel struct {
	hwnd HWND
	key  string
	rect Rect
	hbm  uintptr // composed bitmap for WM_PAINT modes
	w, h int
	plot *image.RGBA // last render, before fading and wallpaper blending
	fade float64     // current strength 0..1 (1 = fully shown)
}

type App struct {
	cfgPath string
	cfg     *Config
	eng     *Engine
	desk    *Desktop
	fv      *FolderView
	mons    []Monitor

	main           HWND
	panels         map[string]*panel
	editing        bool
	overlays       map[string]*overlay
	toolbar        HWND
	selected       string
	settings       *Settings
	trayIcon       uintptr
	taskbarCreated uint32

	lastSave       time.Time
	ticks          int
	status         string
	dl             *DLWatcher
	quitting       bool
	folders        map[string]*folderState
	updStatus      string
	updAvailable   bool
	startupChecked bool
	drag           *overlay
	dragZone       int
	lastCount      int
	backupMsg      string
	peekErr        string
	hoverID        string
	dropTarget     string // plot highlighted as a merge target while dragging
	tabGhost       HWND
	reopenPage     string
	shutDown       bool
}

var app *App

func init() { runtime.LockOSThread() }

func main() {
	call(pSetProcessDpiAwarenessCtx, ^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)

	child, afterUpdate := false, false
	for _, a := range os.Args[1:] {
		switch a {
		case childFlag:
			child = true
		case "--after-update":
			afterUpdate = true
		}
	}
	if !child {
		supervise(afterUpdate)
		return
	}
	runApp(afterUpdate)
}

// runApp is the app itself (the --child process).
func runApp(afterUpdate bool) {
	captureCrashes()
	for tries := 0; ; tries++ {
		h, _, err := pCreateMutexW.Call(0, 0, up("Local\\PlotsDesktopGroups"))
		if h == 0 || err != windows.ERROR_ALREADY_EXISTS {
			break
		}
		if afterUpdate && tries < 50 {
			// the previous version is still shutting down
			windows.CloseHandle(windows.Handle(h))
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if w := findWindow("PlotsMain"); w != 0 {
			postMessage(w, msgShowSettings, 0, 0)
		}
		return
	}
	call(pCoInitializeEx, 0, COINIT_APARTMENTTHREADED)
	icc := [2]uint32{8, 0x4 | 0x20 | 0x1} // ICC_BAR_CLASSES | ICC_STANDARD_CLASSES | ICC_LISTVIEW
	call(pInitCommonControlsEx, uintptr(unsafe.Pointer(&icc)))

	dir := plotsDir()
	// keep the previous run's log: after a crash it shows what happened last
	os.Rename(filepath.Join(dir, "plots.log"), filepath.Join(dir, "plots.prev.log"))
	if f, err := os.OpenFile(filepath.Join(dir, "plots.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		log.SetOutput(f)
	}
	log.Printf("Plots %s starting", version)
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		log.Println("config:", err)
	}
	a := &App{cfgPath: cfgPath, cfg: cfg, panels: map[string]*panel{}, overlays: map[string]*overlay{}}
	app = a
	a.applyLanguage()
	a.applyTheme()
	a.eng = NewEngine(cfg, a)
	a.eng.Logf = log.Printf
	a.eng.FolderDrop = a.moveIntoFolder
	a.mons = Monitors()
	a.dl = NewDLWatcher(cfg)
	a.run()
}

// ------------------------------------------------------------------ Env

func (a *App) PlotRect(p *Plot) (Rect, float64) {
	m, ok := monitorByName(a.mons, p.Monitor)
	r := RectWH(m.Rect.L+p.X, m.Rect.T+p.Y, p.W, p.H)
	if !ok {
		// monitor disconnected: keep the plot visible on the primary monitor
		if r.R > m.Work.R {
			r = RectWH(m.Work.R-p.W, r.T, p.W, p.H)
		}
		if r.B > m.Work.B {
			r = RectWH(r.L, m.Work.B-p.H, p.W, p.H)
		}
		if r.L < m.Work.L {
			r = RectWH(m.Work.L, r.T, p.W, p.H)
		}
		if r.T < m.Work.T {
			r = RectWH(r.L, m.Work.T, p.W, p.H)
		}
	}
	return r, m.Scale
}

func (a *App) FileGone(path string) bool {
	if len(path) < 3 || path[1] != ':' && !strings.HasPrefix(path, `\\`) {
		return false // virtual items (This PC, Recycle Bin...)
	}
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

// setPlotScreenRect stores a screen rect for a plot, relative to the monitor
// containing its centre.
func (a *App) setPlotScreenRect(p *Plot, r Rect) {
	m := monitorAt(a.mons, r.Center())
	p.Monitor = m.Name
	p.X, p.Y = r.L-m.Rect.L, r.T-m.Rect.T
	p.W, p.H = r.W(), r.H()
	a.markDirty()
}

func (a *App) markDirty() { a.eng.Dirty = true }

func (a *App) save(force bool) {
	if !a.eng.Dirty {
		return
	}
	if !force && time.Since(a.lastSave) < 2*time.Second {
		return
	}
	if err := SaveConfig(a.cfgPath, a.cfg); err != nil {
		log.Println("save:", err)
		return
	}
	a.eng.Dirty = false
	a.lastSave = time.Now()
}

// ------------------------------------------------------------------ main window

var mainProc, panelProc uintptr

func (a *App) run() {
	mainProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr { return a.mainWndProc(h, msg, wp, lp) })
	panelProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr { return a.panelWndProc(h, msg, wp, lp) })

	a.trayIcon = appIcon(16)
	registerClass("PlotsMain", mainProc, appIcon(32))
	registerClass("PlotsPanel", panelProc, 0)
	a.main = createWindow(WS_EX_TOOLWINDOW, "PlotsMain", "Plots", WS_POPUP, 0, 0, 0, 0, 0, 0)
	a.taskbarCreated = uint32(call(pRegisterWindowMessageW, up("TaskbarCreated")))

	a.addTray()
	a.registerPeek()
	a.attach()
	go a.applyAutostart()
	a.publishHookState()
	startMouseHook(a.main)
	startDesktopWatcher(a.main)
	call(pSetTimer, a.main, timerTick, uintptr(a.cfg.PollMs), 0)
	a.tick()
	if len(a.cfg.Plots) == 0 {
		// first run: start in edit mode with one plot so there's something to see
		a.newPlot(cursorPos())
	}

	var m MSG
	for {
		r := call(pGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		if m.Message == 0x020A /*WM_MOUSEWHEEL*/ && a.settings != nil && a.settings.wantsWheel(m.Hwnd) {
			a.settings.wheel(m.WParam)
			continue
		}
		if a.settings != nil && a.settings.hwnd != 0 && call(pIsDialogMessageW, a.settings.hwnd, uintptr(unsafe.Pointer(&m))) != 0 {
			continue
		}
		call(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
	a.shutdown()
}

func (a *App) shutdown() {
	if a.shutDown {
		return
	}
	a.shutDown = true
	if a.desk != nil && isWindow(a.desk.ListView) {
		call(pShowWindow, a.desk.ListView, SW_SHOWNA)
	}
	// bring hidden icons (other tabs, rolled-up, scrolled) back on screen
	if a.fv != nil && a.desk != nil {
		if _, origin, err := a.readItems(); err == nil {
			moves := a.eng.UnparkAll()
			m := make(map[string]Point, len(moves))
			for _, mv := range moves {
				m[mv.Path] = mv.To.Sub(origin)
			}
			a.fv.Position(m)
		}
	}
	a.restoreIcons()
	for id := range a.panels {
		a.destroyPanel(id)
	}
	a.removeTray()
	a.eng.Dirty = true
	a.save(true)
	a.fv.Close()
}

func (a *App) quit() {
	a.quitting = true
	if a.editing {
		a.exitEdit()
	}
	call(pPostQuitMessage, 0)
}

func (a *App) mainWndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	if forwardMenuMsg(msg, wp, lp) {
		return 0
	}
	switch msg {
	case msgThumbsReady:
		a.updatePanels()
		a.refreshPeek()
		return 0
	case msgClosePeek:
		a.closePeek()
		return 0
	case msgFolderClick:
		a.onFolderClick(wp, lp)
		return 0
	case msgFolderMenu:
		a.onFolderMenu(wp, lp)
		return 0
	case WM_HOTKEY:
		if wp == hotkeyPeek {
			a.peek()
		}
		return 0
	case WM_TIMER:
		if wp == timerFade {
			a.fadeStep()
			return 0
		}
		if wp == timerTick {
			a.tick()
		}
		if wp == timerVerify {
			call(pKillTimer, a.main, timerVerify)
			// give Explorer a moment to show the change on its own first
			a.verifyDesktopView()
			a.tick()
		}
		return 0
	case msgTray:
		switch uint32(loword(lp)) {
		case WM_RBUTTONUP, WM_CONTEXTMENU:
			a.trayMenu()
		case WM_USER + 5: // NIN_BALLOONUSERCLICK: the update notification
			a.showSettings("")
			if a.settings != nil {
				a.settings.showPage("about")
			}
		case WM_LBUTTONDBLCLK, WM_LBUTTONUP:
			a.showSettings("")
		}
		return 0
	case msgPlotMenu:
		a.plotMenu(hookMenuPlot(), cursorPos())
		return 0
	case msgToggleHide:
		a.setHidden(!a.cfg.Hidden)
		return 0
	case msgDragStart:
		a.dragStart()
		return 0
	case msgDragMove:
		a.dragMove()
		return 0
	case msgDragEnd:
		a.dragEnd()
		return 0
	case msgToggleRoll:
		if p := a.plotAtIndex(wp); p != nil {
			p.Collapsed = !p.Collapsed
			a.eng.Peek = ""
			a.markDirty()
			a.tick()
		}
		return 0
	case msgSelectTab:
		if p := a.plotAtIndex(wp); p != nil && int(lp) < len(p.Tabs) {
			p.ActiveTab, p.Scroll = int(lp), 0
			a.markDirty()
			a.tick()
		}
		return 0
	case msgScroll:
		if p := a.plotAtIndex(wp); p != nil {
			if int16(lp) > 0 {
				p.Scroll--
			} else {
				p.Scroll++
			}
			if p.Scroll < 0 {
				p.Scroll = 0
			}
			a.tick()
		}
		return 0
	case msgTabDragMove:
		if id, i, pt := tabDragInfo(false); id != "" {
			a.showTabGhost(id, i, pt)
		}
		return 0
	case msgTabDrop:
		id, i, pt := tabDragInfo(true)
		if id != "" {
			a.onTabDrop(id, i, pt)
		}
		return 0
	case msgHover:
		id := ""
		if p := a.plotAtIndex(wp); p != nil {
			id = p.ID
		} else if int(wp) == -2 {
			id = "*icon*" // a desktop icon outside the plots: show the icons
		}
		if id != a.hoverID {
			a.hoverID = id
			if a.cfg.FadeIdle {
				a.startFade()
			}
		}
		return 0
	case msgPeek:
		if p := a.plotAtIndex(wp); p != nil {
			a.eng.Peek = p.ID
		} else {
			a.eng.Peek = ""
		}
		a.tick()
		return 0
	case msgUpdateResult:
		a.onUpdateResult(wp == 1)
		return 0
	case msgDesktopChanged:
		call(pSetTimer, a.main, timerVerify, uintptr(a.cfg.PollMs), 0) // debounce bursts of changes
		return 0
	case WM_APP + 11:
		call(pSetTimer, a.main, timerTick, uintptr(a.cfg.PollMs), 0)
		return 0
	case 0x0011: // WM_QUERYENDSESSION: Windows is shutting down or signing out
		return 1
	case 0x0016: // WM_ENDSESSION
		if wp != 0 {
			// the process ends right after this: put hidden icons back and save
			log.Println("Windows is ending the session; saving and restoring icons")
			a.quitting = true
			a.shutdown()
		}
		return 0
	case msgReopenSettings:
		a.reopenSettings()
		return 0
	case msgShowSettings:
		a.showSettings("")
		return 0
	case WM_DISPLAYCHANGE, WM_SETTINGCHANGE, WM_DPICHANGED:
		if msg == WM_SETTINGCHANGE && lp != 0 && windows.UTF16PtrToString((*uint16)(unsafe.Pointer(lp))) == "ImmersiveColorSet" {
			// Windows switched between light and dark
			if a.cfg.Theme == "" {
				a.themeChanged()
			}
			return 0
		}
		a.mons = Monitors()
		a.eng.Relayout()
		// Explorer re-lays out the desktop after a display change; give it a moment.
		time.AfterFunc(1500*time.Millisecond, func() { postMessage(a.main, WM_APP+9, 0, 0) })
		return 0
	case WM_APP + 9:
		a.mons = Monitors()
		a.eng.Relayout()
		a.tick()
		if a.editing {
			a.syncOverlays()
		}
		return 0
	case WM_DESTROY:
		call(pPostQuitMessage, 0)
		return 0
	}
	if a.taskbarCreated != 0 && msg == a.taskbarCreated {
		// Explorer restarted
		a.addTray()
		a.detach()
		a.attach()
		return 0
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

// ------------------------------------------------------------------ desktop attach

func (a *App) attach() {
	a.desk = FindDesktop()
	if a.desk == nil {
		a.status = T("Could not find the desktop window.")
		return
	}
	if a.cfg.Hidden {
		call(pShowWindow, a.desk.ListView, SW_HIDE)
	} else {
		call(pShowWindow, a.desk.ListView, SW_SHOWNA)
	}
	resetIconAlpha(a.desk)
	log.Printf("desktop: progman=%x defview=%x list=%x host=%x(%s) after=%x",
		a.desk.Progman, a.desk.DefView, a.desk.ListView, a.desk.Host, className(a.desk.Host), a.desk.InsertAfter)
	dumpTree("Progman children", a.desk.Progman)
}

func (a *App) detach() {
	for id := range a.panels {
		a.destroyPanel(id)
	}
	a.fv.Close()
	a.fv = nil
	a.desk = nil
	a.eng.Relayout()
}

func (a *App) readItems() ([]Item, Point, error) {
	if a.fv == nil {
		fv, err := OpenFolderView()
		if err != nil {
			return nil, Point{}, err
		}
		a.fv = fv
		if a.cfg.ManageDesktopFlags {
			a.fv.EnsureFreeLayout()
		}
		a.eng.Spacing = a.fv.Spacing()
		a.eng.IconW = int(math.Round(float64(a.fv.IconSize()) * primaryMonitor(a.mons).Scale))
		log.Printf("folder view ready, spacing=%v icon=%d", a.eng.Spacing, a.eng.IconW)
	}
	items, err := a.fv.Items()
	if err != nil {
		a.fv.Close()
		a.fv = nil
		return nil, Point{}, err
	}
	origin := a.desk.ListOrigin()
	for i := range items {
		items[i].Pos = items[i].Pos.Add(origin)
	}
	return items, origin, nil
}

func (a *App) tick() {
	// never do desktop work while a plot is being dragged: it would stall the drag
	if a.drag != nil {
		return
	}
	for _, o := range a.overlays {
		if o.moving {
			return
		}
	}
	a.ticks++
	if a.ticks > 20 {
		a.scheduledBackup()
		a.checkSnapshotRequest()
	}
	if !a.desk.Valid() {
		a.detach()
		a.attach()
		if !a.desk.Valid() {
			return
		}
	}
	if a.ticks%10 == 0 {
		a.checkUpdate()
	}
	if a.ticks == 25 || a.ticks%600 == 0 {
		a.maybeAutoCheck()
	}
	if a.cfg.MoveDownloads && a.ticks%5 == 0 {
		a.dl.Scan()
	}
	if a.cfg.Hidden {
		a.save(false)
		return
	}
	items, origin, err := a.readItems()
	if err != nil {
		a.status = Tf("Desktop not ready: %s", err.Error())
		return
	}
	a.status = ""
	if a.ticks%25 == 0 && a.fv != nil {
		if a.cfg.ManageDesktopFlags {
			a.fv.EnsureFreeLayout()
		}
		a.syncIconMetrics()
	}
	a.eng.Clicks = append(a.eng.Clicks, drainClicks()...)
	for o, n := range drainRenames() {
		if a.eng.Renames == nil {
			a.eng.Renames = map[string]string{}
		}
		a.eng.Renames[o] = n
	}
	a.pruneClicks()
	if len(items) != a.lastCount {
		log.Printf("desktop items: %d -> %d", a.lastCount, len(items))
		if a.lastCount != 0 {
			// make sure removed icons don't linger on screen
			for _, p := range a.panels {
				call(pInvalidateRect, p.hwnd, 0, 0)
			}
			call(pRedrawWindow, a.desk.ListView, 0, 0, RDW_INVALIDATE|RDW_ERASE|RDW_ALLCHILDREN)
		}
		a.lastCount = len(items)
	}
	if a.renaming() {
		// Explorer is editing an icon's name (e.g. right-click > New): moving
		// icons now would cancel the rename, so wait until it is done
		return
	}
	a.eng.ParkAt = a.parkPoint()
	moves := a.eng.Reconcile(items)
	if len(moves) > 0 {
		parked := 0
		for _, mv := range moves {
			if mv.To == a.eng.ParkAt {
				parked++
			}
		}
		log.Printf("moving %d icons (%d hidden off-screen at %v)", len(moves), parked, a.eng.ParkAt)
		m := make(map[string]Point, len(moves))
		for _, mv := range moves {
			m[mv.Path] = mv.To.Sub(origin)
		}
		if err := a.fv.Position(m); err != nil {
			log.Println(err)
		}
		log.Printf("moved %d icons (e.g. %v)", len(moves), moves[0])
	}
	a.updatePanels()
	a.syncOverlays()
	a.publishHookState()
	a.save(false)
}

func (a *App) refreshNow() {
	a.detach()
	a.mons = Monitors()
	a.attach()
	a.tick()
}

// ------------------------------------------------------------------ panels

func (a *App) renderOpts(p *Plot, v *PlotView, edit bool) RenderOpts {
	c := a.cfg
	col, op := ParseHex(p.Color), p.Opacity
	if c.UniformLook {
		col, op = ParseHex(c.UniformColor), c.UniformOpacity
	}
	o := RenderOpts{
		W: v.Rect.W(), H: v.Rect.H(), Scale: v.Scale, TitleH: c.TitleH(v.Scale),
		Title: p.Title, Color: col, Opacity: op, TitlePt: c.TitleSize,
		FontFile: fontFile(c.TitleFont), Header: c.HeaderStyle, Grip: c.DirectEdit && !edit, Locked: p.Locked,
		HeaderCol: ParseHex(c.HeaderColor), TitleCol: ParseHex(c.TitleColor), TitleAuto: c.TitleAuto,
		Inbox: c.InboxPlot == p.ID, Edit: edit, Selected: edit && a.selected == p.ID,
		Radius:  c.CornerRadius,
		Chevron: !edit, Collapsed: v.Collapsed, TabNames: p.TabNames(), ActiveTab: p.ActiveTab,
		TabBar: v.TabBar.Sub(v.Rect), TabRects: relRects(v.Tabs, v.Rect),
		ScrollPos: v.Scroll, ScrollMax: v.MaxScroll, Rows: v.Rows,
		DropTarget: a.dropTarget == p.ID,
	}
	switch {
	case p.TitleColor == "auto":
		o.TitleAuto = true
	case strings.HasPrefix(p.TitleColor, "#"):
		o.TitleAuto, o.TitleCol = false, ParseHex(p.TitleColor)
	}
	return o
}

func relRects(rs []Rect, base Rect) []Rect {
	out := make([]Rect, len(rs))
	for i, r := range rs {
		out[i] = r.Sub(base)
	}
	return out
}

func (a *App) plotAtIndex(i uintptr) *Plot {
	if int(i) >= 0 && int(i) < len(a.cfg.Plots) {
		return a.cfg.Plots[i]
	}
	return nil
}

func (a *App) IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// lookKey changes whenever anything affecting a plot's appearance changes.
func (a *App) lookKey(o RenderOpts) string {
	return fmt.Sprintf("%s|%v|%d|%d|%s|%d|%v|%v|%v|%v|%.2f|%dx%d|%v|%v|%d|%v|%d/%d/%d", o.Title+fmt.Sprint(o.Radius), o.Color, o.Opacity, o.TitlePt, o.FontFile, o.Header, o.Grip,
		o.HeaderCol, o.TitleCol, o.Inbox, o.Scale, o.W, o.H, o.Collapsed, o.TabNames, o.ActiveTab, o.TabRects, o.ScrollPos, o.ScrollMax, o.Rows) + fmt.Sprint(o.DropTarget, o.Locked, o.TitleAuto, o.TitleCol)
}

func (a *App) setHidden(h bool) {
	if h && a.editing {
		a.exitEdit()
	}
	a.cfg.Hidden = h
	a.markDirty()
	if a.desk != nil {
		if h {
			call(pShowWindow, a.desk.ListView, SW_HIDE)
		} else {
			call(pShowWindow, a.desk.ListView, SW_SHOWNA)
		}
	}
	for _, p := range a.panels {
		if h {
			call(pShowWindow, p.hwnd, SW_HIDE)
		} else {
			call(pShowWindow, p.hwnd, SW_SHOWNA)
		}
	}
	if !h {
		a.tick()
	}
	if a.settings != nil {
		a.settings.refreshGeneral()
	}
}

func (a *App) pruneClicks() {
	now := time.Now()
	out := a.eng.Clicks[:0]
	for _, c := range a.eng.Clicks {
		if now.Sub(c.At) < c.TTL {
			out = append(out, c)
		}
	}
	a.eng.Clicks = out
}

// publishHookState hands the mouse-hook thread a read-only snapshot.
func (a *App) publishHookState() {
	st := &hookState{editing: a.editing, hidden: a.cfg.Hidden, dblHide: a.cfg.DoubleClickHide, direct: a.cfg.DirectEdit}
	if a.desk != nil {
		st.desk = *a.desk
		st.valid = true
	}
	for _, v := range a.eng.Views() {
		st.titles = append(st.titles, titleRect{v.ID, v.Title})
		idx := 0
		rolled := false
		for i, p := range a.cfg.Plots {
			if p.ID == v.ID {
				idx, rolled = i, p.Collapsed
			}
		}
		folder, locked := false, false
		if p := a.cfg.Plot(v.ID); p != nil {
			folder, locked = p.Folder != "", p.Locked
		}
		st.plots = append(st.plots, plotZone{id: v.ID, idx: idx, rect: v.Rect, title: v.Title, chevron: v.Chevron,
			tabs: v.Tabs, edge: int(math.Round(8 * v.Scale)), collapsed: v.Collapsed, rolled: rolled, scrolls: v.MaxScroll > 0,
			folder: folder, locked: locked})
	}
	for path, pos := range a.eng.last {
		if a.eng.IsParked(path) {
			continue
		}
		st.icons = append(st.icons, RectWH(pos.X-a.cfg.IconOffsetX-a.eng.Nudge(), pos.Y-a.cfg.IconOffsetY, a.eng.Spacing.X, a.eng.Spacing.Y))
	}
	setHookState(st)
}

func dumpTree(label string, parent HWND) {
	log.Println(label + ":")
	for c := call(pFindWindowExW, parent, 0, 0, 0); c != 0; c = call(pFindWindowExW, parent, c, 0, 0) {
		ex := call(pGetWindowLongPtrW, c, ^uintptr(19)) // GWL_EXSTYLE (-20)
		log.Printf("  %x %-18s vis=%v rect=%v ex=%08x", c, className(c), call(pIsWindowVisible, c) != 0, windowRect(c), ex)
	}
	vs := Rect{int(call(pGetSystemMetrics, 76)), int(call(pGetSystemMetrics, 77)), 0, 0}
	vs.R, vs.B = vs.L+int(call(pGetSystemMetrics, 78)), vs.T+int(call(pGetSystemMetrics, 79))
	log.Printf("  virtual screen=%v progman client origin=%v", vs, clientToScreen(parent, Point{}))
}

// checkUpdate swaps in Plots-update.exe placed next to Plots.exe and restarts,
// if Plots-update.exe.sig beside it shows it's a newer, genuine Plots build.
func (a *App) checkUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	upd := filepath.Join(dir, updateName)
	if _, err := os.Stat(upd); err != nil {
		return
	}
	sigPath := filepath.Join(dir, updateSigName)
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		err = errors.New(T("the update isn't signed by Plots, so it wasn't installed"))
	} else {
		var v string
		if v, err = checkUpdateFile(upd, sig); err == nil {
			log.Println("update: verified", v)
		}
	}
	if err != nil {
		// set it aside so it isn't checked again every few seconds
		log.Println("update: refused:", err)
		os.Remove(upd + ".rejected")
		os.Rename(upd, upd+".rejected")
		os.Remove(sigPath)
		a.setUpdateStatus(Tf("Update not installed: %s.", err.Error()), false)
		return
	}
	os.Remove(sigPath)
	old := filepath.Join(dir, "Plots.old.exe")
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		log.Println("update: rename current:", err)
		return
	}
	if err := os.Rename(upd, exe); err != nil {
		log.Println("update: install:", err)
		os.Rename(old, exe)
		return
	}
	log.Println("update: installed, restarting")
	cmd := exec.Command(exe, "--after-update")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		log.Println("update: restart:", err)
		return
	}
	a.quit()
}

// renaming reports whether Explorer has an in-place name edit box open on the desktop.
func (a *App) renaming() bool {
	return a.desk != nil && findWindowEx(a.desk.ListView, 0, "Edit") != 0
}

// parkPoint is an off-screen spot (below all monitors) where hidden icons wait.
func (a *App) parkPoint() Point {
	v := Rect{}
	for i, m := range a.mons {
		if i == 0 {
			v = m.Rect
		}
		v = Rect{min(v.L, m.Rect.L), min(v.T, m.Rect.T), max(v.R, m.Rect.R), max(v.B, m.Rect.B)}
	}
	return Point{v.L + 20, v.B + 400}
}

// syncIconMetrics picks up the desktop's icon size and spacing (they change
// with Ctrl+mouse wheel on the desktop, or the icon size setting).
func (a *App) syncIconMetrics() {
	if a.fv == nil {
		return
	}
	sp := a.fv.Spacing()
	iw := int(math.Round(float64(a.fv.IconSize()) * primaryMonitor(a.mons).Scale))
	if sp != a.eng.Spacing || iw != a.eng.IconW {
		a.eng.Spacing, a.eng.IconW = sp, iw
		a.eng.Relayout()
	}
}

// desktopIconSize is the desktop icon size in logical px (0 if the desktop isn't ready).
func (a *App) desktopIconSize() int {
	if a.fv == nil {
		return 0
	}
	return a.fv.IconSize()
}

func (a *App) setDesktopIconSize(size int) {
	if a.fv == nil || !a.fv.SetIconSize(size) {
		return
	}
	a.syncIconMetrics()
	a.tick()
}
