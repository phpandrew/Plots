//go:build windows

package main

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/image/vector"
	"golang.org/x/sys/windows"
)

// The settings window: a navigation sidebar (sections, then the plot list) and
// a scrolling content pane on the right. Each page is a column of white cards,
// one setting per card, in the style of Windows 11 Settings.

type settingsPage struct {
	name   string
	ctls   []HWND
	cards  []Rect // content coordinates, physical px
	height int    // content height, physical px
}

type Settings struct {
	a       *App
	hwnd    HWND
	pane    HWND
	list    HWND
	editBtn HWND
	navBtns []HWND

	font, bold, bigFont, iconFont uintptr
	bgBrush, cardBrush            uintptr
	scale                         float64
	sc                            func(int) int

	pages   map[string]*settingsPage
	page    string
	base    map[HWND]Point // control position in content coordinates
	onCard  map[HWND]bool
	muted   map[HWND]bool
	scrollY int
	brush   uintptr
	brushes map[HWND]uintptr

	ids     []string
	cur     string
	loading bool

	// plot page
	plotHeader, monLabel, title, swatch, chooseBtn, opacity, opLabel HWND
	inbox, rollToggle, tabsEdit, folderEdit, folderClear, sortCombo  HWND
	iconCombo, deleteBtn                                             HWND

	// appearance page
	fontCombo, sizeBar, sizeLabel, titleSw, headerCombo, headerSw HWND
	deskIconCombo, padBar, padLabel, radBar, radLabel             HWND
	uniform, uniSw, uniBar, uniLabel                              HWND
	tintCombo, tintSw, tintBar, tintLabel                         HWND
	fade, fadeBar, fadeLabel, fadeIcons                           HWND
	fontNames                                                     []string

	// rules page
	ruleList, presetCombo, patEdit, rulePlot, ruleTab, ruleStatus HWND
	ruleSel                                                       int

	// general page
	inboxCombo, dl, dlLabel, direct, grow, snap, dbl, startup, flags HWND
	peek, peekKey, peekWin, pollBar, pollLabel, status               HWND
	langCombo                                                        HWND
	comboIDs                                                         []string

	// backup page
	backupCombo, backupDir, keepBar, keepLabel, backupStatus HWND

	// about page
	verLabel, linkLabel, installBtn, updLabel, autoUpd HWND
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
	idTabs
	idRoll
	idGrow
	idRulesNav
	idRuleList
	idPreset
	idPattern
	idRulePlot
	idRuleTab
	idRuleAdd
	idRuleDel
	idRuleApply
	idFolderBrowse
	idFolderClear
	idFolderSort
	idRadius
	idIconSize
	idDeskIcon
	idBackupNav
	idExport
	idImport
	idBackupEvery
	idBackupDir
	idBackupOpen
	idBackupNow
	idPeek
	idPeekKey
	idPeekWin
	idTint
	idTintSw
	idTintChoose
	idSideLogo
	idFade
	idLanguage
	idFadeIcons
)

var (
	settingsProc, paneProc uintptr
	pSetScrollInfo         = user32.NewProc("SetScrollInfo")
)

// Layout, in 96-dpi units.
const (
	winW  = 940
	winH  = 680
	sbW   = 236 // sidebar width
	paneW = winW - sbW
	cardL = 32
	cardR = paneW - 32 - 17 // leave room for the scroll bar
	cardW = cardR - cardL
	ctlR  = cardR - 18 // right edge of a card's control
)

var (
	clrWhite    = uint32(0xFFFFFF)
	clrBg       = uint32(0xF3F3F3)
	clrCardLine = uint32(0xE5E5E5)
	clrNavSel   = uint32(0xE9E9E9)
	clrMuted    = uint32(0x6E6A66)
	clrText     = uint32(0x1C1C1C)
	clrSelLine  = uint32(0xC06700) // BGR of #0067C0
)

func uiFont(dpi int) uintptr {
	h := -int(math.Round(9.5 * float64(dpi) / 72))
	return call(pCreateFontW, uintptr(int32(h)), 0, 0, 0, 400, 0, 0, 0, 1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE*/, 0, up("Segoe UI"))
}

func makeFont(dpi int, pt float64, weight int, face string) uintptr {
	return call(pCreateFontW, uintptr(int32(-int(math.Round(pt*float64(dpi)/72)))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, up(face))
}

// iconFace is the Windows icon font: Segoe Fluent Icons on Windows 11,
// Segoe MDL2 Assets on Windows 10 (same code points).
func iconFace() string {
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	if _, err := os.Stat(filepath.Join(win, "Fonts", "SegoeIcons.ttf")); err == nil {
		return "Segoe Fluent Icons"
	}
	return "Segoe MDL2 Assets"
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
		paneProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp uintptr) uintptr {
			if a.settings == nil {
				return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
			}
			return a.settings.paneWndProc(h, msg, wp, lp)
		})
		registerClass("PlotsSettings", settingsProc, appIcon(32))
		registerClass("PlotsSettingsPane", paneProc, 0)
	}
	m := monitorAt(a.mons, cursorPos())
	s := &Settings{a: a, scale: m.Scale, pages: map[string]*settingsPage{}, base: map[HWND]Point{}, onCard: map[HWND]bool{}, muted: map[HWND]bool{}}
	a.settings = s
	s.sc = func(v int) int { return int(math.Round(float64(v) * s.scale)) }
	sc := s.sc
	cw, ch := sc(winW), sc(winH)
	x, y := m.Work.L+(m.Work.W()-cw)/2, m.Work.T+(m.Work.H()-ch)/2
	s.hwnd = createWindow(WS_EX_CONTROLPARENT, "PlotsSettings", T("Plots — Settings"),
		WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_MINIMIZEBOX|WS_CLIPCHILDREN, x, y, cw, ch, 0, 0)
	// grow the window so the client area is exactly cw x ch
	var cr RECT
	call(pGetClientRect, s.hwnd, uintptr(unsafe.Pointer(&cr)))
	wr := windowRect(s.hwnd)
	call(pSetWindowPos, s.hwnd, 0, 0, 0, uintptr(cw+(wr.W()-int(cr.Right))), uintptr(ch+(wr.H()-int(cr.Bottom))), SWP_NOMOVE|SWP_NOZORDER)
	dpi := dpiForWindow(s.hwnd)
	s.font = uiFont(dpi)
	s.bold = makeFont(dpi, 11, 600, "Segoe UI")
	s.bigFont = makeFont(dpi, 20, 600, "Segoe UI")
	s.iconFont = makeFont(dpi, 12, 400, iconFace())
	s.bgBrush = call(pCreateSolidBrush, uintptr(clrBg))
	s.cardBrush = call(pCreateSolidBrush, uintptr(clrWhite))
	s.pane = createWindow(WS_EX_CONTROLPARENT, "PlotsSettingsPane", "", WS_CHILD|WS_VISIBLE|WS_VSCROLL|WS_CLIPCHILDREN, sc(sbW), 0, cw-sc(sbW), ch, s.hwnd, 0)
	s.build()
	s.refreshList()
	s.refreshGeneral()
	if plotID == "" {
		plotID = a.selected
	}
	if plotID == "" && len(a.cfg.Plots) > 0 {
		plotID = a.cfg.Plots[0].ID
	}
	if plotID != "" {
		s.selectPlot(plotID)
	} else {
		s.showPage("general")
	}
	call(pShowWindow, s.hwnd, SW_SHOW)
	call(pSetForegroundWindow, s.hwnd)
	a.raiseUI()
}

// ctl makes a sidebar control (child of the main window, 96-dpi coordinates).
func (s *Settings) ctl(ex uintptr, class, text string, style uintptr, x, y, w, h int, id int) HWND {
	c := createWindow(ex, class, text, WS_CHILD|WS_VISIBLE|style, s.sc(x), s.sc(y), s.sc(w), s.sc(h), s.hwnd, uintptr(id))
	sendMessage(c, WM_SETFONT, s.font, 1)
	return c
}

// ------------------------------------------------------------------ page layout helper

type layout struct {
	s    *Settings
	pg   *settingsPage
	y    int  // next free y, 96-dpi units
	card Rect // current card, 96-dpi units
}

func (s *Settings) newPage(name string) *layout {
	pg := &settingsPage{name: name}
	s.pages[name] = pg
	return &layout{s: s, pg: pg, y: 24}
}

// add creates a control on the page (96-dpi content coordinates).
func (l *layout) add(ex uintptr, class, text string, style uintptr, x, y, w, h, id int) HWND {
	s := l.s
	c := createWindow(ex, class, text, WS_CHILD|style, s.sc(x), s.sc(y), s.sc(w), s.sc(h), s.pane, uintptr(id))
	sendMessage(c, WM_SETFONT, s.font, 1)
	s.base[c] = Point{s.sc(x), s.sc(y)}
	l.pg.ctls = append(l.pg.ctls, c)
	if l.card.Contains(Point{x + 1, y + 1}) {
		s.onCard[c] = true
	}
	return c
}

func (l *layout) text(text string, x, y, w, h int, muted bool) HWND {
	c := l.add(0, "STATIC", text, SS_LEFT|0x80 /*SS_NOPREFIX*/, x, y, w, h, 0)
	if muted {
		l.s.muted[c] = true
	}
	return c
}

