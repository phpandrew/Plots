//go:build windows

package main

import (
	"fmt"
	"image/color"
	"math"
	"unsafe"
)

const (
	NIM_ADD        = 0
	NIM_MODIFY     = 1
	NIM_DELETE     = 2
	NIM_SETVERSION = 4
	NIF_MESSAGE    = 1
	NIF_ICON       = 2
	NIF_TIP        = 4
)

func (a *App) trayData() *NOTIFYICONDATAW {
	nid := &NOTIFYICONDATAW{HWnd: a.main, UID: 1, UFlags: NIF_MESSAGE | NIF_ICON | NIF_TIP, UCallbackMessage: msgTray, HIcon: a.trayIcon}
	nid.CbSize = uint32(unsafe.Sizeof(*nid))
	copy(nid.SzTip[:], u16s("Plots "+displayVersion()))
	return nid
}

func u16s(s string) []uint16 {
	p := u16(s)
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(p), n*2)) != 0 {
		n++
	}
	return unsafe.Slice(p, n)
}

func (a *App) addTray() {
	nid := a.trayData()
	call(pShell_NotifyIconW, NIM_DELETE, uintptr(unsafe.Pointer(nid)))
	call(pShell_NotifyIconW, NIM_ADD, uintptr(unsafe.Pointer(nid)))
}

func (a *App) removeTray() {
	nid := a.trayData()
	call(pShell_NotifyIconW, NIM_DELETE, uintptr(unsafe.Pointer(nid)))
}

type menu struct {
	h    uintptr
	subs []uintptr
}

func newMenu() *menu { return &menu{h: call(pCreatePopupMenu)} }

func (m *menu) item(id int, text string, checked, disabled bool) {
	flags := uintptr(MF_STRING)
	if checked {
		flags |= MF_CHECKED
	}
	if disabled {
		flags |= MF_GRAYED
	}
	call(pAppendMenuW, m.h, flags, uintptr(id), up(text))
}

func (m *menu) sep() { call(pAppendMenuW, m.h, MF_SEPARATOR, 0, 0) }

func (m *menu) sub(text string, s *menu) {
	call(pAppendMenuW, m.h, MF_POPUP, s.h, up(text))
}

func (m *menu) track(owner HWND, at Point) int {
	call(pSetForegroundWindow, owner)
	r := call(pTrackPopupMenu, m.h, TPM_RETURNCMD|TPM_RIGHTBUTTON|TPM_NONOTIFY, uintptr(at.X), uintptr(at.Y), 0, owner, 0)
	postMessage(owner, 0, 0, 0) // WM_NULL, per TrackPopupMenu docs
	call(pDestroyMenu, m.h)     // also destroys sub-menus
	return int(r)
}

const (
	cmdEdit = 100 + iota
	cmdNewPlot
	cmdSettings
	cmdHide
	cmdRefresh
	cmdExit
	cmdRename
	cmdColour
	cmdInbox
	cmdDelete
	cmdRoll
	cmdTabs
	cmdNewFolder
	cmdOpacityBase = 200
	cmdModeBase    = 400
)

func (a *App) trayMenu() {
	m := newMenu()
	m.item(cmdEdit, "Edit plots", a.editing, false)
	m.item(cmdNewPlot, "New plot", false, false)
	m.item(cmdNewFolder, "New live folder plot…", false, false)
	m.item(cmdSettings, "Settings…", false, false)
	m.sep()
	m.item(cmdHide, "Hide desktop icons && plots", a.cfg.Hidden, false)
	m.item(cmdRefresh, "Refresh now", false, false)
	dm := newMenu()
	cur := a.cfg.PanelMode
	dm.item(cmdModeBase+0, "Automatic", cur == "", false)
	dm.item(cmdModeBase+1, "Mode A (layered)", cur == "slwa", false)
	dm.item(cmdModeBase+2, "Mode B (plain)", cur == "plain", false)
	dm.item(cmdModeBase+3, "Mode C (per-pixel alpha)", cur == "ulw", false)
	m.sub(fmt.Sprintf("Drawing mode (now: %s)", a.panelMode()), dm)
	m.sep()
	m.item(cmdExit, "Exit Plots", false, false)
	a.runCmd(m.track(a.main, cursorPos()), "")
}

