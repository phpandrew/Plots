//go:build windows

package main

import (
	"fmt"
	"math"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Settings struct {
	a        *App
	hwnd     HWND
	list     HWND
	title    HWND
	swatch   HWND
	opacity  HWND
	opLabel  HWND
	inbox    HWND
	monLabel HWND
	dbl      HWND
	startup  HWND
	dl       HWND
	dlLabel  HWND
	flags    HWND
	snap     HWND
	status   HWND
	editBtn  HWND
	plotCtls []HWND
	ids      []string
	cur      string
	loading  bool
	brush    uintptr
	font     uintptr
	scale    float64

	sc          func(int) int
	bgBrush     uintptr
	sideBrush   uintptr
	bold        uintptr
	headers     []HWND
	muted       []HWND
	sepY        int
	page        string
	plotPage    []HWND
	genPage     []HWND
	genBtn      HWND
	plotHeader  HWND
	inboxCombo  HWND
	direct      HWND
	comboIDs    []string
	lookPage    []HWND
	lookBtn     HWND
	fontCombo   HWND
	sizeBar     HWND
	sizeLabel   HWND
	titleSw     HWND
	headerCombo HWND
	headerSw    HWND
	padBar      HWND
	padLabel    HWND
	uniform     HWND
	uniSw       HWND
	uniBar      HWND
	uniLabel    HWND
	brushes     map[HWND]uintptr
	fontNames   []string
	chooseBtn   HWND
	pollBar     HWND
	pollLabel   HWND
}

const (
	idList = 1000 + iota
	idAdd
	idDelete
	idTitle
	idSwatch
	idChoose
	idOpacity
	idInbox
	idDbl
	idStartup
	idDl
	idFlags
	idEdit
	idClose
	idRefresh
	idSnap
	idGeneral
	idInboxCombo
	idDirect
	idAppearance
	idFont
	idSize
	idTitleSw
	idTitleChoose
	idHeader
	idHeaderSw
	idHeaderChoose
	idPad
	idUniform
	idUniSw
	idUniChoose
	idUniOpacity
	idPoll
)

var settingsProc uintptr

func uiFont(dpi int) uintptr {
	h := -int(math.Round(9 * float64(dpi) / 72))
	return call(pCreateFontW, uintptr(int32(h)), 0, 0, 0, 400, 0, 0, 0, 1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE*/, 0, up("Segoe UI"))
}

func (a *App) showSettings(plotID string) {
	if a.settings != nil && isWindow(a.settings.hwnd) {
		call(pShowWindow, a.settings.hwnd, SW_SHOW)
		call(pSetForegroundWindow, a.settings.hwnd)
		a.raiseUI()
		if plotID != "" {
			a.settings.selectPlot(plotID)
		}
		return
	}
	if settingsProc == 0 {
		settingsProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr {
			if a.settings == nil {
				return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
			}
			return a.settings.wndProc(h, msg, wp, lp)
		})
		registerClass("PlotsSettings", settingsProc, appIcon(32))
	}
	m := monitorAt(a.mons, cursorPos())
	s := &Settings{a: a, scale: m.Scale}
	a.settings = s
	sc := func(v int) int { return int(math.Round(float64(v) * s.scale)) }
	cw, ch := sc(winW), sc(winH)
	x, y := m.Work.L+(m.Work.W()-cw)/2, m.Work.T+(m.Work.H()-ch)/2
	s.hwnd = createWindow(WS_EX_CONTROLPARENT, "PlotsSettings", "Plots — Settings",
		WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_MINIMIZEBOX|WS_CLIPCHILDREN, x, y, cw, ch, 0, 0)
	// grow the window so the client area is exactly cw x ch
	var cr RECT
	call(pGetClientRect, s.hwnd, uintptr(unsafe.Pointer(&cr)))
	wr := windowRect(s.hwnd)
	call(pSetWindowPos, s.hwnd, 0, 0, 0, uintptr(cw+(wr.W()-int(cr.Right))), uintptr(ch+(wr.H()-int(cr.Bottom))), SWP_NOMOVE|SWP_NOZORDER)
	s.font = uiFont(dpiForWindow(s.hwnd))
	s.build(sc)
	s.refreshList()
	s.refreshGeneral()
	if plotID == "" {
		plotID = a.selected
	}
	if plotID == "" && len(a.cfg.Plots) > 0 {
		plotID = a.cfg.Plots[0].ID
	}
	s.selectPlot(plotID)
	call(pShowWindow, s.hwnd, SW_SHOW)
	call(pSetForegroundWindow, s.hwnd)
	a.raiseUI()
}

func (s *Settings) ctl(ex uintptr, class, text string, style uintptr, x, y, w, h int, id int) HWND {
	c := createWindow(ex, class, text, WS_CHILD|WS_VISIBLE|style, x, y, w, h, s.hwnd, uintptr(id))
	sendMessage(c, WM_SETFONT, s.font, 1)
	return c
}

// Layout (96-dpi units). Left: sidebar with the plot list. Right: plot and general settings.
const (
	sbW  = 260 // sidebar width
	winW = 800
	winH = 560
	colL = 284 // right column label x
	colF = 392 // right column field x
	colR = 776 // right edge
)