// title adds the page heading and an optional muted subtitle.
func (l *layout) title(text, sub string) (HWND, HWND) {
	t := l.add(0, "STATIC", text, SS_LEFT|0x80|0x8000 /*SS_ENDELLIPSIS*/, cardL, l.y, cardW, 40, 0)
	sendMessage(t, WM_SETFONT, l.s.bigFont, 1)
	l.y += 44
	var st HWND
	if sub != "" {
		st = l.text(sub, cardL, l.y, cardW, 20, true)
		l.y += 24
	}
	l.y += 8
	return t, st
}

// section starts a group of cards with a small bold heading.
func (l *layout) section(text string) {
	l.y += 10
	h := l.text(text, cardL+2, l.y, cardW, 22, false)
	sendMessage(h, WM_SETFONT, l.s.bold, 1)
	l.y += 30
}

// row adds a card with a label (and a muted description under it) and room
// for a control ctrlW wide on the right. ctrlW < 0 puts the controls on their
// own line under the text, for wide controls like a folder path. It returns
// the vertical centre for the control and the description.
func (l *layout) row(label, desc string, ctrlW int) (int, HWND) {
	textW := cardW - 36
	if ctrlW > 0 {
		textW -= ctrlW + 24
	}
	ll := l.s.textLines(label, textW) // translations can be long: let labels wrap too
	lines := 0
	if desc != "" {
		lines = l.s.textLines(desc, textW)
	}
	top := max(54, 34+18*ll)
	if lines > 0 {
		top = 32 + 18*ll + 18*lines
	}
	h := top
	if ctrlW < 0 {
		h += 44
	}
	l.card = Rect{cardL, l.y, cardR, l.y + h}
	l.pg.cards = append(l.pg.cards, Rect{l.s.sc(cardL), l.s.sc(l.y), l.s.sc(cardR), l.s.sc(l.y + h)})
	var d HWND
	if lines > 0 {
		l.text(label, cardL+18, l.y+14, textW, 18*ll+2, false)
		d = l.text(desc, cardL+18, l.y+17+18*ll, textW, 18*lines, true)
	} else {
		l.text(label, cardL+18, l.y+(top-18*ll)/2, textW, 18*ll+2, false)
	}
	cy := l.y + top/2
	if ctrlW < 0 {
		cy = l.y + top - 6 + 22
	}
	l.y += h + 4
	return cy, d
}

// textWidth is the width of a single line of text, in 96-dpi units.
func (s *Settings) textWidth(text string) int {
	hdc := call(pGetDC, s.hwnd)
	defer call(pReleaseDC, s.hwnd, hdc)
	old := call(pSelectObject, hdc, s.font)
	defer call(pSelectObject, hdc, old)
	r := RECT{}
	call(pDrawTextW, hdc, up(strings.ReplaceAll(text, "&&", "&")), ^uintptr(0), uintptr(unsafe.Pointer(&r)), 0x20|0x400|0x800 /*SINGLELINE|CALCRECT|NOPREFIX*/)
	return int(math.Ceil(float64(r.Right) / s.scale))
}

// card adds a free-form card of height h and returns its top.
// textLines is how many lines desc wraps to in a box w wide (96-dpi units).
func (s *Settings) textLines(desc string, w int) int {
	if strings.TrimSpace(desc) == "" {
		return max(1, int(math.Ceil(float64(len(desc))*6.4/float64(w))))
	}
	hdc := call(pGetDC, s.hwnd)
	defer call(pReleaseDC, s.hwnd, hdc)
	old := call(pSelectObject, hdc, s.font)
	defer call(pSelectObject, hdc, old)
	r := RECT{0, 0, int32(s.sc(w)), 0}
	call(pDrawTextW, hdc, up(desc), ^uintptr(0), uintptr(unsafe.Pointer(&r)), 0x10|0x400|0x800 /*WORDBREAK|CALCRECT|NOPREFIX*/)
	lineH := s.sc(17)
	return max(1, (int(r.Bottom)+lineH/2)/lineH)
}

func (l *layout) cardBox(h int) int {
	l.card = Rect{cardL, l.y, cardR, l.y + h}
	l.pg.cards = append(l.pg.cards, Rect{l.s.sc(cardL), l.s.sc(l.y), l.s.sc(cardR), l.s.sc(l.y + h)})
	top := l.y
	l.y += h + 4
	return top
}

func (l *layout) toggle(cy, id int) HWND {
	c := l.add(0, "BUTTON", "", WS_TABSTOP|0xB /*BS_OWNERDRAW*/, ctlR-56, cy-15, 56, 30, id)
	toggleState[c] = false
	return c
}

// button keeps its right edge at x+w and grows to the left if the
// (translated) text needs more room.
func (l *layout) button(text string, cy, x, w, id int) HWND {
	if need := l.s.textWidth(text) + 32; need > w {
		x -= need - w
		w = need
	}
	return l.add(0, "BUTTON", text, WS_TABSTOP|BS_PUSHBUTTON, x, cy-16, w, 32, id)
}

func (l *layout) combo(cy, w, id int, items ...string) HWND {
	c := l.add(0, "COMBOBOX", "", WS_TABSTOP|WS_VSCROLL|0x3 /*CBS_DROPDOWNLIST*/, ctlR-w, cy-15, w, 320, id)
	for _, t := range items {
		sendMessage(c, 0x143 /*CB_ADDSTRING*/, 0, up(t))
	}
	return c
}

func (l *layout) edit(cy, x, w, id int, style uintptr) HWND {
	return l.add(WS_EX_CLIENTEDGE, "EDIT", "", WS_TABSTOP|ES_AUTOHSCROLL|style, x, cy-14, w, 28, id)
}

func (l *layout) slider(cy, lo, hi, id int) (HWND, HWND) {
	t := l.add(0, "msctls_trackbar32", "", WS_TABSTOP|TBS_HORZ|TBS_NOTICKS, ctlR-60-220, cy-15, 220, 30, id)
	sendMessage(t, TBM_SETRANGE, 1, uintptr(hi<<16|lo))
	lb := l.text("", ctlR-56, cy-10, 56, 20, false)
	return t, lb
}

func (l *layout) swatch(cy, sid, cid int) HWND {
	sw := l.add(0, "STATIC", "", SS_NOTIFY|0x00800000 /*WS_BORDER*/, ctlR-100-12-44, cy-14, 44, 28, sid)
	l.button(T("Choose…"), cy, ctlR-100, 100, cid)
	return sw
}

func (l *layout) done() {
	l.pg.height = l.s.sc(l.y + 24)
}

// ------------------------------------------------------------------ build

func (s *Settings) build() {
	const BTN = WS_TABSTOP | BS_PUSHBUTTON

	// ---- sidebar
	s.ctl(0, "STATIC", "", 0xD /*SS_OWNERDRAW*/, 20, 18, 28, 28, idSideLogo)
	h := s.ctl(0, "STATIC", "Plots", SS_LEFT, 56, 20, 150, 26, 0)
	sendMessage(h, WM_SETFONT, s.bold, 1)
	nav := []struct {
		id   int
		text string
	}{{idGeneral, T("General")}, {idAppearance, T("Appearance")}, {idRulesNav, T("Sorting rules")}, {idBackupNav, T("Backup & restore")}, {idAboutNav, T("About")}}
	for i, n := range nav {
		s.navBtns = append(s.navBtns, s.ctl(0, "BUTTON", n.text, WS_TABSTOP|0xB /*BS_OWNERDRAW*/, 8, 64+i*40, sbW-16, 38, n.id))
	}
	y := 64 + len(nav)*40 + 14
	addW := max(72, s.textWidth(T("+  Add"))+28)
	h = s.ctl(0, "STATIC", T("Your plots"), SS_LEFT|0x8000 /*SS_ENDELLIPSIS*/, 20, y+5, sbW-8-addW-28, 20, 0)
	sendMessage(h, WM_SETFONT, s.bold, 1)
	s.ctl(0, "BUTTON", T("+  Add"), BTN, sbW-8-addW, y, addW, 30, idAdd)
	y += 38
	s.list = s.ctl(0, "LISTBOX", "", WS_TABSTOP|WS_VSCROLL|LBS_NOTIFY|LBS_NOINTEGRALHEIGHT|LBS_OWNERDRAWFIXED|LBS_HASSTRINGS, 8, y, sbW-16, winH-y-58, idList)
	s.editBtn = s.ctl(0, "BUTTON", T("Edit layout"), BTN, 8, winH-48, sbW-16, 36, idEdit)

	s.buildPlotPage()
	s.buildLookPage()
	s.buildRulesPage()
	s.buildGeneralPage()
	s.buildBackupPage()
	s.buildAbout()
}