func (a *App) plotMenu(id string, at Point) {
	p := a.cfg.Plot(id)
	if p == nil {
		return
	}
	m := newMenu()
	m.item(0, p.Title, false, true)
	m.sep()
	m.item(cmdRename, "Rename…", false, false)
	m.item(cmdColour, "Colour…", false, false)
	op := newMenu()
	for _, v := range []int{0, 20, 40, 60, 80, 100} {
		op.item(cmdOpacityBase+v, fmt.Sprintf("%d%%", v), math.Abs(float64(p.Opacity-v)) < 10, false)
	}
	m.sub("Opacity", op)
	m.item(cmdInbox, "New desktop items go here", a.cfg.InboxPlot == id, false)
	m.item(cmdRoll, "Roll up", p.Collapsed, false)
	m.item(cmdTabs, "Tabs…", false, false)
	m.sep()
	m.item(cmdEdit, "Edit plots", a.editing, false)
	m.item(cmdNewPlot, "New plot", false, false)
	m.item(cmdDelete, "Delete plot…", false, false)
	m.sep()
	m.item(cmdSettings, "Settings…", false, false)
	m.item(cmdHide, "Hide desktop icons && plots", a.cfg.Hidden, false)
	a.runCmd(m.track(a.main, at), id)
}

func (a *App) runCmd(cmd int, plotID string) {
	p := a.cfg.Plot(plotID)
	switch {
	case cmd == cmdEdit:
		if a.editing {
			a.exitEdit()
		} else {
			a.enterEdit()
		}
	case cmd == cmdNewFolder:
		a.newFolderPlot(a.main)
	case cmd == cmdNewPlot:
		a.newPlot(cursorPos())
	case cmd == cmdSettings:
		a.showSettings(plotID)
	case cmd == cmdHide:
		a.setHidden(!a.cfg.Hidden)
	case cmd == cmdRefresh:
		a.refreshNow()
	case cmd == cmdExit:
		a.quit()
	case cmd == cmdRename && p != nil:
		a.showSettings(plotID)
		if a.settings != nil {
			call(pSetFocus, a.settings.title)
			sendMessage(a.settings.title, 0x00B1 /*EM_SETSEL*/, 0, ^uintptr(0))
		}
	case cmd == cmdColour && p != nil:
		if c, ok := chooseColor(a.main, ParseHex(p.Color)); ok {
			p.Color = ToHex(c)
			a.plotChanged()
		}
	case cmd == cmdInbox && p != nil:
		if a.cfg.InboxPlot == p.ID {
			a.cfg.InboxPlot = ""
		} else {
			a.cfg.InboxPlot = p.ID
		}
		a.plotChanged()
	case cmd == cmdRoll && p != nil:
		p.Collapsed = !p.Collapsed
		a.eng.Peek = ""
		a.plotChanged()
		a.tick()
	case cmd == cmdTabs && p != nil:
		a.showSettings(plotID)
		if a.settings != nil {
			call(pSetFocus, a.settings.tabsEdit)
		}
	case cmd == cmdDelete && p != nil:
		a.deletePlot(p.ID, a.main)
	case cmd >= cmdModeBase && cmd <= cmdModeBase+3:
		a.cfg.PanelMode = []string{"", "slwa", "plain", "ulw"}[cmd-cmdModeBase]
		a.markDirty()
		for id := range a.panels {
			a.destroyPanel(id)
		}
		a.tick()
		a.save(true)
	case cmd >= cmdOpacityBase && cmd <= cmdOpacityBase+100 && p != nil:
		p.Opacity = cmd - cmdOpacityBase
		a.plotChanged()
	}
}

// plotChanged re-renders everything after an appearance/config change.
func (a *App) plotChanged() {
	a.markDirty()
	a.updatePanels()
	if a.editing {
		a.syncOverlays()
	}
	if a.settings != nil {
		a.settings.refreshList()
	}
}

var custColors [16]uint32

func chooseColor(owner HWND, c color.NRGBA) (color.NRGBA, bool) {
	cc := CHOOSECOLORW{HwndOwner: owner, RgbResult: uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16,
		LpCustColors: &custColors[0], Flags: CC_RGBINIT | CC_FULLOPEN}
	cc.LStructSize = uint32(unsafe.Sizeof(cc))
	if call(pChooseColorW, uintptr(unsafe.Pointer(&cc))) == 0 {
		return color.NRGBA{}, false
	}
	v := cc.RgbResult
	return color.NRGBA{uint8(v), uint8(v >> 8), uint8(v >> 16), 255}, true
}

// notify shows a Windows notification from the tray icon.
func (a *App) notify(title, text string) {
	nid := a.trayData()
	nid.UFlags = 0x10 // NIF_INFO
	copy(nid.SzInfoTitle[:63], u16s(title))
	copy(nid.SzInfo[:255], u16s(text))
	nid.DwInfoFlags = 0x1 // NIIF_INFO
	call(pShell_NotifyIconW, NIM_MODIFY, uintptr(unsafe.Pointer(nid)))
}