var (
	clrWhite   = uint32(0xFFFFFF)
	clrSide    = uint32(0xF5F3F2) // BGR
	clrLine    = uint32(0xE3E0DE)
	clrMuted   = uint32(0x6E6A66)
	clrSel     = uint32(0xF7E6D6)
	clrSelLine = uint32(0xD77800)
)

func (s *Settings) build(sc func(int) int) {
	s.sc = sc
	const BTN = WS_TABSTOP | BS_PUSHBUTTON
	const CHK = WS_TABSTOP | 0xB // BS_OWNERDRAW: drawn as a toggle switch
	s.bgBrush = call(pCreateSolidBrush, uintptr(clrWhite))
	s.sideBrush = call(pCreateSolidBrush, uintptr(clrSide))
	dpi := dpiForWindow(s.hwnd)
	s.bold = call(pCreateFontW, uintptr(int32(-int(math.Round(11.5*float64(dpi)/72)))), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 5, 0, up("Segoe UI"))

	// sidebar
	h := s.ctl(0, "STATIC", "Plots", SS_LEFT, sc(20), sc(18), sc(220), sc(26), 0)
	sendMessage(h, WM_SETFONT, s.bold, 1)
	s.list = s.ctl(0, "LISTBOX", "", WS_TABSTOP|WS_VSCROLL|LBS_NOTIFY|LBS_NOINTEGRALHEIGHT|LBS_OWNERDRAWFIXED|LBS_HASSTRINGS, sc(12), sc(52), sc(sbW-24), sc(290), idList)
	s.ctl(0, "BUTTON", "+  Add plot", BTN, sc(12), sc(348), sc(114), sc(32), idAdd)
	s.ctl(0, "BUTTON", "Delete", BTN, sc(134), sc(348), sc(114), sc(32), idDelete)
	s.lookBtn = s.ctl(0, "BUTTON", "Appearance", WS_TABSTOP|0xB /*BS_OWNERDRAW*/, sc(12), sc(404), sc(sbW-24), sc(36), idAppearance)
	s.genBtn = s.ctl(0, "BUTTON", "General settings", WS_TABSTOP|0xB /*BS_OWNERDRAW*/, sc(12), sc(444), sc(sbW-24), sc(36), idGeneral)
	s.ctl(0, "BUTTON", "Refresh desktop", BTN, sc(12), sc(490), sc(236), sc(32), idRefresh)
	s.muted = append(s.muted, s.ctl(0, "STATIC", "Plots "+displayVersion(), SS_LEFT, sc(20), sc(532), sc(220), sc(18), 0))

	fw := colR - colF
	w := colR - colL
	pg := func(h HWND) HWND { s.plotPage = append(s.plotPage, h); return h }
	gp := func(h HWND) HWND { s.genPage = append(s.genPage, h); return h }

	// ---- plot page
	s.plotHeader = pg(s.ctl(0, "STATIC", "Plot", SS_LEFT|0x8000 /*SS_ENDELLIPSIS*/, sc(colL), sc(18), sc(w), sc(26), 0))
	sendMessage(s.plotHeader, WM_SETFONT, s.bold, 1)
	pg(s.ctl(0, "STATIC", "Title", SS_LEFT, sc(colL), sc(66), sc(100), sc(20), 0))
	s.title = pg(s.ctl(WS_EX_CLIENTEDGE, "EDIT", "", WS_TABSTOP|ES_AUTOHSCROLL, sc(colF), sc(62), sc(fw), sc(28), idTitle))
	pg(s.ctl(0, "STATIC", "Colour", SS_LEFT, sc(colL), sc(108), sc(100), sc(20), 0))
	s.swatch = pg(s.ctl(0, "STATIC", "", SS_NOTIFY, sc(colF), sc(102), sc(48), sc(30), idSwatch))
	s.chooseBtn = pg(s.ctl(0, "BUTTON", "Choose…", BTN, sc(colF+58), sc(102), sc(110), sc(30), idChoose))
	pg(s.ctl(0, "STATIC", "Opacity", SS_LEFT, sc(colL), sc(150), sc(100), sc(20), 0))
	s.opacity = pg(s.ctl(0, "msctls_trackbar32", "", WS_TABSTOP|TBS_HORZ|TBS_NOTICKS, sc(colF-6), sc(144), sc(fw-56), sc(32), idOpacity))
	sendMessage(s.opacity, TBM_SETRANGE, 1, uintptr(100<<16))
	s.opLabel = pg(s.ctl(0, "STATIC", "", SS_LEFT, sc(colR-44), sc(150), sc(44), sc(20), 0))
	s.inbox = pg(s.ctl(0, "BUTTON", "New items saved to the desktop go into this plot", CHK, sc(colL), sc(188), sc(w), sc(32), idInbox))
	toggleState[s.inbox] = false
	s.monLabel = pg(s.ctl(0, "STATIC", "", SS_LEFT, sc(colL), sc(226), sc(w), sc(20), 0))
	s.muted = append(s.muted, s.monLabel)

	// ---- appearance page (applies to every plot)
	ap := func(h HWND) HWND { s.lookPage = append(s.lookPage, h); return h }
	h = ap(s.ctl(0, "STATIC", "Appearance", SS_LEFT, sc(colL), sc(18), sc(w), sc(26), 0))
	sendMessage(h, WM_SETFONT, s.bold, 1)
	s.muted = append(s.muted, ap(s.ctl(0, "STATIC", "These settings apply to every plot.", SS_LEFT, sc(colL), sc(46), sc(w), sc(20), 0)))
	label := func(text string, y int) { ap(s.ctl(0, "STATIC", text, SS_LEFT, sc(colL), sc(y+4), sc(104), sc(20), 0)) }
	slider := func(y, lo, hi, id int) (HWND, HWND) {
		t := ap(s.ctl(0, "msctls_trackbar32", "", WS_TABSTOP|TBS_HORZ|TBS_NOTICKS, sc(colF-6), sc(y-2), sc(fw-56), sc(32), id))
		sendMessage(t, TBM_SETRANGE, 1, uintptr(hi<<16|lo))
		return t, ap(s.ctl(0, "STATIC", "", SS_LEFT, sc(colR-44), sc(y+4), sc(44), sc(20), 0))
	}
	swatch := func(y, sid, cid int) HWND {
		sw := ap(s.ctl(0, "STATIC", "", SS_NOTIFY, sc(colF), sc(y), sc(48), sc(30), sid))
		ap(s.ctl(0, "BUTTON", "Choose…", BTN, sc(colF+58), sc(y), sc(110), sc(30), cid))
		return sw
	}
	label("Title font", 80)
	s.fontCombo = ap(s.ctl(0, "COMBOBOX", "", WS_TABSTOP|WS_VSCROLL|0x3, sc(colF), sc(80), sc(fw), sc(320), idFont))
	label("Title size", 122)
	s.sizeBar, s.sizeLabel = slider(122, 8, 24, idSize)
	label("Title colour", 162)
	s.titleSw = swatch(160, idTitleSw, idTitleChoose)
	label("Header", 204)
	s.headerCombo = ap(s.ctl(0, "COMBOBOX", "", WS_TABSTOP|0x3, sc(colF), sc(202), sc(fw), sc(200), idHeader))
	for _, t := range []string{"Darker shade of the plot colour", "Same as the plot", "Custom colour", "No header band"} {
		sendMessage(s.headerCombo, 0x143, 0, up(t))
	}
	label("Header colour", 244)
	s.headerSw = swatch(242, idHeaderSw, idHeaderChoose)
	label("Icon padding", 286)
	s.padBar, s.padLabel = slider(286, 0, 40, idPad)
	s.uniform = ap(s.ctl(0, "BUTTON", "Use the same colour and opacity for every plot", CHK, sc(colL), sc(326), sc(w), sc(32), idUniform))
	toggleState[s.uniform] = false
	label("Colour", 370)
	s.uniSw = swatch(368, idUniSw, idUniChoose)
	label("Opacity", 412)
	s.uniBar, s.uniLabel = slider(412, 0, 100, idUniOpacity)

	// ---- general page
	h = gp(s.ctl(0, "STATIC", "General settings", SS_LEFT, sc(colL), sc(18), sc(w), sc(26), 0))
	sendMessage(h, WM_SETFONT, s.bold, 1)
	gp(s.ctl(0, "STATIC", "New desktop items go to", SS_LEFT, sc(colL), sc(66), sc(170), sc(20), 0))
	s.inboxCombo = gp(s.ctl(0, "COMBOBOX", "", WS_TABSTOP|WS_VSCROLL|0x3 /*CBS_DROPDOWNLIST*/, sc(colL+176), sc(62), sc(w-176), sc(300), idInboxCombo))
	s.muted = append(s.muted, gp(s.ctl(0, "STATIC", "Anything saved or downloaded to the desktop lands in this plot. Icons you drag onto a plot stay there.", SS_LEFT, sc(colL), sc(96), sc(w), sc(36), 0)))
	y := 144
	row := func(text string, id int) HWND {
		c := gp(s.ctl(0, "BUTTON", text, CHK, sc(colL), sc(y), sc(w), sc(32), id))
		toggleState[c] = false
		y += 38
		return c
	}
	s.direct = row("Move and resize plots any time (drag the title bar or edges)", idDirect)
	s.snap = row("Snap plots to the icon grid and to each other (hold Alt to override)", idSnap)
	s.dbl = row("Double-click empty desktop to hide / show icons", idDbl)
	s.startup = row("Start Plots with Windows", idStartup)
	s.flags = row("Keep Auto-arrange && Align-to-grid off (recommended)", idFlags)
	s.dl = row("Also move finished downloads from the Downloads folder to the desktop", idDl)
	s.dlLabel = gp(s.ctl(0, "STATIC", "", SS_LEFT|0x4000 /*SS_PATHELLIPSIS*/, sc(colL), sc(y-8), sc(w-70), sc(20), 0))
	s.muted = append(s.muted, s.dlLabel)
	gp(s.ctl(0, "STATIC", "Desktop check rate", SS_LEFT, sc(colL), sc(y+28), sc(150), sc(20), 0))
	s.pollBar = gp(s.ctl(0, "msctls_trackbar32", "", WS_TABSTOP|TBS_HORZ, sc(colF+40), sc(y+22), sc(fw-110), sc(32), idPoll))
	sendMessage(s.pollBar, TBM_SETRANGE, 1, uintptr(5<<16|1))
	s.pollLabel = gp(s.ctl(0, "STATIC", "", SS_LEFT, sc(colR-64), sc(y+28), sc(64), sc(20), 0))
	s.muted = append(s.muted, gp(s.ctl(0, "STATIC", "How quickly new, deleted and moved icons are picked up. Faster uses a little more CPU.", SS_LEFT, sc(colL), sc(y+56), sc(w), sc(20), 0)))
	s.status = gp(s.ctl(0, "STATIC", "", SS_LEFT, sc(colL), sc(y+84), sc(w), sc(30), 0))
	s.muted = append(s.muted, s.status)

	s.editBtn = s.ctl(0, "BUTTON", "Edit layout", BTN, sc(colR-236), sc(512), sc(116), sc(32), idEdit)
	s.ctl(0, "BUTTON", "Close", BTN|BS_DEFPUSHBUTTON, sc(colR-112), sc(512), sc(112), sc(32), idClose)
	s.showPage("plot")
}