func (s *Settings) buildPlotPage() {
	l := s.newPage("plot")
	s.plotHeader, s.monLabel = l.title(T("Plot"), " ")

	l.section(T("Look"))
	cy, _ := l.row(T("Title"), "", 280)
	s.title = l.edit(cy, ctlR-280, 280, idTitle, 0)
	cy, _ = l.row(T("Colour"), "", 156)
	s.swatch = l.swatch(cy, idSwatch, idChoose)
	s.chooseBtn = l.pg.ctls[len(l.pg.ctls)-1]
	cy, _ = l.row(T("Opacity"), "", 280)
	s.opacity, s.opLabel = l.slider(cy, 0, 100, idOpacity)
	cy, _ = l.row(T("Rolled up"), T("Only the title bar shows. Hover over it to peek."), 56)
	s.rollToggle = l.toggle(cy, idRoll)

	l.section(T("Contents"))
	cy, _ = l.row(T("New desktop items go here"), T("Anything saved or downloaded to the desktop lands in this plot."), 56)
	s.inbox = l.toggle(cy, idInbox)
	cy, _ = l.row(T("Tabs"), T("Separate names with commas, e.g. Orders, Designs. Drag a plot onto another's title bar to merge them."), 280)
	s.tabsEdit = l.edit(cy, ctlR-280, 280, idTabs, 0)
	cy, _ = l.row(T("Live folder"), T("Show a folder's live contents instead of desktop icons."), -1)
	s.folderEdit = l.edit(cy, cardL+18, cardW-36-96-80-16, 0, 0x800 /*ES_READONLY*/)
	l.button(T("Choose…"), cy, ctlR-96-80-8, 96, idFolderBrowse)
	s.folderClear = l.button(T("Clear"), cy, ctlR-80, 80, idFolderClear)
	cy, _ = l.row(T("Sort folder by"), "", 260)
	s.sortCombo = l.combo(cy, 260, idFolderSort, T("Name"), T("Date modified (newest first)"), T("Type"), T("Size (largest first)"))
	cy, _ = l.row(T("Folder icon size"), T("Desktop icons all share one size, set under Appearance."), 260)
	s.iconCombo = l.combo(cy, 260, idIconSize, iconSizeLabels(true)...)

	l.y += 12
	s.deleteBtn = l.add(0, "BUTTON", T("Delete this plot"), WS_TABSTOP|BS_PUSHBUTTON, cardL, l.y, max(150, s.textWidth(T("Delete this plot"))+32), 32, idDelete)
	l.y += 32
	l.done()
}

var iconSizes = []int{32, 48, 64, 96, 128}

func iconSizeLabels(withDesktop bool) []string {
	var out []string
	if withDesktop {
		out = append(out, T("Same as desktop icons"))
	}
	for _, n := range []string{T("Small"), T("Medium"), T("Large"), T("Extra large"), T("Huge")} {
		out = append(out, n)
	}
	for i := range out {
		if withDesktop && i == 0 {
			continue
		}
		j := i
		if withDesktop {
			j--
		}
		out[i] = Tf("%s (%d px)", out[i], iconSizes[j])
	}
	return out
}

func (s *Settings) buildLookPage() {
	l := s.newPage("look")
	l.title(T("Appearance"), T("These settings apply to every plot."))

	l.section(T("Title bar"))
	cy, _ := l.row(T("Font"), "", 280)
	s.fontCombo = l.combo(cy, 280, idFont)
	cy, _ = l.row(T("Size"), "", 280)
	s.sizeBar, s.sizeLabel = l.slider(cy, 8, 24, idSize)
	cy, _ = l.row(T("Text colour"), "", 156)
	s.titleSw = l.swatch(cy, idTitleSw, idTitleChoose)
	cy, _ = l.row(T("Header band"), "", 280)
	s.headerCombo = l.combo(cy, 280, idHeader, T("Darker shade of the plot colour"), T("Same as the plot"), T("Custom colour"), T("No header band"))
	cy, _ = l.row(T("Header colour"), T("Used when the header band is set to a custom colour."), 156)
	s.headerSw = l.swatch(cy, idHeaderSw, idHeaderChoose)

	l.section(T("Panels"))
	cy, _ = l.row(T("Same colour for every plot"), T("Off: each plot uses its own colour and opacity."), 56)
	s.uniform = l.toggle(cy, idUniform)
	cy, _ = l.row(T("Colour"), "", 156)
	s.uniSw = l.swatch(cy, idUniSw, idUniChoose)
	cy, _ = l.row(T("Opacity"), "", 280)
	s.uniBar, s.uniLabel = l.slider(cy, 0, 100, idUniOpacity)
	cy, _ = l.row(T("Corner radius"), "", 280)
	s.radBar, s.radLabel = l.slider(cy, 0, 30, idRadius)
	cy, _ = l.row(T("Icon padding"), T("Space between a plot's edge and its icons."), 280)
	s.padBar, s.padLabel = l.slider(cy, 0, 40, idPad)
	cy, _ = l.row(T("Fade plots until you hover"), T("Plots stay faded into the wallpaper and come into focus when the mouse is over them."), 56)
	s.fade = l.toggle(cy, idFade)
	cy, _ = l.row(T("Faded strength"), T("How visible a plot is while it's faded."), 280)
	s.fadeBar, s.fadeLabel = l.slider(cy, 10, 90, 0)
	cy, _ = l.row(T("Fade desktop icons too"), T("Windows draws all desktop icons as one layer, so they fade together and come back when the mouse is over any plot or icon."), 56)
	s.fadeIcons = l.toggle(cy, idFadeIcons)

	l.section(T("Icons"))
	cy, _ = l.row(T("Desktop icon size"), T("Applies to every desktop icon, like Ctrl + mouse wheel on the desktop."), 280)
	s.deskIconCombo = l.combo(cy, 280, idDeskIcon, iconSizeLabels(false)...)
	cy, _ = l.row(T("Tint live-folder icons"), T("Only for live-folder plots, whose icons Plots draws itself. Windows draws desktop icons, so they can't be tinted."), 280)
	s.tintCombo = l.combo(cy, 280, idTint, T("Off"), T("Match each plot's colour"), T("Custom colour"))
	cy, _ = l.row(T("Tint colour"), "", 156)
	s.tintSw = l.swatch(cy, idTintSw, idTintChoose)
	cy, _ = l.row(T("Tint strength"), "", 280)
	s.tintBar, s.tintLabel = l.slider(cy, 10, 100, 0)
	l.done()
}

func (s *Settings) buildRulesPage() {
	l := s.newPage("rules")
	l.title(T("Sorting rules"), T("New files on the desktop that match a rule go straight to that plot."))
	top := l.cardBox(170)
	s.ruleList = l.add(0, "LISTBOX", "", WS_TABSTOP|WS_VSCROLL|LBS_NOTIFY|LBS_NOINTEGRALHEIGHT, cardL+12, top+12, cardW-24, 146, idRuleList)
	cy, _ := l.row(T("Add a common file type"), "", 300)
	s.presetCombo = l.combo(cy, 300, idPreset, T("Choose a file type…"))
	for _, pr := range RulePresets {
		sendMessage(s.presetCombo, 0x143, 0, up(T(pr.Name)+"  ("+pr.Pattern+")"))
	}
	sendMessage(s.presetCombo, 0x14E, 0, 0)
	cy, _ = l.row(T("File types"), T("Like *.pdf;*.docx. Use \"folder\" for folders."), 300)
	s.patEdit = l.edit(cy, ctlR-300, 300, idPattern, 0)
	cy, _ = l.row(T("Goes to plot"), "", 300)
	s.rulePlot = l.combo(cy, 300, idRulePlot)
	cy, _ = l.row(T("Tab"), "", 300)
	s.ruleTab = l.combo(cy, 300, idRuleTab)
	l.y += 10
	w1 := max(120, s.textWidth(T("+  Add rule"))+32)
	w2 := max(120, s.textWidth(T("Delete rule"))+32)
	w3 := max(180, s.textWidth(T("Sort my desktop now"))+32)
	l.add(0, "BUTTON", T("+  Add rule"), WS_TABSTOP|BS_PUSHBUTTON, cardL, l.y, w1, 32, idRuleAdd)
	l.add(0, "BUTTON", T("Delete rule"), WS_TABSTOP|BS_PUSHBUTTON, cardL+w1+8, l.y, w2, 32, idRuleDel)
	l.add(0, "BUTTON", T("Sort my desktop now"), WS_TABSTOP|BS_PUSHBUTTON, cardR-w3, l.y, w3, 32, idRuleApply)
	l.y += 42
	s.ruleStatus = l.text(T("Rules are checked top to bottom. Anything that matches no rule goes to your new-items plot."), cardL, l.y, cardW, 54, true)
	l.y += 36
	l.done()
}

