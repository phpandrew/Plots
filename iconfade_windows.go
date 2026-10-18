//go:build windows

package main

import (
	"log"
	"math"
)

// Fading the desktop icons along with the plots. Explorer draws the icons in
// one window, so they can only fade together: they rest faded and come back
// as soon as the mouse is over any plot. Plots changes only that window's
// transparency and always puts it back to fully visible.

var pGetLayeredWindowAttributes = user32.NewProc("GetLayeredWindowAttributes")

var (
	iconFade    = 1.0 // current icon strength, 0..1
	iconHwnd    HWND  // the window whose alpha we changed
	iconAddedEx bool  // we added WS_EX_LAYERED ourselves
	iconFailed  bool
)

func (a *App) iconFadeTarget() float64 {
	if !a.cfg.FadeIdle || !a.cfg.FadeIcons || a.editing || a.cfg.Hidden || a.hoverID != "" || a.drag != nil {
		return 1
	}
	return float64(a.cfg.FadeLevel) / 100
}

// iconTarget is the window that holds the desktop icons: SHELLDLL_DefView
// (already layered on Windows 11 24H2), or the list view inside it.
func (a *App) iconTarget() HWND {
	if a.desk == nil {
		return 0
	}
	if a.desk.DefView != 0 && isWindow(a.desk.DefView) {
		return a.desk.DefView
	}
	return a.desk.ListView
}

// setIconAlpha applies strength k (0..1) to the desktop icons.
func (a *App) setIconAlpha(k float64) {
	h := a.iconTarget()
	if h == 0 || iconFailed {
		return
	}
	if h != iconHwnd {
		iconHwnd, iconAddedEx = h, false
	}
	alpha := uintptr(math.Round(math.Max(0, math.Min(1, k)) * 255))
	ex := call(pGetWindowLongPtrW, h, ^uintptr(19) /*GWL_EXSTYLE*/)
	if ex&WS_EX_LAYERED == 0 {
		if alpha == 255 {
			return
		}
		call(pSetWindowLongPtrW, h, ^uintptr(19), ex|WS_EX_LAYERED)
		iconAddedEx = true
	}
	if r, _, err := pSetLayeredWindowAttributes.Call(h, 0, alpha, LWA_ALPHA); r == 0 {
		log.Printf("icon fade: SetLayeredWindowAttributes failed: %v; fading desktop icons is off", err)
		iconFailed = true
		return
	}
	if alpha == 255 && iconAddedEx {
		call(pSetWindowLongPtrW, h, ^uintptr(19), ex&^WS_EX_LAYERED)
		iconAddedEx = false
	}
}

// iconFadeStep animates the icons toward their target; reports whether it is still moving.
func (a *App) iconFadeStep(dt float64) bool {
	t := a.iconFadeTarget()
	if math.Abs(iconFade-t) < 0.001 {
		return false
	}
	speed := 1 / 0.18
	if t < iconFade {
		speed = 1 / 0.4
	}
	if iconFade < t {
		iconFade = math.Min(t, iconFade+dt*speed)
	} else {
		iconFade = math.Max(t, iconFade-dt*speed)
	}
	a.setIconAlpha(iconFade)
	return math.Abs(iconFade-t) >= 0.001
}

// restoreIcons makes the desktop icons fully visible again (on exit, or when
// the option is turned off).
func (a *App) restoreIcons() {
	iconFade = 1
	if iconHwnd != 0 && isWindow(iconHwnd) {
		iconFailed = false
		a.setIconAlpha(1)
	}
}