func (s *Settings) showPage(name string) {
	s.page = name
	vis := func(list []HWND, on bool) {
		for _, h := range list {
			if on {
				call(pShowWindow, h, SW_SHOWNA)
			} else {
				call(pShowWindow, h, SW_HIDE)
			}
		}
	}
	vis(s.plotPage, false)
	vis(s.genPage, false)
	vis(s.lookPage, false)
	switch name {
	case "plot":
		vis(s.plotPage, true)
	case "general":
		vis(s.genPage, true)
	case "look":
		vis(s.lookPage, true)
	}
	if name != "plot" {
		sendMessage(s.list, LB_SETCURSEL, ^uintptr(0), 0)
		s.refreshGeneral()
		s.refreshLook()
	}
	call(pInvalidateRect, s.genBtn, 0, 1)
	call(pInvalidateRect, s.lookBtn, 0, 1)
	call(pInvalidateRect, s.hwnd, 0, 1)
}

func (s *Settings) isMuted(h HWND) bool {
	for _, m := range s.muted {
		if m == h {
			return true
		}
	}
	return false
}

func (s *Settings) inSidebar(h HWND) bool {
	r := windowRect(h)
	p := screenToClient(s.hwnd, Point{r.L, r.T})
	return p.X < s.sc(sbW)
}