func (s *Settings) buildGeneralPage() {
	l := s.newPage("general")
	l.title(T("General"), "")

	l.section(T("New items"))
	cy, _ := l.row(T("New desktop items go to"), T("Anything saved or downloaded to the desktop lands in this plot."), 280)
	s.inboxCombo = l.combo(cy, 280, idInboxCombo)
	cy, s.dlLabel = l.row(T("Move finished downloads to the desktop"), " ", 56)
	s.dl = l.toggle(cy, idDl)
	s.onCard[s.dlLabel] = true
	call(pSetWindowLongPtrW, s.dlLabel, ^uintptr(15) /*GWL_STYLE*/, WS_CHILD|SS_LEFT|0x80|0x4000 /*SS_PATHELLIPSIS*/)

	l.section(T("Behaviour"))
	cy, _ = l.row(T("Move and resize plots any time"), T("Drag a plot's title bar or edges without entering edit mode."), 56)
	s.direct = l.toggle(cy, idDirect)
	cy, _ = l.row(T("Grow plots to fit their icons"), T("Off: plots keep their size and scroll."), 56)
	s.grow = l.toggle(cy, idGrow)
	cy, _ = l.row(T("Snap to grid"), T("Snap plots to the icon grid and to each other. Hold Alt to place freely."), 56)
	s.snap = l.toggle(cy, idSnap)
	cy, _ = l.row(T("Double-click to hide"), T("Double-click empty desktop to hide or show icons and plots."), 56)
	s.dbl = l.toggle(cy, idDbl)

	l.section(T("Peek"))
	cy, _ = l.row(T("Peek shortcut"), T("Shows every plot above your windows. Double-click an icon to open it. Press the shortcut again, or Esc, to hide them."), 56)
	s.peek = l.toggle(cy, idPeek)
	cy, _ = l.row(T("Keys"), T("Click the box and press the shortcut you want."), 220)
	s.peekKey = l.add(WS_EX_CLIENTEDGE, "msctls_hotkey32", "", WS_TABSTOP, ctlR-220, cy-14, 220, 28, idPeekKey)
	sendMessage(s.peekKey, 0x403 /*HKM_SETRULES*/, 0x1|0x2 /*HKCOMB_NONE|HKCOMB_S*/, 0x2|0x4 /*CONTROL|ALT*/)
	cy, _ = l.row(T("Include the Windows key"), "", 56)
	s.peekWin = l.toggle(cy, idPeekWin)

	l.section(T("System"))
	cy, _ = l.row(T("Language"), "", 280)
	s.langCombo = l.combo(cy, 280, idLanguage, T("Automatic (Windows language)"))
	for _, lg := range Languages() {
		sendMessage(s.langCombo, 0x143, 0, up(lg.Name))
	}
	cy, _ = l.row(T("Start Plots with Windows"), "", 56)
	s.startup = l.toggle(cy, idStartup)
	cy, _ = l.row(T("Keep Auto-arrange and Align-to-grid off"), T("Recommended. Explorer would otherwise rearrange your icons."), 56)
	s.flags = l.toggle(cy, idFlags)
	cy, _ = l.row(T("Desktop check rate"), T("How quickly new, deleted and moved icons are picked up."), 280)
	s.pollBar, s.pollLabel = l.slider(cy, 1, 5, idPoll)
	cy, _ = l.row(T("Refresh the desktop"), T("Re-read every icon now."), 120)
	l.button(T("Refresh"), cy, ctlR-120, 120, idRefresh)
	s.status = l.text("", cardL, l.y+4, cardW, 36, true)
	l.y += 40
	l.done()
}

func (s *Settings) buildBackupPage() {
	l := s.newPage("backup")
	l.title(T("Backup & restore"), T("Your plots, their icons and every setting, in one .plots file."))

	l.section(T("Export and import"))
	cy, _ := l.row(T("Export"), T("Save your layout to a file, e.g. to move it to another PC."), 140)
	l.button(T("Export…"), cy, ctlR-140, 140, idExport)
	cy, _ = l.row(T("Import"), T("Replace your plots and settings with a file's. Your current layout is backed up first."), 140)
	l.button(T("Import…"), cy, ctlR-140, 140, idImport)

	l.section(T("Scheduled backups"))
	cy, _ = l.row(T("Back up automatically"), "", 220)
	var labels []string
	for _, b := range BackupSchedules {
		labels = append(labels, T(b.Label))
	}
	s.backupCombo = l.combo(cy, 220, idBackupEvery, labels...)
	cy, _ = l.row(T("Backup folder"), "", -1)
	s.backupDir = l.edit(cy, cardL+18, cardW-36-96-80-16, 0, 0x800 /*ES_READONLY*/)
	l.button(T("Choose…"), cy, ctlR-96-80-8, 96, idBackupDir)
	l.button(T("Open"), cy, ctlR-80, 80, idBackupOpen)
	cy, _ = l.row(T("Backups to keep"), T("The oldest are deleted. Only Plots' own backup files are ever touched."), 280)
	s.keepBar, s.keepLabel = l.slider(cy, 1, 50, 0)
	cy, s.backupStatus = l.row(T("Back up now"), " ", 140)
	l.button(T("Back up now"), cy, ctlR-140, 140, idBackupNow)
	l.done()
}

// ------------------------------------------------------------------ pages & scrolling

func (s *Settings) showPage(name string) {
	if s.pages[name] == nil {
		return
	}
	s.page = name
	s.scrollTo(0, true)
	for n, pg := range s.pages {
		for _, h := range pg.ctls {
			if n == name {
				call(pShowWindow, h, SW_SHOWNA)
			} else {
				call(pShowWindow, h, SW_HIDE)
			}
		}
	}
	s.updateScrollBar()
	switch name {
	case "rules":
		s.refreshRules()
	case "backup":
		s.refreshBackup()
	}
	if name != "plot" {
		sendMessage(s.list, LB_SETCURSEL, ^uintptr(0), 0)
		s.refreshGeneral()
		s.refreshLook()
	}
	for _, b := range s.navBtns {
		call(pInvalidateRect, b, 0, 1)
	}
	s.refreshAbout()
	call(pInvalidateRect, s.pane, 0, 1)
}

func (s *Settings) paneHeight() int {
	var cr RECT
	call(pGetClientRect, s.pane, uintptr(unsafe.Pointer(&cr)))
	return int(cr.Bottom)
}

type scrollInfo struct {
	Size, Mask    uint32
	Min, Max      int32
	Page          uint32
	Pos, TrackPos int32
}

func (s *Settings) updateScrollBar() {
	pg := s.pages[s.page]
	if pg == nil {
		return
	}
	si := scrollInfo{Mask: 0x1 | 0x2 | 0x4 /*RANGE|PAGE|POS*/, Max: int32(pg.height - 1), Page: uint32(s.paneHeight()), Pos: int32(s.scrollY)}
	si.Size = uint32(unsafe.Sizeof(si))
	call(pSetScrollInfo, s.pane, 1 /*SB_VERT*/, uintptr(unsafe.Pointer(&si)), 1)
}

// scrollTo moves the current page's controls so content row y is at the top.
func (s *Settings) scrollTo(y int, force bool) {
	pg := s.pages[s.page]
	if pg == nil {
		return
	}
	y = max(0, min(y, pg.height-s.paneHeight()))
	if y == s.scrollY && !force {
		return
	}
	s.scrollY = y
	sendMessage(s.pane, 0x000B /*WM_SETREDRAW*/, 0, 0)
	for _, list := range s.pages {
		for _, h := range list.ctls {
			b := s.base[h]
			off := 0
			if list == pg {
				off = y
			}
			call(pSetWindowPos, h, 0, uintptr(b.X), uintptr(b.Y-off), 0, 0, SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE)
		}
	}
	sendMessage(s.pane, 0x000B, 1, 0)
	call(pRedrawWindow, s.pane, 0, 0, RDW_INVALIDATE|RDW_ERASE|RDW_ALLCHILDREN|0x100 /*RDW_UPDATENOW*/)
	s.updateScrollBar()
}

// scrollIntoView keeps the focused control visible when tabbing through a page.
func (s *Settings) scrollIntoView(h HWND) {
	b, ok := s.base[h]
	if !ok || call(pIsWindowVisible, h) == 0 {
		return // not on the page being shown
	}
	r := windowRect(h)
	ph := s.paneHeight()
	if b.Y-s.scrollY < s.sc(8) {
		s.scrollTo(b.Y-s.sc(48), false)
	} else if b.Y-s.scrollY+r.H() > ph-s.sc(8) {
		s.scrollTo(b.Y+r.H()-ph+s.sc(48), false)
	}
}

// wantsWheel reports whether a mouse-wheel message aimed at h should scroll
// the page instead (so scrolling past a slider or list box doesn't change it).
func (s *Settings) wantsWheel(h HWND) bool {
	if h == s.pane {
		return false
	}
	if call(pGetParent, h) != s.pane {
		return false
	}
	var cls [32]uint16
	call(pGetClassNameW, h, uintptr(unsafe.Pointer(&cls[0])), 32)
	switch strings.ToLower(windows.UTF16ToString(cls[:])) {
	case "msctls_trackbar32":
		return true
	case "combobox":
		return sendMessage(h, 0x157 /*CB_GETDROPPEDSTATE*/, 0, 0) == 0
	}
	return false
}

func (s *Settings) wheel(wp uintptr) {
	delta := int(int16(hiword(wp)))
	s.scrollTo(s.scrollY-delta*s.sc(56)/120, false)
}

func (s *Settings) paneWndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_COMMAND, WM_HSCROLL, WM_CTLCOLORSTATIC, WM_CTLCOLORBTN, 0x0134 /*WM_CTLCOLORLISTBOX*/, 0x0133 /*WM_CTLCOLOREDIT*/, 0x002B /*WM_DRAWITEM*/, 0x002C /*WM_MEASUREITEM*/ :
		return s.wndProc(s.hwnd, msg, wp, lp)
	case 0x0115: // WM_VSCROLL
		ph := s.paneHeight()
		y := s.scrollY
		switch loword(wp) {
		case 0: // SB_LINEUP
			y -= s.sc(40)
		case 1: // SB_LINEDOWN
			y += s.sc(40)
		case 2: // SB_PAGEUP
			y -= ph - s.sc(40)
		case 3: // SB_PAGEDOWN
			y += ph - s.sc(40)
		case 4, 5: // SB_THUMBPOSITION, SB_THUMBTRACK
			y = int(hiword(wp))
			if pg := s.pages[s.page]; pg != nil && pg.height > 0xFFFF {
				y = s.scrollY
			}
		case 6: // SB_TOP
			y = 0
		case 7: // SB_BOTTOM
			y = 1 << 30
		}
		s.scrollTo(y, false)
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		s.wheel(wp)
		return 0
	case WM_ERASEBKGND:
		return 1
	case WM_PAINT:
		s.paintPane()
		return 0
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

