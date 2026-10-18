//go:build windows

package main

import (
	"fmt"
	"log"
	"strings"
)

// Peek: a global shortcut that shows every plot above all windows.
// Pressing it again (or Esc) hides them.

var (
	pRegisterHotKey   = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey = user32.NewProc("UnregisterHotKey")
)

const (
	hotkeyPeek = 1
	WM_HOTKEY  = 0x0312
	modAlt     = 1
	modCtrl    = 2
	modShift   = 4
	modWin     = 8
)

// HKM_* use HOTKEYF_SHIFT 1, HOTKEYF_CONTROL 2, HOTKEYF_ALT 4.
func modsToHotkeyF(m int) int {
	f := 0
	if m&modShift != 0 {
		f |= 1
	}
	if m&modCtrl != 0 {
		f |= 2
	}
	if m&modAlt != 0 {
		f |= 4
	}
	return f
}

func hotkeyFToMods(f int) int {
	m := 0
	if f&1 != 0 {
		m |= modShift
	}
	if f&2 != 0 {
		m |= modCtrl
	}
	if f&4 != 0 {
		m |= modAlt
	}
	return m
}

func keyName(vk int) string {
	switch {
	case vk == 0x20:
		return "Space"
	case vk >= 'A' && vk <= 'Z', vk >= '0' && vk <= '9':
		return string(rune(vk))
	case vk >= 0x70 && vk <= 0x87:
		return fmt.Sprintf("F%d", vk-0x6F)
	case vk == 0xC0:
		return "`"
	}
	return fmt.Sprintf("key %d", vk)
}

func shortcutText(mods, vk int) string {
	var parts []string
	for _, m := range []struct {
		bit  int
		name string
	}{{modWin, "Win"}, {modCtrl, "Ctrl"}, {modAlt, "Alt"}, {modShift, "Shift"}} {
		if mods&m.bit != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, keyName(vk)), "+")
}

// registerPeek (re)registers the Peek shortcut from the config.
func (a *App) registerPeek() {
	call(pUnregisterHotKey, a.main, hotkeyPeek)
	a.peekErr = ""
	if !a.cfg.PeekEnabled || a.cfg.PeekKey == 0 {
		return
	}
	reg := func() bool {
		r, _, _ := pRegisterHotKey.Call(a.main, hotkeyPeek, uintptr(a.cfg.PeekMods|0x4000 /*MOD_NOREPEAT*/), uintptr(a.cfg.PeekKey))
		return r != 0
	}
	ok := reg()
	if !ok && a.cfg.PeekMods == modCtrl|modAlt && a.cfg.PeekKey == 0x20 {
		// the old default is often taken (e.g. by PowerToys): use the new one
		a.cfg.PeekKey = 'P'
		a.markDirty()
		ok = reg()
	}
	if !ok {
		a.peekErr = Tf("%s is already used by another app. Pick a different Peek shortcut.", shortcutText(a.cfg.PeekMods, a.cfg.PeekKey))
		log.Println("peek:", a.peekErr)
	}
}

// peek shows or hides the plots above all windows (see peekview_windows.go).
func (a *App) peek() { a.togglePeek() }