func (s *Settings) paint() {
	var ps PAINTSTRUCT
	hdc := call(pBeginPaint, s.hwnd, uintptr(unsafe.Pointer(&ps)))
	var cr RECT
	call(pGetClientRect, s.hwnd, uintptr(unsafe.Pointer(&cr)))
	call(pFillRect, hdc, uintptr(unsafe.Pointer(&cr)), s.bgBrush)
	side := RECT{0, 0, int32(s.sc(sbW)), cr.Bottom}
	call(pFillRect, hdc, uintptr(unsafe.Pointer(&side)), s.sideBrush)
	line := call(pCreateSolidBrush, uintptr(clrLine))
	edge := RECT{int32(s.sc(sbW)), 0, int32(s.sc(sbW) + 1), cr.Bottom}
	call(pFillRect, hdc, uintptr(unsafe.Pointer(&edge)), line)
	call(pDeleteObject, line)
	call(pEndPaint, s.hwnd, uintptr(unsafe.Pointer(&ps)))
}

type DRAWITEMSTRUCT struct {
	CtlType, CtlID, ItemID, ItemAction, ItemState uint32
	HwndItem                                      HWND
	HDC                                           uintptr
	RcItem                                        RECT
	ItemData                                      uintptr
}

type MEASUREITEMSTRUCT struct {
	CtlType, CtlID, ItemID, ItemWidth, ItemHeight uint32
	ItemData                                      uintptr
}

func fillRect(hdc uintptr, r RECT, c uint32) {
	b := call(pCreateSolidBrush, uintptr(c))
	call(pFillRect, hdc, uintptr(unsafe.Pointer(&r)), b)
	call(pDeleteObject, b)
}

// drawToggle paints a label with an anti-aliased on/off switch on the right.
func (s *Settings) drawToggle(di *DRAWITEMSTRUCT) {
	hdc, r := di.HDC, di.RcItem
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	on := toggleState[di.HwndItem]
	enabled := call(pIsWindowEnabled, di.HwndItem) != 0
	img := RenderToggleRow(w, h, s.scale, on, enabled)
	bmp := makeBitmap(img)
	mem := call(pCreateCompatibleDC, hdc)
	old := call(pSelectObject, mem, bmp)
	call(pBitBlt, hdc, uintptr(r.Left), uintptr(r.Top), uintptr(w), uintptr(h), mem, 0, 0, SRCCOPY)
	call(pSelectObject, mem, old)
	call(pDeleteDC, mem)
	call(pDeleteObject, bmp)
	call(pSetBkMode, hdc, 1)
	of := call(pSelectObject, hdc, s.font)
	if enabled {
		call(pSetTextColor, hdc, 0x202020)
	} else {
		call(pSetTextColor, hdc, 0xA0A0A0)
	}
	tr := RECT{r.Left, r.Top, r.Right - int32(s.sc(64)), r.Bottom}
	text := strings.ReplaceAll(windowText(di.HwndItem), "&&", "&")
	call(pDrawTextW, hdc, up(text), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20|0x8000|0x800 /*VCENTER|SINGLELINE|END_ELLIPSIS|NOPREFIX*/)
	call(pSelectObject, hdc, of)
	if di.ItemState&0x10 != 0 { // ODS_FOCUS
		fr := RECT{r.Right - int32(s.sc(56)), r.Top + 2, r.Right - 2, r.Bottom - 2}
		call(pDrawFocusRect, hdc, uintptr(unsafe.Pointer(&fr)))
	}
}