// paintPane draws the background and the page's cards (anti-aliased).
func (s *Settings) paintPane() {
	var ps PAINTSTRUCT
	hdc := call(pBeginPaint, s.pane, uintptr(unsafe.Pointer(&ps)))
	defer call(pEndPaint, s.pane, uintptr(unsafe.Pointer(&ps)))
	var cr RECT
	call(pGetClientRect, s.pane, uintptr(unsafe.Pointer(&cr)))
	w, h := int(cr.Right), int(cr.Bottom)
	if w <= 0 || h <= 0 {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bgr(clrBg)), image.Point{}, draw.Src)
	if pg := s.pages[s.page]; pg != nil {
		rr := float32(6 * s.scale)
		line := bgr(clrCardLine)
		white := bgr(clrWhite)
		for _, c := range pg.cards {
			y0, y1 := float32(c.T-s.scrollY), float32(c.B-s.scrollY)
			if y1 < 0 || y0 > float32(h) {
				continue
			}
			x0, x1 := float32(c.L), float32(c.R)
			fill(img, line, func(z *vector.Rasterizer) { roundRectPath(z, x0, y0, x1, y1, rr, rr, false) })
			fill(img, white, func(z *vector.Rasterizer) { roundRectPath(z, x0+1, y0+1, x1-1, y1-1, rr-1, rr-1, false) })
		}
	}
	blit(hdc, RECT{0, 0, int32(w), int32(h)}, img)
}

func (s *Settings) paint() {
	var ps PAINTSTRUCT
	hdc := call(pBeginPaint, s.hwnd, uintptr(unsafe.Pointer(&ps)))
	var cr RECT
	call(pGetClientRect, s.hwnd, uintptr(unsafe.Pointer(&cr)))
	call(pFillRect, hdc, uintptr(unsafe.Pointer(&cr)), s.bgBrush)
	call(pEndPaint, s.hwnd, uintptr(unsafe.Pointer(&ps)))
}

// ------------------------------------------------------------------ owner drawing

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

// drawToggle paints an anti-aliased on/off switch.
func (s *Settings) drawToggle(di *DRAWITEMSTRUCT) {
	r := di.RcItem
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	on := toggleState[di.HwndItem]
	enabled := call(pIsWindowEnabled, di.HwndItem) != 0
	blit(di.HDC, r, RenderToggleRow(w, h, s.scale, on, enabled))
	if di.ItemState&0x10 != 0 { // ODS_FOCUS
		fr := RECT{r.Left + 2, r.Top + 2, r.Right - 2, r.Bottom - 2}
		call(pDrawFocusRect, di.HDC, uintptr(unsafe.Pointer(&fr)))
	}
}

func (s *Settings) navPage(id uint32) (page, glyph string) {
	switch id {
	case idGeneral:
		return "general", "\uE713"
	case idAppearance:
		return "look", "\uE790"
	case idRulesNav:
		return "rules", "\uE71C"
	case idBackupNav:
		return "backup", "\uE81C"
	case idAboutNav:
		return "about", "\uE946"
	}
	return "", ""
}

func (s *Settings) drawNav(di *DRAWITEMSTRUCT) {
	hdc, r := di.HDC, di.RcItem
	page, glyph := s.navPage(di.CtlID)
	active := s.page == page
	fillRect(hdc, r, clrBg)
	if active || di.ItemState&1 != 0 {
		blit(hdc, r, roundedFill(int(r.Right-r.Left), int(r.Bottom-r.Top), s.scale, clrNavSel, clrBg))
	}
	if active {
		fillRect(hdc, RECT{r.Left, r.Top + int32(s.sc(10)), r.Left + int32(s.sc(3)), r.Bottom - int32(s.sc(10))}, clrSelLine)
	}
	call(pSetBkMode, hdc, 1)
	call(pSetTextColor, hdc, uintptr(clrText))
	old := call(pSelectObject, hdc, s.iconFont)
	ir := RECT{r.Left + int32(s.sc(14)), r.Top, r.Left + int32(s.sc(38)), r.Bottom}
	call(pDrawTextW, hdc, up(glyph), ^uintptr(0), uintptr(unsafe.Pointer(&ir)), 0x1|0x4|0x20 /*CENTER|VCENTER|SINGLELINE*/)
	call(pSelectObject, hdc, s.font)
	tr := RECT{r.Left + int32(s.sc(48)), r.Top, r.Right, r.Bottom}
	tr.Right -= int32(s.sc(6))
	call(pDrawTextW, hdc, up(windowText(di.HwndItem)), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20|0x800|0x8000 /*NOPREFIX|END_ELLIPSIS*/)
	call(pSelectObject, hdc, old)
	if di.ItemState&0x10 != 0 && di.ItemState&0x200 == 0 { // focus, not ODS_NOFOCUSRECT
		fr := RECT{r.Left + 1, r.Top + 1, r.Right - 1, r.Bottom - 1}
		call(pDrawFocusRect, hdc, uintptr(unsafe.Pointer(&fr)))
	}
}

// roundedFill is a w x h rounded rectangle of colour c on background bg.
func roundedFill(w, h int, scale float64, c, bg uint32) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bgr(bg)), image.Point{}, draw.Src)
	rr := float32(5 * scale)
	fill(img, bgr(c), func(z *vector.Rasterizer) { roundRectPath(z, 0, 0, float32(w), float32(h), rr, rr, false) })
	return img
}

func (s *Settings) drawItem(di *DRAWITEMSTRUCT) {
	hdc, r := di.HDC, di.RcItem
	selected := di.ItemState&1 != 0 && s.page == "plot"
	fillRect(hdc, r, clrBg)
	if int(di.ItemID) < 0 || int(di.ItemID) >= len(s.ids) {
		return
	}
	p := s.a.cfg.Plot(s.ids[di.ItemID])
	if p == nil {
		return
	}
	if selected {
		blit(hdc, r, roundedFill(int(r.Right-r.Left), int(r.Bottom-r.Top), s.scale, clrNavSel, clrBg))
		fillRect(hdc, RECT{r.Left, r.Top + int32(s.sc(10)), r.Left + int32(s.sc(3)), r.Bottom - int32(s.sc(10))}, clrSelLine)
	}
	c := ParseHex(p.Color)
	if s.a.cfg.UniformLook {
		c = ParseHex(s.a.cfg.UniformColor)
	}
	sw := int32(s.sc(14))
	sy := r.Top + (r.Bottom-r.Top-sw)/2
	sx := r.Left + int32(s.sc(18))
	blit(hdc, RECT{sx, sy, sx + sw, sy + sw}, roundedFill(int(sw), int(sw), s.scale*0.7, uint32(c.R)|uint32(c.G)<<8|uint32(c.B)<<16, func() uint32 {
		if selected {
			return clrNavSel
		}
		return clrBg
	}()))
	call(pSetBkMode, hdc, 1 /*TRANSPARENT*/)
	old := call(pSelectObject, hdc, s.font)
	tr := RECT{sx + sw + int32(s.sc(16)), r.Top, r.Right - int32(s.sc(8)), r.Bottom}
	var tags []string
	if s.a.cfg.InboxPlot == p.ID {
		tags = append(tags, T("new items"))
	}
	if p.Folder != "" {
		tags = append(tags, T("folder"))
	}
	if len(tags) > 0 {
		call(pSetTextColor, hdc, uintptr(clrMuted))
		tag := strings.Join(tags, " · ")
		// the tag gets at most 45% of the row; the plot name keeps the rest
		var m RECT
		call(pDrawTextW, hdc, up(tag), ^uintptr(0), uintptr(unsafe.Pointer(&m)), 0x20|0x400|0x800 /*SINGLELINE|CALCRECT|NOPREFIX*/)
		tw := min(m.Right, (tr.Right-tr.Left)*45/100)
		trTag := RECT{tr.Right - tw, tr.Top, tr.Right, tr.Bottom}
		call(pDrawTextW, hdc, up(tag), ^uintptr(0), uintptr(unsafe.Pointer(&trTag)), 0x2|0x4|0x20|0x8000|0x800 /*RIGHT|VCENTER|SINGLELINE|END_ELLIPSIS|NOPREFIX*/)
		tr.Right -= tw + int32(s.sc(10))
	}
	call(pSetTextColor, hdc, uintptr(clrText))
	call(pDrawTextW, hdc, up(p.Title), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20|0x8000|0x800 /*VCENTER|SINGLELINE|END_ELLIPSIS|NOPREFIX*/)
	call(pSelectObject, hdc, old)
}

// ------------------------------------------------------------------ toggles

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

func enable(on bool, hs ...HWND) {
	var v uintptr
	if on {
		v = 1
	}
	for _, h := range hs {
		call(pEnableWindow, h, v)
	}
}

// ------------------------------------------------------------------ refresh

// reloadAll re-reads everything (after an import).
func (s *Settings) reloadAll() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	s.cur = ""
	s.refreshList()
	s.refreshGeneral()
	s.refreshLook()
	s.refreshRules()
	s.refreshBackup()
	if len(s.a.cfg.Plots) > 0 && s.page == "plot" {
		s.selectPlot(s.a.cfg.Plots[0].ID)
	}
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
		sendMessage(s.list, LB_ADDSTRING, 0, up(p.Title))
		s.ids = append(s.ids, p.ID)
		if p.ID == s.cur {
			sel = i
		}
	}
	if sel < 0 && len(s.ids) > 0 {
		sel = 0
		s.cur = s.ids[0]
	}
	if len(s.ids) == 0 {
		s.cur = ""
		if s.page == "plot" {
			s.loading = false
			s.showPage("general")
			s.loading = true
		}
	}
	if s.page != "plot" {
		sel = -1
	}
	sendMessage(s.list, LB_SETCURSEL, uintptr(sel), 0)
	s.loadPlot()
}

