//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Dark mode. Plots follows the Windows app theme (Settings › Personalization ›
// Colors), or a theme picked under General › Theme. The settings window, its
// controls and title bar, and Plots' menus switch together.

var (
	darkMode    bool
	themeForced string // snapshot override: "", "light" or "dark"

	pSetWindowTheme        = windows.NewLazySystemDLL("uxtheme.dll").NewProc("SetWindowTheme")
	pDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

	uxSetPreferredAppMode, uxFlushMenuThemes, uxAllowDarkModeForWindow uintptr
)

func init() {
	// undocumented but stable uxtheme exports (by ordinal) used for dark menus and controls
	if h, err := windows.LoadLibrary("uxtheme.dll"); err == nil {
		uxAllowDarkModeForWindow, _ = windows.GetProcAddressByOrdinal(h, 133)
		uxSetPreferredAppMode, _ = windows.GetProcAddressByOrdinal(h, 135)
		uxFlushMenuThemes, _ = windows.GetProcAddressByOrdinal(h, 136)
	}
}

// systemDark reports whether Windows apps are set to dark mode.
func systemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}

// applyTheme picks light or dark from the setting and sets the palette.
func (a *App) applyTheme() {
	mode := a.cfg.Theme
	if themeForced != "" {
		mode = themeForced
	}
	switch mode {
	case "dark":
		darkMode = true
	case "light":
		darkMode = false
	default:
		darkMode = systemDark()
	}
	if darkMode {
		clrWhite = 0x2B2B2B // cards
		clrBg = 0x202020
		clrCardLine = 0x3A3A3A
		clrNavSel = 0x2D2D2D
		clrMuted = 0xA8A8A8
		clrText = 0xF2F2F2
		clrSelLine = 0xFFC24C // BGR of #4CC2FF
	} else {
		clrWhite = 0xFFFFFF
		clrBg = 0xF3F3F3
		clrCardLine = 0xE5E5E5
		clrNavSel = 0xE9E9E9
		clrMuted = 0x6E6A66
		clrText = 0x1C1C1C
		clrSelLine = 0xC06700
	}
	if uxSetPreferredAppMode != 0 {
		m := uintptr(3) // ForceLight
		if darkMode {
			m = 2 // ForceDark
		}
		syscallN(uxSetPreferredAppMode, m)
		if uxFlushMenuThemes != 0 {
			syscallN(uxFlushMenuThemes)
		}
	}
}

func syscallN(p uintptr, args ...uintptr) uintptr {
	r, _, _ := syscallSyscallN(p, args...)
	return r
}

// themeWindow gives a top-level window a dark or light title bar.
func themeWindow(h HWND) {
	v := int32(0)
	if darkMode {
		v = 1
	}
	pDwmSetWindowAttribute.Call(h, 20 /*DWMWA_USE_IMMERSIVE_DARK_MODE*/, uintptr(unsafe.Pointer(&v)), 4)
	themeControl(h, "")
}

// themeControl switches a standard control to the dark or light theme.
func themeControl(h HWND, class string) {
	if uxAllowDarkModeForWindow != 0 {
		syscallN(uxAllowDarkModeForWindow, h, boolArg(darkMode))
	}
	theme := "Explorer"
	switch class {
	case "COMBOBOX", "EDIT":
		theme = "CFD"
	}
	if darkMode {
		theme = "DarkMode_" + theme
	}
	pSetWindowTheme.Call(h, up(theme), 0)
}

func boolArg(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

// themeChanged rebuilds the UI after Windows (or the setting) switches theme.
func (a *App) themeChanged() {
	was := darkMode
	a.applyTheme()
	if was == darkMode {
		return
	}
	if a.settings != nil && isWindow(a.settings.hwnd) {
		a.reopenPage = a.settings.page
		call(pDestroyWindow, a.settings.hwnd)
		postMessage(a.main, msgReopenSettings, 0, 0)
	}
	if a.toolbar != 0 && isWindow(a.toolbar) {
		themeWindow(a.toolbar)
	}
}

// ------------------------------------------------------------------ dark hotkey box

// The standard hotkey control always paints itself white. In dark mode Plots
// draws it instead (same behaviour, just painted to match).

var (
	pSetWindowSubclass = comctl32.NewProc("SetWindowSubclass")
	pDefSubclassProc   = comctl32.NewProc("DefSubclassProc")
	hotkeyProc         uintptr
)

func darkHotkey(h HWND, s *Settings) {
	if !darkMode {
		return
	}
	if hotkeyProc == 0 {
		hotkeyProc = windows.NewCallback(func(h HWND, msg uint32, wp, lp, id, ref uintptr) uintptr {
			if msg == WM_PAINT && app != nil && app.settings != nil {
				app.settings.paintHotkey(h)
				return 0
			}
			if msg == WM_ERASEBKGND {
				return 1
			}
			r, _, _ := pDefSubclassProc.Call(h, uintptr(msg), wp, lp)
			if msg == 0x0007 || msg == 0x0008 || msg == WM_KEYDOWN || msg == 0x0101 || msg == 0x0104 || msg == 0x0105 {
				call(pInvalidateRect, h, 0, 1) // focus or keys changed: repaint
			}
			return r
		})
	}
	pSetWindowSubclass.Call(h, hotkeyProc, 1, 0)
}

func (s *Settings) paintHotkey(h HWND) {
	var ps PAINTSTRUCT
	hdc := call(pBeginPaint, h, uintptr(unsafe.Pointer(&ps)))
	defer call(pEndPaint, h, uintptr(unsafe.Pointer(&ps)))
	var cr RECT
	call(pGetClientRect, h, uintptr(unsafe.Pointer(&cr)))
	fillRect(hdc, cr, clrWhite)
	v := sendMessage(h, 0x402 /*HKM_GETHOTKEY*/, 0, 0)
	text := T("None")
	if vk := int(v & 0xFF); vk != 0 {
		text = shortcutText(hotkeyFToMods(int(v>>8&0xFF)), vk)
	}
	call(pSetBkMode, hdc, 1)
	call(pSetTextColor, hdc, uintptr(clrText))
	old := call(pSelectObject, hdc, s.font)
	tr := RECT{cr.Left + int32(s.sc(6)), cr.Top, cr.Right, cr.Bottom}
	call(pDrawTextW, hdc, up(text), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20|0x800)
	call(pSelectObject, hdc, old)
	if call(pGetFocus) == h {
		fillRect(hdc, RECT{cr.Left, cr.Bottom - int32(s.sc(2)), cr.Right, cr.Bottom}, clrSelLine)
	}
}