func (s *Settings) drawNav(di *DRAWITEMSTRUCT) {
	hdc, r := di.HDC, di.RcItem
	page, label := "general", "General settings"
	if di.CtlID == idAppearance {
		page, label = "look", "Appearance"
	}
	active := s.page == page
	bg := clrSide
	if active {
		bg = clrSel
	}
	fillRect(hdc, r, bg)
	if active {
		fillRect(hdc, RECT{r.Left, r.Top + int32(s.sc(6)), r.Left + int32(s.sc(3)), r.Bottom - int32(s.sc(6))}, clrSelLine)
	}
	call(pSetBkMode, hdc, 1)
	old := call(pSelectObject, hdc, s.font)
	call(pSetTextColor, hdc, 0x202020)
	gx, gy := r.Left+int32(s.sc(12)), r.Top+(r.Bottom-r.Top)/2
	if page == "general" {
		// three bars (settings sliders)
		for i, w := range []int32{16, 11, 14} {
			y := gy - int32(s.sc(6)) + int32(s.sc(5*i))
			fillRect(hdc, RECT{gx, y, gx + int32(s.sc(int(w))), y + int32(max(1, s.sc(2)))}, clrMuted)
		}
	} else {
		// four colour chips
		cs := []uint32{0x1E6B1E, 0x8C2A3B, 0x1E1E6B, 0x7A5A0E}
		q := int32(s.sc(7))
		for i, c := range cs {
			x := gx + int32(i%2)*(q+int32(s.sc(2)))
			y := gy - q - int32(s.sc(1)) + int32(i/2)*(q+int32(s.sc(2)))
			fillRect(hdc, RECT{x, y, x + q, y + q}, c)
		}
	}
	tr := RECT{gx + int32(s.sc(26)), r.Top, r.Right, r.Bottom}
	call(pDrawTextW, hdc, up(label), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20)
	call(pSelectObject, hdc, old)
}

func (s *Settings) drawItem(di *DRAWITEMSTRUCT) {
	hdc, r := di.HDC, di.RcItem
	selected := di.ItemState&1 != 0
	bg := clrSide
	if selected {
		bg = clrSel
	}
	fillRect(hdc, r, bg)
	if int(di.ItemID) < 0 || int(di.ItemID) >= len(s.ids) {
		return
	}
	p := s.a.cfg.Plot(s.ids[di.ItemID])
	if p == nil {
		return
	}
	if selected {
		fillRect(hdc, RECT{r.Left, r.Top + int32(s.sc(6)), r.Left + int32(s.sc(3)), r.Bottom - int32(s.sc(6))}, clrSelLine)
	}
	c := ParseHex(p.Color)
	sw := int32(s.sc(16))
	sy := r.Top + (r.Bottom-r.Top-sw)/2
	sx := r.Left + int32(s.sc(12))
	fillRect(hdc, RECT{sx, sy, sx + sw, sy + sw}, uint32(c.R)|uint32(c.G)<<8|uint32(c.B)<<16)
	call(pSetBkMode, hdc, 1 /*TRANSPARENT*/)
	old := call(pSelectObject, hdc, s.font)
	tr := RECT{sx + sw + int32(s.sc(10)), r.Top, r.Right - int32(s.sc(8)), r.Bottom}
	if s.a.cfg.InboxPlot == p.ID {
		call(pSetTextColor, hdc, uintptr(clrMuted))
		call(pDrawTextW, hdc, up("new items"), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x2|0x4|0x20 /*RIGHT|VCENTER|SINGLELINE*/)
		tr.Right -= int32(s.sc(72))
	}
	call(pSetTextColor, hdc, 0x202020)
	call(pDrawTextW, hdc, up(p.Title), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20|0x8000 /*VCENTER|SINGLELINE|END_ELLIPSIS*/)
	call(pSelectObject, hdc, old)
}

var toggleState = map[HWND]bool{}

func checked(h HWND) bool {
	if v, ok := toggleState[h]; ok {
		return v
	}
	return sendMessage(h, BM_GETCHECK, 0, 0) == 1
}

func setCheck(h HWND, v bool) {
	if old, ok := toggleState[h]; ok {
		if old != v {
			toggleState[h] = v
			call(pInvalidateRect, h, 0, 0)
		}
		return
	}
	var x uintptr
	if v {
		x = 1
	}
	sendMessage(h, BM_SETCHECK, x, 0)
}

func (s *Settings) refreshList() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	s.loading = true
	defer func() { s.loading = false }()
	sendMessage(s.list, LB_RESETCONTENT, 0, 0)
	s.ids = s.ids[:0]
	sel := -1
	for i, p := range s.a.cfg.Plots {
		label := p.Title
		if s.a.cfg.InboxPlot == p.ID {
			label += "  — new items"
		}
		sendMessage(s.list, LB_ADDSTRING, 0, up(label))
		s.ids = append(s.ids, p.ID)
		if p.ID == s.cur {
			sel = i
		}
	}
	if sel < 0 && len(s.ids) > 0 {
		sel = 0
		s.cur = s.ids[0]
	}
	if s.page != "plot" {
		sel = -1
	}
	sendMessage(s.list, LB_SETCURSEL, uintptr(sel), 0)
	s.loadPlot()
}