func (s *Settings) selectPlot(id string) {
	if s == nil || !isWindow(s.hwnd) || s.a.cfg.Plot(id) == nil {
		return
	}
	s.cur = id
	if s.page != "plot" {
		s.showPage("plot")
	}
	for i, x := range s.ids {
		if x == id {
			sendMessage(s.list, LB_SETCURSEL, uintptr(i), 0)
		}
	}
	call(pInvalidateRect, s.list, 0, 1)
	s.loadPlot()
}

func (s *Settings) loadPlot() {
	s.loading = true
	defer func() { s.loading = false }()
	p := s.a.cfg.Plot(s.cur)
	if p == nil {
		setWindowText(s.title, "")
		setWindowText(s.plotHeader, T("Plot"))
		setWindowText(s.monLabel, T("Add a plot to get started."))
		return
	}
	if windowText(s.title) != p.Title && call(pGetFocus) != s.title {
		setWindowText(s.title, p.Title)
	}
	setWindowText(s.plotHeader, p.Title)
	if names := s.tabNamesText(p); windowText(s.tabsEdit) != names && call(pGetFocus) != s.tabsEdit {
		setWindowText(s.tabsEdit, names)
	}
	setCheck(s.rollToggle, p.Collapsed)
	setWindowText(s.folderEdit, p.Folder)
	sortIdx := map[string]int{"": 0, "name": 0, "date": 1, "type": 2, "size": 3}[p.FolderSort]
	sendMessage(s.sortCombo, 0x14E, uintptr(sortIdx), 0)
	iconIdx := 0
	for i, n := range iconSizes {
		if n == p.IconSize {
			iconIdx = i + 1
		}
	}
	sendMessage(s.iconCombo, 0x14E, uintptr(iconIdx), 0)
	folder := p.Folder != ""
	enable(folder, s.sortCombo, s.folderClear, s.iconCombo)
	enable(!folder, s.tabsEdit)
	enable(!s.a.cfg.UniformLook, s.swatch, s.chooseBtn, s.opacity)
	sendMessage(s.opacity, TBM_SETPOS, 1, uintptr(p.Opacity))
	setWindowText(s.opLabel, fmt.Sprintf("%d%%", p.Opacity))
	setCheck(s.inbox, s.a.cfg.InboxPlot == p.ID)
	mon := p.Monitor
	for i, m := range s.a.mons {
		if m.Name == p.Monitor {
			mon = Tf("Monitor %d", i+1)
		}
	}
	n := 0
	if v := s.a.eng.View(p.ID); v != nil {
		n = v.Count
	}
	what := Tf("%d icons", len(p.AllItems()))
	if folder {
		what = Tf("%d items in %s", n, filepath.Base(p.Folder))
	}
	setWindowText(s.monLabel, fmt.Sprintf("%s  ·  %d × %d  ·  %s", mon, p.W, p.H, what))
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
	setCheck(s.grow, c.GrowToFit)
	setCheck(s.peek, c.PeekEnabled)
	setCheck(s.peekWin, c.PeekMods&modWin != 0)
	if call(pGetFocus) != s.peekKey {
		sendMessage(s.peekKey, 0x401 /*HKM_SETHOTKEY*/, uintptr(c.PeekKey&0xFF|modsToHotkeyF(c.PeekMods)<<8), 0)
	}
	enable(c.PeekEnabled, s.peekKey, s.peekWin)
	langSel := 0
	for i, lg := range Languages() {
		if lg.Code == c.Language {
			langSel = i + 1
		}
	}
	sendMessage(s.langCombo, 0x14E, uintptr(langSel), 0)
	sendMessage(s.pollBar, TBM_SETPOS, 1, uintptr(c.PollMs/100))
	setWindowText(s.pollLabel, Tf("%.1f s", float64(c.PollMs)/1000))
	sendMessage(s.inboxCombo, 0x14B /*CB_RESETCONTENT*/, 0, 0)
	sendMessage(s.inboxCombo, 0x143 /*CB_ADDSTRING*/, 0, up(T("Off: leave them where Windows puts them")))
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
	setWindowText(s.dlLabel, Tf("From %s", s.a.dl.Dir()))
	if s.a.editing {
		setWindowText(s.editBtn, T("Done editing"))
	} else {
		setWindowText(s.editBtn, T("Edit layout"))
	}
	st := s.a.status
	if c.Hidden {
		st = T("Desktop icons are hidden (tray menu or double-click to show).")
	}
	if s.a.peekErr != "" {
		st = strings.TrimSpace(st + " " + s.a.peekErr)
	}
	setWindowText(s.status, st)
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
		sendMessage(s.fontCombo, 0x143, 0, up(T("Segoe UI Semibold (default)")))
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
	sendMessage(s.radBar, TBM_SETPOS, 1, uintptr(c.CornerRadius))
	setWindowText(s.radLabel, fmt.Sprintf("%d px", c.CornerRadius))
	sendMessage(s.padBar, TBM_SETPOS, 1, uintptr(c.Padding))
	setWindowText(s.padLabel, fmt.Sprintf("%d px", c.Padding))
	sendMessage(s.uniBar, TBM_SETPOS, 1, uintptr(c.UniformOpacity))
	setWindowText(s.uniLabel, fmt.Sprintf("%d%%", c.UniformOpacity))
	setCheck(s.uniform, c.UniformLook)
	s.setBrush(s.titleSw, c.TitleColor)
	s.setBrush(s.headerSw, c.HeaderColor)
	s.setBrush(s.uniSw, c.UniformColor)
	s.setBrush(s.tintSw, c.TintColor)
	enable(c.UniformLook, s.uniSw, s.uniBar)
	enable(c.HeaderStyle == HeaderCustom, s.headerSw)
	sendMessage(s.tintCombo, 0x14E, uintptr(c.TintMode), 0)
	sendMessage(s.tintBar, TBM_SETPOS, 1, uintptr(c.TintStrength))
	setWindowText(s.tintLabel, fmt.Sprintf("%d%%", c.TintStrength))
	enable(c.TintMode != TintOff, s.tintBar)
	setCheck(s.fade, c.FadeIdle)
	sendMessage(s.fadeBar, TBM_SETPOS, 1, uintptr(c.FadeLevel))
	setWindowText(s.fadeLabel, fmt.Sprintf("%d%%", c.FadeLevel))
	enable(c.FadeIdle, s.fadeBar, s.fadeIcons)
	setCheck(s.fadeIcons, c.FadeIcons)
	enable(c.TintMode == TintCustom, s.tintSw)
	// desktop icon size: nearest preset to Explorer's current size
	cur := s.a.desktopIconSize()
	best := -1
	for i, n := range iconSizes {
		if best < 0 || abs(n-cur) < abs(iconSizes[best]-cur) {
			best = i
		}
	}
	if cur == 0 {
		best = 1
	}
	sendMessage(s.deskIconCombo, 0x14E, uintptr(best), 0)
}

func (s *Settings) refreshBackup() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	s.loading = true
	defer func() { s.loading = false }()
	c := s.a.cfg
	for i, b := range BackupSchedules {
		if b.Key == c.BackupEvery {
			sendMessage(s.backupCombo, 0x14E, uintptr(i), 0)
		}
	}
	setWindowText(s.backupDir, s.a.backupDir())
	sendMessage(s.keepBar, TBM_SETPOS, 1, uintptr(c.BackupKeep))
	setWindowText(s.keepLabel, fmt.Sprintf("%d", c.BackupKeep))
	st := DescribeBackup(c, time.Now())
	if s.a.backupMsg != "" {
		st = s.a.backupMsg
	}
	setWindowText(s.backupStatus, st)
}

// lookChanged re-renders every plot after an appearance change.
func (s *Settings) lookChanged() {
	a := s.a
	a.markDirty()
	a.eng.Relayout() // title height / padding may have changed
	a.tick()
	s.refreshLook()
	call(pInvalidateRect, s.list, 0, 1)
	a.save(false)
}

func displayVersion() string {
	v := version
	if len(v) > 0 && v[0] >= '0' && v[0] <= '9' {
		v = "v" + v
	}
	return v
}

func (s *Settings) tabNamesText(p *Plot) string {
	if len(p.Tabs) <= 1 {
		return p.Tabs[0].Name
	}
	var names []string
	for _, t := range p.Tabs {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
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

// ------------------------------------------------------------------ messages

func (s *Settings) wndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	a := s.a
	switch msg {
	case WM_COMMAND:
		id, code := int(loword(wp)), uint32(hiword(wp))
		if code == 0x100 /*EN_SETFOCUS*/ || code == 3 /*CBN_SETFOCUS*/ || code == 6 /*BN_SETFOCUS*/ {
			s.scrollIntoView(lp)
		}
		if s.loading {
			return 0
		}
		if _, ok := toggleState[lp]; ok && lp != 0 && (code == BN_CLICKED || code == 5 /*BN_DOUBLECLICKED*/) {
			toggleState[lp] = !toggleState[lp]
			call(pInvalidateRect, lp, 0, 0)
		}
		if s.aboutCommand(id) {
			a.save(false)
			return 0
		}
		s.command(id, code)
		a.save(false)
		return 0
	case WM_HSCROLL:
		s.slider(lp)
		return 0
	case WM_CTLCOLORSTATIC, WM_CTLCOLORBTN, 0x0133 /*WM_CTLCOLOREDIT*/ :
		hdc := wp
		if msg == 0x0133 {
			call(pSetTextColor, hdc, uintptr(clrText))
			call(pSetBkColor, hdc, uintptr(clrWhite))
			return s.cardBrush
		}
		if lp == s.swatch && s.brush != 0 {
			return s.brush
		}
		if b, ok := s.brushes[lp]; ok {
			return b
		}
		call(pSetBkMode, hdc, 1)
		switch {
		case lp == s.linkLabel:
			call(pSetTextColor, hdc, uintptr(clrSelLine))
		case s.muted[lp]:
			call(pSetTextColor, hdc, uintptr(clrMuted))
		default:
			call(pSetTextColor, hdc, uintptr(clrText))
		}
		if s.onCard[lp] {
			call(pSetBkColor, hdc, uintptr(clrWhite))
			return s.cardBrush
		}
		call(pSetBkColor, hdc, uintptr(clrBg))
		return s.bgBrush
	case 0x0134: // WM_CTLCOLORLISTBOX
		if s.onCard[lp] {
			return s.cardBrush
		}
		return s.bgBrush
	case WM_ERASEBKGND:
		return 1
	case WM_PAINT:
		s.paint()
		return 0
	case 0x020A: // WM_MOUSEWHEEL over the window: scroll the page
		pt := Point{int(int16(loword(lp))), int(int16(hiword(lp)))}
		if windowRect(s.pane).Contains(pt) {
			s.wheel(wp)
			return 0
		}
	case 0x002C: // WM_MEASUREITEM
		mi := (*MEASUREITEMSTRUCT)(unsafe.Pointer(lp))
		mi.ItemHeight = uint32(s.sc(38))
		return 1
	case 0x002B: // WM_DRAWITEM
		di := (*DRAWITEMSTRUCT)(unsafe.Pointer(lp))
		if di.CtlID == idList {
			s.drawItem(di)
			return 1
		}
		if di.CtlID == idSideLogo {
			blit(di.HDC, di.RcItem, logoTile(int(di.RcItem.Right-di.RcItem.Left), clrBg))
			return 1
		}
		if s.drawAbout(di) {
			return 1
		}
		if p, _ := s.navPage(di.CtlID); p != "" {
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
		for _, f := range []uintptr{s.font, s.bold, s.bigFont, s.iconFont, s.bgBrush, s.cardBrush} {
			call(pDeleteObject, f)
		}
		for _, b := range s.brushes {
			call(pDeleteObject, b)
		}
		for hh := range toggleState {
			if !isWindow(hh) {
				delete(toggleState, hh)
			}
		}
		a.settings = nil
		a.save(true)
		return 0
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

func (s *Settings) command(id int, code uint32) {
	a := s.a
	p := a.cfg.Plot(s.cur)
	switch id {
	case 2: // IDCANCEL (Esc)
		call(pDestroyWindow, s.hwnd)
	case idList:
		if code == LBN_SELCHANGE {
			i := int(sendMessage(s.list, LB_GETCURSEL, 0, 0))
			if i >= 0 && i < len(s.ids) {
				a.selected = s.ids[i]
				s.selectPlot(s.ids[i])
				if a.editing {
					a.syncOverlays()
				}
			}
		}
	case idGeneral:
		s.showPage("general")
	case idAppearance:
		s.showPage("look")
	case idRulesNav:
		s.showPage("rules")
	case idBackupNav:
		s.showPage("backup")
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
			a.markDirty()
			a.updatePanels()
			call(pInvalidateRect, s.list, 0, 1)
			s.refreshListText()
		}
	case idChoose, idSwatch:
		if p != nil {
			if c, ok := chooseColor(s.hwnd, ParseHex(p.Color)); ok {
				p.Color = ToHex(c)
				a.plotChanged()
			}
		}
	case idTabs:
		if code == EN_CHANGE && p != nil {
			p.SetTabNames(strings.Split(windowText(s.tabsEdit), ","))
			a.markDirty()
			a.eng.Relayout()
			a.tick()
		}
	case idFolderBrowse:
		if p != nil {
			if n := len(p.AllItems()); n > 0 && p.Folder == "" {
				messageBox(s.hwnd, Tf("\"%s\" holds %d desktop icon(s). Drag them to another plot first, or use \"New live folder plot…\" from the tray menu to make a separate plot.", p.Title, n), "Plots", MB_OK|MB_ICONINFO)
				break
			}
			if dir := pickFolder(s.hwnd, Tf("Choose the folder to show in \"%s\"", p.Title)); dir != "" {
				p.Folder = dir
				if p.Title == "" || strings.HasPrefix(p.Title, "Plot ") {
					p.Title = filepath.Base(dir)
				}
				p.SetTabNames(nil)
				a.markDirty()
				a.eng.Relayout()
				a.tick()
				s.refreshList()
			}
		}
	case idFolderClear:
		if p != nil && p.Folder != "" {
			p.Folder = ""
			a.markDirty()
			a.eng.Relayout()
			a.tick()
			s.loadPlot()
			call(pInvalidateRect, s.list, 0, 1)
		}
	case idFolderSort:
		if code == 1 && p != nil {
			p.FolderSort = []string{"name", "date", "type", "size"}[max(0, int(sendMessage(s.sortCombo, 0x147, 0, 0)))%4]
			if fs := a.folders[p.ID]; fs != nil {
				fs.readAt = time.Time{}
			}
			a.markDirty()
			a.tick()
		}
	case idIconSize:
		if code == 1 && p != nil {
			i := int(sendMessage(s.iconCombo, 0x147, 0, 0))
			p.IconSize = 0
			if i > 0 && i <= len(iconSizes) {
				p.IconSize = iconSizes[i-1]
			}
			a.markDirty()
			a.eng.Relayout()
			a.tick()
		}
	case idDeskIcon:
		if code == 1 {
			if i := int(sendMessage(s.deskIconCombo, 0x147, 0, 0)); i >= 0 && i < len(iconSizes) {
				a.setDesktopIconSize(iconSizes[i])
			}
		}
	case idRoll:
		if p != nil {
			p.Collapsed = checked(s.rollToggle)
			a.markDirty()
			a.tick()
		}
	case idGrow:
		a.cfg.GrowToFit = checked(s.grow)
		a.markDirty()
		a.eng.Relayout()
		a.tick()
	case idRuleList:
		if code == LBN_SELCHANGE {
			s.ruleSel = int(sendMessage(s.ruleList, LB_GETCURSEL, 0, 0))
			s.loadRule()
		}
	case idPreset:
		if code == 1 {
			i := int(sendMessage(s.presetCombo, 0x147, 0, 0)) - 1
			if i >= 0 && i < len(RulePresets) {
				if r := s.curRule(); r == nil {
					s.addRule(RulePresets[i].Pattern)
				} else {
					r.Pattern = RulePresets[i].Pattern
					s.ruleEdited()
					s.loadRule()
				}
			}
			s.loading = true
			sendMessage(s.presetCombo, 0x14E, 0, 0)
			s.loading = false
		}
	case idPattern:
		if code == EN_CHANGE {
			if r := s.curRule(); r != nil {
				r.Pattern = windowText(s.patEdit)
				s.ruleEdited()
			}
		}
	case idRulePlot:
		if code == 1 {
			if r := s.curRule(); r != nil {
				i := int(sendMessage(s.rulePlot, 0x147, 0, 0))
				if i >= 0 && i < len(a.cfg.Plots) {
					r.PlotID = a.cfg.Plots[i].ID
					r.Tab = ""
					s.ruleEdited()
					s.loadRule()
				}
			}
		}
	case idRuleTab:
		if code == 1 {
			if r := s.curRule(); r != nil {
				if rp := a.cfg.Plot(r.PlotID); rp != nil {
					i := int(sendMessage(s.ruleTab, 0x147, 0, 0))
					if i >= 0 && i < len(rp.Tabs) {
						r.Tab = rp.Tabs[i].Name
						s.ruleEdited()
					}
				}
			}
		}
	case idRuleAdd:
		s.addRule("")
		call(pSetFocus, s.patEdit)
	case idRuleDel:
		if s.ruleSel >= 0 && s.ruleSel < len(a.cfg.Rules) {
			a.cfg.Rules = append(a.cfg.Rules[:s.ruleSel], a.cfg.Rules[s.ruleSel+1:]...)
			if s.ruleSel >= len(a.cfg.Rules) {
				s.ruleSel = len(a.cfg.Rules) - 1
			}
			a.markDirty()
			s.refreshRules()
		}
	case idRuleApply:
		n := a.eng.ApplyRules()
		a.tick()
		setWindowText(s.ruleStatus, Tf("Sorted %d icon(s) using your rules.", n))
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
			a.cfg.HeaderStyle = HeaderCustom
			s.lookChanged()
		}
	case idUniSw, idUniChoose:
		if c, ok := chooseColor(s.hwnd, ParseHex(a.cfg.UniformColor)); ok {
			a.cfg.UniformColor = ToHex(c)
			s.lookChanged()
		}
	case idFade:
		a.cfg.FadeIdle = checked(s.fade)
		if !a.cfg.FadeIdle {
			a.restoreIcons()
		}
		s.lookChanged()
	case idFadeIcons:
		a.cfg.FadeIcons = checked(s.fadeIcons)
		if !a.cfg.FadeIcons {
			a.restoreIcons()
		}
		s.lookChanged()
	case idUniform:
		a.cfg.UniformLook = checked(s.uniform)
		s.lookChanged()
		s.loadPlot()
	case idTint:
		if code == 1 {
			a.cfg.TintMode = max(0, int(sendMessage(s.tintCombo, 0x147, 0, 0)))
			s.lookChanged()
		}
	case idTintSw, idTintChoose:
		if c, ok := chooseColor(s.hwnd, ParseHex(a.cfg.TintColor)); ok {
			a.cfg.TintColor = ToHex(c)
			if a.cfg.TintMode == TintOff {
				a.cfg.TintMode = TintCustom
			}
			s.lookChanged()
		}
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
		a.publishHookState()
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
	case idPeek, idPeekWin:
		a.cfg.PeekEnabled = checked(s.peek)
		if checked(s.peekWin) {
			a.cfg.PeekMods |= modWin
		} else {
			a.cfg.PeekMods &^= modWin
		}
		a.markDirty()
		a.registerPeek()
		s.refreshGeneral()
	case idPeekKey:
		if code == EN_CHANGE {
			v := sendMessage(s.peekKey, 0x402 /*HKM_GETHOTKEY*/, 0, 0)
			vk, hf := int(v&0xFF), int(v>>8&0xFF)
			if vk != 0 {
				a.cfg.PeekKey = vk
				a.cfg.PeekMods = hotkeyFToMods(hf) | a.cfg.PeekMods&modWin
				a.markDirty()
				a.registerPeek()
				setWindowText(s.status, a.peekErr)
			}
		}
	case idLanguage:
		if code == 1 {
			i := int(sendMessage(s.langCombo, 0x147, 0, 0))
			langs := Languages()
			code := ""
			if i > 0 && i <= len(langs) {
				code = langs[i-1].Code
			}
			if code != a.cfg.Language {
				a.cfg.Language = code
				a.markDirty()
				a.save(true)
				a.applyLanguage()
				a.languageChanged(s.page)
			}
		}
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
	case idExport:
		a.exportLayout(s.hwnd)
	case idImport:
		a.importLayout(s.hwnd)
	case idBackupEvery:
		if code == 1 {
			if i := int(sendMessage(s.backupCombo, 0x147, 0, 0)); i >= 0 && i < len(BackupSchedules) {
				a.cfg.BackupEvery = BackupSchedules[i].Key
				a.markDirty()
				nextBackupCheck = time.Time{}
				a.scheduledBackup()
				s.refreshBackup()
			}
		}
	case idBackupDir:
		if dir := pickFolder(s.hwnd, T("Choose where Plots saves its backups")); dir != "" {
			a.cfg.BackupDir = dir
			a.markDirty()
			s.refreshBackup()
		}
	case idBackupOpen:
		dir := a.backupDir()
		os.MkdirAll(dir, 0o755)
		openURL(dir)
	case idBackupNow:
		a.backupNow(s.hwnd)
		s.refreshBackup()
	}
}

// refreshListText updates plot names in the sidebar and combos without
// rebuilding them (keeps typing in the title box smooth).
func (s *Settings) refreshListText() {
	for i, id := range s.ids {
		if p := s.a.cfg.Plot(id); p != nil {
			s.loading = true
			sel := sendMessage(s.list, LB_GETCURSEL, 0, 0)
			sendMessage(s.list, 0x182 /*LB_DELETESTRING*/, uintptr(i), 0)
			sendMessage(s.list, 0x181 /*LB_INSERTSTRING*/, uintptr(i), up(p.Title))
			sendMessage(s.list, LB_SETCURSEL, sel, 0)
			s.loading = false
		}
	}
}

func (s *Settings) slider(lp uintptr) {
	a := s.a
	if s.loading {
		return
	}
	v := int(sendMessage(lp, TBM_GETPOS, 0, 0))
	switch lp {
	case s.pollBar:
		if v*100 != a.cfg.PollMs {
			a.cfg.PollMs = v * 100
			setWindowText(s.pollLabel, Tf("%.1f s", float64(a.cfg.PollMs)/1000))
			call(pSetTimer, a.main, timerTick, uintptr(a.cfg.PollMs), 0)
			a.markDirty()
		}
	case s.keepBar:
		if v != a.cfg.BackupKeep {
			a.cfg.BackupKeep = v
			setWindowText(s.keepLabel, fmt.Sprintf("%d", v))
			a.markDirty()
		}
	case s.opacity:
		if p := a.cfg.Plot(s.cur); p != nil && p.Opacity != v {
			p.Opacity = v
			setWindowText(s.opLabel, fmt.Sprintf("%d%%", v))
			a.markDirty()
			a.updatePanels()
			if a.editing {
				a.syncOverlays()
			}
		}
	case s.fadeBar:
		if v != a.cfg.FadeLevel {
			a.cfg.FadeLevel = v
			setWindowText(s.fadeLabel, fmt.Sprintf("%d%%", v))
			a.markDirty()
			a.updatePanels()
		}
	case s.sizeBar, s.padBar, s.radBar, s.uniBar, s.tintBar:
		ptr := map[HWND]*int{s.sizeBar: &a.cfg.TitleSize, s.padBar: &a.cfg.Padding, s.radBar: &a.cfg.CornerRadius, s.uniBar: &a.cfg.UniformOpacity, s.tintBar: &a.cfg.TintStrength}[lp]
		if *ptr != v {
			*ptr = v
			s.lookChanged()
		}
	}
	a.save(false)
}

// ------------------------------------------------------------------ rules

func (s *Settings) curRule() *Rule {
	if s.ruleSel >= 0 && s.ruleSel < len(s.a.cfg.Rules) {
		return s.a.cfg.Rules[s.ruleSel]
	}
	return nil
}

func ruleLabel(c *Config, r *Rule) string {
	dest := T("(choose a plot)")
	if p := c.Plot(r.PlotID); p != nil {
		dest = p.Title
		if len(p.Tabs) > 1 {
			dest += " › " + p.TabByName(r.Tab).Name
		}
	}
	pat := r.Pattern
	if strings.TrimSpace(pat) == "" {
		pat = T("(no file types yet)")
	}
	return pat + "   →   " + dest
}

func (s *Settings) addRule(pattern string) {
	c := s.a.cfg
	r := &Rule{Pattern: pattern}
	if len(c.Plots) > 0 {
		r.PlotID = c.Plots[0].ID
	}
	c.Rules = append(c.Rules, r)
	s.ruleSel = len(c.Rules) - 1
	s.a.markDirty()
	s.refreshRules()
}

// ruleEdited refreshes the selected row without disturbing the edit box.
func (s *Settings) ruleEdited() {
	s.a.markDirty()
	if r := s.curRule(); r != nil {
		s.loading = true
		sendMessage(s.ruleList, 0x182 /*LB_DELETESTRING*/, uintptr(s.ruleSel), 0)
		sendMessage(s.ruleList, 0x181 /*LB_INSERTSTRING*/, uintptr(s.ruleSel), up(ruleLabel(s.a.cfg, r)))
		sendMessage(s.ruleList, LB_SETCURSEL, uintptr(s.ruleSel), 0)
		s.loading = false
	}
	s.a.save(false)
}

func (s *Settings) refreshRules() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	s.loading = true
	sendMessage(s.ruleList, LB_RESETCONTENT, 0, 0)
	for _, r := range s.a.cfg.Rules {
		sendMessage(s.ruleList, LB_ADDSTRING, 0, up(ruleLabel(s.a.cfg, r)))
	}
	if s.ruleSel >= len(s.a.cfg.Rules) {
		s.ruleSel = len(s.a.cfg.Rules) - 1
	}
	if s.ruleSel < 0 && len(s.a.cfg.Rules) > 0 {
		s.ruleSel = 0
	}
	sendMessage(s.ruleList, LB_SETCURSEL, uintptr(s.ruleSel), 0)
	s.loading = false
	s.loadRule()
}

func (s *Settings) loadRule() {
	s.loading = true
	defer func() { s.loading = false }()
	c := s.a.cfg
	sendMessage(s.rulePlot, 0x14B, 0, 0)
	for _, p := range c.Plots {
		sendMessage(s.rulePlot, 0x143, 0, up(p.Title))
	}
	sendMessage(s.ruleTab, 0x14B, 0, 0)
	r := s.curRule()
	enable(r != nil, s.patEdit, s.rulePlot, s.ruleTab)
	if r == nil {
		setWindowText(s.patEdit, "")
		return
	}
	if windowText(s.patEdit) != r.Pattern {
		setWindowText(s.patEdit, r.Pattern)
	}
	for i, p := range c.Plots {
		if p.ID == r.PlotID {
			sendMessage(s.rulePlot, 0x14E, uintptr(i), 0)
			for j, t := range p.Tabs {
				name := t.Name
				if name == "" {
					name = T("(only tab)")
				}
				sendMessage(s.ruleTab, 0x143, 0, up(name))
				if strings.EqualFold(t.Name, r.Tab) || (r.Tab == "" && j == 0) {
					sendMessage(s.ruleTab, 0x14E, uintptr(j), 0)
				}
			}
		}
	}
}