func (s *Settings) selectPlot(id string) {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	if s.page != "plot" {
		s.showPage("plot")
	}
	s.cur = id
	for i, x := range s.ids {
		if x == id {
			sendMessage(s.list, LB_SETCURSEL, uintptr(i), 0)
		}
	}
	s.loadPlot()
}

func (s *Settings) loadPlot() {
	s.loading = true
	defer func() { s.loading = false }()
	p := s.a.cfg.Plot(s.cur)
	for _, c := range s.plotCtls {
		var en uintptr
		if p != nil {
			en = 1
		}
		call(pEnableWindow, c, en)
	}
	if p == nil {
		setWindowText(s.title, "")
		setWindowText(s.monLabel, "Add a plot to get started.")
		return
	}
	if windowText(s.title) != p.Title {
		setWindowText(s.title, p.Title)
	}
	setWindowText(s.plotHeader, p.Title)
	var en uintptr = 1
	if s.a.cfg.UniformLook {
		en = 0 // colour & opacity come from Appearance
	}
	for _, h := range []HWND{s.swatch, s.chooseBtn, s.opacity, s.opLabel} {
		call(pEnableWindow, h, en)
	}
	sendMessage(s.opacity, TBM_SETPOS, 1, uintptr(p.Opacity))
	setWindowText(s.opLabel, fmt.Sprintf("%d%%", p.Opacity))
	setCheck(s.inbox, s.a.cfg.InboxPlot == p.ID)
	mon := p.Monitor
	idx := 0
	for i, m := range s.a.mons {
		if m.Name == p.Monitor {
			idx = i + 1
		}
	}
	if idx > 0 {
		mon = fmt.Sprintf("Monitor %d (%s)", idx, strings.TrimPrefix(p.Monitor, `\\.\`))
	}
	n := 0
	if v := s.a.eng.View(p.ID); v != nil {
		n = v.Count
	}
	setWindowText(s.monLabel, fmt.Sprintf("%s · %d×%d · %d icons", mon, p.W, p.H, n))
	s.setSwatch(ParseHex(p.Color))
}

func (s *Settings) setSwatch(c interface{ RGBA() (r, g, b, a uint32) }) {
	r, g, b, _ := c.RGBA()
	if s.brush != 0 {
		call(pDeleteObject, s.brush)
	}
	s.brush = call(pCreateSolidBrush, uintptr(r>>8|(g>>8)<<8|(b>>8)<<16))
	call(pInvalidateRect, s.swatch, 0, 1)
}

func (s *Settings) refreshGeneral() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	s.loading = true
	defer func() { s.loading = false }()
	c := s.a.cfg
	setCheck(s.dbl, c.DoubleClickHide)
	setCheck(s.startup, c.StartWithWindows)
	setCheck(s.dl, c.MoveDownloads)
	setCheck(s.flags, c.ManageDesktopFlags)
	setCheck(s.snap, c.SnapToGrid)
	setCheck(s.direct, c.DirectEdit)
	sendMessage(s.pollBar, TBM_SETPOS, 1, uintptr(c.PollMs/100))
	setWindowText(s.pollLabel, fmt.Sprintf("%.1f s", float64(c.PollMs)/1000))
	sendMessage(s.inboxCombo, 0x14B /*CB_RESETCONTENT*/, 0, 0)
	sendMessage(s.inboxCombo, 0x143 /*CB_ADDSTRING*/, 0, up("(off — leave new items where Windows puts them)"))
	s.comboIDs = []string{""}
	sel := 0
	for _, p := range c.Plots {
		sendMessage(s.inboxCombo, 0x143, 0, up(p.Title))
		s.comboIDs = append(s.comboIDs, p.ID)
		if p.ID == c.InboxPlot {
			sel = len(s.comboIDs) - 1
		}
	}
	sendMessage(s.inboxCombo, 0x14E /*CB_SETCURSEL*/, uintptr(sel), 0)
	setWindowText(s.dlLabel, s.a.dl.Dir())
	if s.a.editing {
		setWindowText(s.editBtn, "Done editing")
	} else {
		setWindowText(s.editBtn, "Edit layout")
	}
	st := s.a.status
	if c.Hidden {
		st = "Desktop icons are hidden (tray menu or double-click to show)."
	}
	setWindowText(s.status, st)
}

func (s *Settings) wndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	a := s.a
	switch msg {
	case WM_COMMAND:
		id, code := int(loword(wp)), uint32(hiword(wp))
		if s.loading {
			return 0
		}
		if _, ok := toggleState[lp]; ok && lp != 0 && (code == BN_CLICKED || code == 5 /*BN_DOUBLECLICKED*/) {
			toggleState[lp] = !toggleState[lp]
			call(pInvalidateRect, lp, 0, 0)
		}
		p := a.cfg.Plot(s.cur)
		switch id {
		case idList:
			if code == LBN_SELCHANGE {
				i := int(sendMessage(s.list, LB_GETCURSEL, 0, 0))
				if i >= 0 && i < len(s.ids) {
					s.showPage("plot")
					s.cur = s.ids[i]
					a.selected = s.cur
					s.loadPlot()
					if a.editing {
						a.syncOverlays()
					}
				}
			}
		case idAdd:
			a.newPlot(cursorPos())
		case idDelete:
			if p != nil {
				a.deletePlot(p.ID, s.hwnd)
			}
		case idTitle:
			if code == EN_CHANGE && p != nil {
				p.Title = windowText(s.title)
				setWindowText(s.plotHeader, p.Title)
				a.plotChanged()
			}
		case idChoose, idSwatch:
			if p != nil {
				if c, ok := chooseColor(s.hwnd, ParseHex(p.Color)); ok {
					p.Color = ToHex(c)
					a.plotChanged()
				}
			}
		case idGeneral:
			s.showPage("general")
		case idAppearance:
			s.showPage("look")
		case idFont:
			if code == 1 {
				i := int(sendMessage(s.fontCombo, 0x147, 0, 0))
				if i >= 0 && i < len(s.fontNames) {
					a.cfg.TitleFont = s.fontNames[i]
					s.lookChanged()
				}
			}
		case idHeader:
			if code == 1 {
				a.cfg.HeaderStyle = int(sendMessage(s.headerCombo, 0x147, 0, 0))
				s.lookChanged()
			}
		case idTitleSw, idTitleChoose:
			if c, ok := chooseColor(s.hwnd, ParseHex(a.cfg.TitleColor)); ok {
				a.cfg.TitleColor = ToHex(c)
				s.lookChanged()
			}
		case idHeaderSw, idHeaderChoose:
			if c, ok := chooseColor(s.hwnd, ParseHex(a.cfg.HeaderColor)); ok {
				a.cfg.HeaderColor = ToHex(c)
				if a.cfg.HeaderStyle != HeaderCustom {
					a.cfg.HeaderStyle = HeaderCustom
				}
				s.lookChanged()
			}
		case idUniSw, idUniChoose:
			if c, ok := chooseColor(s.hwnd, ParseHex(a.cfg.UniformColor)); ok {
				a.cfg.UniformColor = ToHex(c)
				s.lookChanged()
			}
		case idUniform:
			a.cfg.UniformLook = checked(s.uniform)
			s.lookChanged()
		case idInboxCombo:
			if code == 1 /*CBN_SELCHANGE*/ {
				i := int(sendMessage(s.inboxCombo, 0x147 /*CB_GETCURSEL*/, 0, 0))
				if i >= 0 && i < len(s.comboIDs) {
					a.cfg.InboxPlot = s.comboIDs[i]
					a.plotChanged()
				}
			}
		case idDirect:
			a.cfg.DirectEdit = checked(s.direct)
			a.markDirty()
			a.syncOverlays()
		case idInbox:
			if p != nil {
				if checked(s.inbox) {
					a.cfg.InboxPlot = p.ID
				} else if a.cfg.InboxPlot == p.ID {
					a.cfg.InboxPlot = ""
				}
				a.plotChanged()
			}
		case idDbl:
			a.cfg.DoubleClickHide = checked(s.dbl)
			a.markDirty()
			a.publishHookState()
		case idStartup:
			a.cfg.StartWithWindows = checked(s.startup)
			a.markDirty()
			a.applyAutostart()
		case idDl:
			a.cfg.MoveDownloads = checked(s.dl)
			a.markDirty()
			a.dl.Reset()
		case idFlags:
			a.cfg.ManageDesktopFlags = checked(s.flags)
			a.markDirty()
			if a.cfg.ManageDesktopFlags && a.fv != nil {
				a.fv.EnsureFreeLayout()
			}
		case idSnap:
			a.cfg.SnapToGrid = checked(s.snap)
			a.markDirty()
		case idRefresh:
			a.refreshNow()
			s.refreshGeneral()
		case idEdit:
			if a.editing {
				a.exitEdit()
			} else {
				a.enterEdit()
			}
		case idClose:
			call(pDestroyWindow, h)
		}
		a.save(false)
		return 0
	case WM_HSCROLL:
		if !s.loading && lp == s.pollBar {
			v := int(sendMessage(lp, TBM_GETPOS, 0, 0)) * 100
			if v != a.cfg.PollMs {
				a.cfg.PollMs = v
				setWindowText(s.pollLabel, fmt.Sprintf("%.1f s", float64(v)/1000))
				call(pSetTimer, a.main, timerTick, uintptr(v), 0)
				a.markDirty()
			}
			return 0
		}
		if !s.loading && (lp == s.sizeBar || lp == s.padBar || lp == s.uniBar) {
			v := int(sendMessage(lp, TBM_GETPOS, 0, 0))
			switch lp {
			case s.sizeBar:
				if v == a.cfg.TitleSize {
					return 0
				}
				a.cfg.TitleSize = v
			case s.padBar:
				if v == a.cfg.Padding {
					return 0
				}
				a.cfg.Padding = v
			case s.uniBar:
				if v == a.cfg.UniformOpacity {
					return 0
				}
				a.cfg.UniformOpacity = v
			}
			s.lookChanged()
			return 0
		}
		if lp == s.opacity && !s.loading {
			if p := a.cfg.Plot(s.cur); p != nil {
				p.Opacity = int(sendMessage(s.opacity, TBM_GETPOS, 0, 0))
				setWindowText(s.opLabel, fmt.Sprintf("%d%%", p.Opacity))
				a.markDirty()
				a.updatePanels()
				if a.editing {
					a.syncOverlays()
				}
			}
		}
		return 0
	case WM_CTLCOLORSTATIC, WM_CTLCOLORBTN:
		if lp == s.swatch && s.brush != 0 {
			return s.brush
		}
		if b, ok := s.brushes[lp]; ok {
			return b
		}
		hdc := wp
		call(pSetBkMode, hdc, 1)
		if s.isMuted(lp) {
			call(pSetTextColor, hdc, uintptr(clrMuted))
		} else {
			call(pSetTextColor, hdc, 0x202020)
		}
		if s.inSidebar(lp) {
			call(pSetBkColor, hdc, uintptr(clrSide))
			return s.sideBrush
		}
		call(pSetBkColor, hdc, uintptr(clrWhite))
		return s.bgBrush
	case 0x0134: // WM_CTLCOLORLISTBOX
		return s.sideBrush
	case WM_ERASEBKGND:
		return 1
	case WM_PAINT:
		s.paint()
		return 0
	case 0x002C: // WM_MEASUREITEM
		mi := (*MEASUREITEMSTRUCT)(unsafe.Pointer(lp))
		mi.ItemHeight = uint32(s.sc(36))
		return 1
	case 0x002B: // WM_DRAWITEM
		di := (*DRAWITEMSTRUCT)(unsafe.Pointer(lp))
		if di.CtlID == idList {
			s.drawItem(di)
			return 1
		}
		if di.CtlID == idGeneral || di.CtlID == idAppearance {
			s.drawNav(di)
			return 1
		}
		if _, ok := toggleState[di.HwndItem]; ok {
			s.drawToggle(di)
			return 1
		}
	case WM_CLOSE:
		call(pDestroyWindow, h)
		return 0
	case WM_DESTROY:
		if s.brush != 0 {
			call(pDeleteObject, s.brush)
		}
		call(pDeleteObject, s.font)
		call(pDeleteObject, s.bold)
		for _, b := range s.brushes {
			call(pDeleteObject, b)
		}
		call(pDeleteObject, s.bgBrush)
		call(pDeleteObject, s.sideBrush)
		a.settings = nil
		a.save(true)
		return 0
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

func (s *Settings) setBrush(h HWND, c string) {
	if s.brushes == nil {
		s.brushes = map[HWND]uintptr{}
	}
	if b := s.brushes[h]; b != 0 {
		call(pDeleteObject, b)
	}
	col := ParseHex(c)
	s.brushes[h] = call(pCreateSolidBrush, uintptr(uint32(col.R)|uint32(col.G)<<8|uint32(col.B)<<16))
	call(pInvalidateRect, h, 0, 1)
}

func (s *Settings) refreshLook() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	s.loading = true
	defer func() { s.loading = false }()
	c := s.a.cfg
	if len(s.fontNames) == 0 {
		s.fontNames = []string{""}
		sendMessage(s.fontCombo, 0x143, 0, up("Segoe UI Semibold (default)"))
		for _, f := range installedFonts() {
			s.fontNames = append(s.fontNames, f.Name)
			sendMessage(s.fontCombo, 0x143, 0, up(f.Name))
		}
	}
	for i, n := range s.fontNames {
		if n == c.TitleFont {
			sendMessage(s.fontCombo, 0x14E, uintptr(i), 0)
		}
	}
	sendMessage(s.headerCombo, 0x14E, uintptr(c.HeaderStyle), 0)
	sendMessage(s.sizeBar, TBM_SETPOS, 1, uintptr(c.TitleSize))
	setWindowText(s.sizeLabel, fmt.Sprintf("%d pt", c.TitleSize))
	sendMessage(s.padBar, TBM_SETPOS, 1, uintptr(c.Padding))
	setWindowText(s.padLabel, fmt.Sprintf("%d px", c.Padding))
	sendMessage(s.uniBar, TBM_SETPOS, 1, uintptr(c.UniformOpacity))
	setWindowText(s.uniLabel, fmt.Sprintf("%d%%", c.UniformOpacity))
	setCheck(s.uniform, c.UniformLook)
	s.setBrush(s.titleSw, c.TitleColor)
	s.setBrush(s.headerSw, c.HeaderColor)
	s.setBrush(s.uniSw, c.UniformColor)
	for _, h := range []HWND{s.uniSw, s.uniBar, s.uniLabel} {
		var en uintptr
		if c.UniformLook {
			en = 1
		}
		call(pEnableWindow, h, en)
	}
}

// lookChanged re-renders every plot after an appearance change.
func (s *Settings) lookChanged() {
	a := s.a
	a.markDirty()
	a.eng.Relayout() // title height / padding may have changed
	a.tick()
	s.refreshLook()
	s.refreshList()
	a.save(false)
}

func displayVersion() string {
	v := version
	if len(v) > 0 && v[0] >= '0' && v[0] <= '9' {
		v = "v" + v
	}
	return v
}
