//go:build windows

package main

import (
	"fmt"
	"image"
	"log"
	"math"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Panels live on Explorer's desktop layer, behind the icons.
//
// Modes:
//   slwa  - layered child with constant alpha 255, painted in WM_PAINT (Win11 24H2 default)
//   plain - ordinary child window painted in WM_PAINT (classic WorkerW default)
//   ulw   - per-pixel UpdateLayeredWindow (works on some systems, invisible on others)
// In slwa/plain the panel is opaque; translucency is produced by blending the
// plot over the wallpaper image the shell is showing on that monitor.

const (
	LWA_ALPHA       = 0x2
	RDW_INVALIDATE  = 0x1
	RDW_ERASE       = 0x4
	RDW_ALLCHILDREN = 0x80
	RDW_UPDATENOW   = 0x100
	RDW_FRAME       = 0x400
	SRCCOPY         = 0x00CC0020
)

func (a *App) panelMode() string {
	switch a.cfg.PanelMode {
	case "slwa", "plain", "ulw":
		return a.cfg.PanelMode
	}
	if a.desk != nil && a.desk.Host == a.desk.Progman {
		return "slwa"
	}
	return "plain"
}

func (a *App) panelByHwnd(h HWND) *panel {
	for _, p := range a.panels {
		if p.hwnd == h {
			return p
		}
	}
	return nil
}

func (a *App) panelWndProc(h HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_NCHITTEST:
		return ^uintptr(0) // HTTRANSPARENT: clicks go to the icons/desktop
	case WM_ERASEBKGND:
		return 1
	case WM_PAINT:
		var ps PAINTSTRUCT
		hdc := call(pBeginPaint, h, uintptr(unsafe.Pointer(&ps)))
		if p := a.panelByHwnd(h); p != nil && p.hbm != 0 {
			mem := call(pCreateCompatibleDC, hdc)
			old := call(pSelectObject, mem, p.hbm)
			call(pBitBlt, hdc, 0, 0, uintptr(p.w), uintptr(p.h), mem, 0, 0, SRCCOPY)
			call(pSelectObject, mem, old)
			call(pDeleteDC, mem)
		}
		call(pEndPaint, h, uintptr(unsafe.Pointer(&ps)))
		return 0
	}
	return call(pDefWindowProcW, h, uintptr(msg), wp, lp)
}

// makeBitmap converts a premultiplied RGBA image into a 32-bit DIB section.
func makeBitmap(img *image.RGBA) uintptr {
	bmp, bits := newDIB(img.Bounds().Dx(), img.Bounds().Dy())
	if bmp != 0 {
		copyToDIB(bits, img)
	}
	return bmp
}

func newDIB(w, h int) (uintptr, unsafe.Pointer) {
	bi := BITMAPINFOHEADER{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp := call(pCreateDIBSection, 0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	return bmp, bits
}

// copyToDIB writes img (RGBA) into a DIB's pixels (BGRA, opaque).
func copyToDIB(bits unsafe.Pointer, img *image.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	dst := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		o := dst[y*w*4 : (y+1)*w*4]
		for x := 0; x < w*4; x += 4 {
			o[x], o[x+1], o[x+2], o[x+3] = row[x+2], row[x+1], row[x], 255
		}
	}
}

func (a *App) repaintDesktop(r Rect) {
	if a.desk == nil || r.W() <= 0 {
		return
	}
	tl := screenToClient(a.desk.Host, Point{r.L, r.T})
	rc := RECT{int32(tl.X), int32(tl.Y), int32(tl.X + r.W()), int32(tl.Y + r.H())}
	call(pRedrawWindow, a.desk.Host, uintptr(unsafe.Pointer(&rc)), 0, RDW_INVALIDATE|RDW_ERASE|RDW_ALLCHILDREN|RDW_FRAME|RDW_UPDATENOW)
}

func (a *App) destroyPanel(id string) {
	p := a.panels[id]
	if p == nil {
		return
	}
	call(pDestroyWindow, p.hwnd)
	if p.hbm != 0 {
		call(pDeleteObject, p.hbm)
	}
	a.repaintDesktop(p.rect)
	delete(a.panels, id)
}

func (a *App) createPanel(title string) HWND {
	mode := a.panelMode()
	ex := uintptr(WS_EX_NOACTIVATE | WS_EX_TRANSPARENT | WS_EX_NOPARENTNOTIFY | WS_EX_TOOLWINDOW)
	if mode != "plain" {
		ex |= WS_EX_LAYERED
	}
	h := createWindow(ex, "PlotsPanel", title, WS_CHILD|WS_CLIPSIBLINGS, 0, 0, 1, 1, a.desk.Host, 0)
	if h == 0 {
		// fallback: create as popup, switch to child style, then re-parent
		h = createWindow(ex, "PlotsPanel", title, WS_POPUP, 0, 0, 1, 1, 0, 0)
		if h != 0 {
			call(pSetWindowLongPtrW, h, ^uintptr(15) /*GWL_STYLE -16*/, WS_CHILD|WS_CLIPSIBLINGS)
			call(pSetParent, h, a.desk.Host)
		}
	}
	if h != 0 && mode == "slwa" {
		call(pSetLayeredWindowAttributes, h, 0, 255, LWA_ALPHA)
	}
	log.Printf("panel %q created hwnd=%x parent=%x mode=%s", title, h, call(pGetParent, h), mode)
	return h
}

func (a *App) updatePanels() {
	if a.desk == nil {
		return
	}
	mode := a.panelMode()
	a.fixPanelOrder()
	if a.ticks%10 == 1 || wc.sig == "" {
		if refreshWallpaper(a.mons) {
			for _, p := range a.panels {
				p.key = "" // re-compose over the new wallpaper
			}
		}
	}
	seen := map[string]bool{}
	for _, v := range a.eng.Views() {
		p := a.cfg.Plot(v.ID)
		if p == nil {
			continue
		}
		seen[v.ID] = true
		pn := a.panels[v.ID]
		if pn == nil || !isWindow(pn.hwnd) {
			h := a.createPanel(p.Title)
			if h == 0 {
				a.status = T("Could not create plot panel on the desktop.")
				continue
			}
			pn = &panel{hwnd: h}
			a.panels[v.ID] = pn
		}
		o := a.renderOpts(p, v, false)
		key := fmt.Sprintf("%s|%v|%s|%s", a.lookKey(o), v.Rect, mode, a.folderKey(p))
		if pn.rect != v.Rect {
			old := pn.rect
			pos := screenToClient(a.desk.Host, Point{v.Rect.L, v.Rect.T})
			call(pSetWindowPos, pn.hwnd, a.desk.InsertAfter, uintptr(pos.X), uintptr(pos.Y), uintptr(v.Rect.W()), uintptr(v.Rect.H()), SWP_NOACTIVATE)
			pn.rect = v.Rect
			a.repaintDesktop(old)
			log.Printf("panel %q screen=%v client=%v actual=%v", p.Title, v.Rect, pos, windowRect(pn.hwnd))
		}
		if key != pn.key {
			if mode != "ulw" {
				// automatic title contrast looks at the wallpaper behind the plot
				if bg, origin := wallpaperUnder(a.mons, v.Rect); bg != nil {
					o.Backdrop, o.BackdropAt = bg, image.Pt(v.Rect.L-origin.X, v.Rect.T-origin.Y)
				}
			}
			plot := RenderPlot(o)
			if p.Folder != "" {
				a.drawFolder(plot, p, v)
			}
			pn.plot = plot
			if pn.fade == 0 {
				pn.fade = a.fadeTarget(p.ID)
			}
			if mode == "ulw" {
				if !pushLayered(pn.hwnd, plot, nil) {
					log.Printf("UpdateLayeredWindow failed for panel %q: %v", p.Title, windows.GetLastError())
				}
			} else {
				a.composePanel(pn, v.Rect)
			}
			pn.key = key
		}
		if mode != "ulw" && math.Abs(pn.fade-a.fadeTarget(p.ID)) > 0.001 {
			a.startFade()
		}
		// in edit mode the overlays stand in for the panels
		want := !a.editing && !a.cfg.Hidden
		if (call(pIsWindowVisible, pn.hwnd) != 0) != want {
			if want {
				call(pSetWindowPos, pn.hwnd, a.desk.InsertAfter, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW)
			} else {
				call(pShowWindow, pn.hwnd, SW_HIDE)
				a.repaintDesktop(pn.rect)
			}
			log.Printf("panel %q visible=%v (want %v)", p.Title, call(pIsWindowVisible, pn.hwnd) != 0, want)
			if want {
				dumpTree("Progman children after show", a.desk.Progman)
			}
		}
	}
	for id := range a.panels {
		if !seen[id] {
			a.destroyPanel(id)
		}
	}
	if a.ticks%10 == 1 && a.drag == nil {
		var used []Rect
		for _, v := range a.eng.Views() {
			used = append(used, v.Rect)
		}
		pruneWallpapers(a.mons, used)
	}
	if math.Abs(iconFade-a.iconFadeTarget()) > 0.001 {
		a.startFade()
	}
}

// fixPanelOrder makes sure every panel sits between the icons and the
// wallpaper. Explorer re-orders its own windows when the wallpaper changes
// (slideshows, theme changes), which would bury the panels under the wallpaper.
func (a *App) fixPanelOrder() {
	if len(a.panels) == 0 || a.desk.InsertAfter == 0 {
		return
	}
	ours := map[HWND]bool{}
	for _, p := range a.panels {
		ours[p.hwnd] = true
	}
	// walk the z-order below DefView: all our panels must come before anything else
	ok := true
	seen := 0
	for h := call(pGetWindow, a.desk.InsertAfter, 2 /*GW_HWNDNEXT*/); h != 0 && seen < len(ours); h = call(pGetWindow, h, 2) {
		if !ours[h] {
			if call(pIsWindowVisible, h) != 0 {
				ok = false
				break
			}
			continue
		}
		seen++
	}
	if ok && seen == len(ours) {
		return
	}
	log.Printf("panels were buried (z-order changed by Explorer); restoring")
	for _, p := range a.panels {
		call(pSetWindowPos, p.hwnd, a.desk.InsertAfter, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
		call(pInvalidateRect, p.hwnd, 0, 0)
	}
}

// ------------------------------------------------------------------ fade until hovered

// fadeTarget is how strongly a plot should show right now (1 = fully).
func (a *App) fadeTarget(id string) float64 {
	if !a.cfg.FadeIdle || a.editing || a.hoverID == id || a.eng.Peek == id || (a.drag != nil && a.drag.id == id) {
		return 1
	}
	return float64(a.cfg.FadeLevel) / 100
}

// composePanel blends the panel's last render, faded, over the wallpaper.
// The fade and blend buffers and the bitmap are reused while the panel keeps
// its size, so fading in and out (60 frames a second) allocates nothing.
func (a *App) composePanel(pn *panel, r Rect) {
	if pn.plot == nil {
		return
	}
	plot := pn.plot
	if pn.fade < 0.999 {
		pn.fadeBuf = FadeInto(pn.fadeBuf, pn.plot, pn.fade)
		plot = pn.fadeBuf
	}
	bg, origin := wallpaperUnder(a.mons, r)
	pn.compBuf = ComposePanelInto(pn.compBuf, bg, origin, r, plot)
	img := pn.compBuf
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if pn.hbm == 0 || pn.w != w || pn.h != h {
		if pn.hbm != 0 {
			call(pDeleteObject, pn.hbm)
		}
		pn.hbm, pn.bits = newDIB(w, h)
		pn.w, pn.h = w, h
	}
	if pn.hbm != 0 {
		call(pGdiFlush)
		copyToDIB(pn.bits, img)
	}
	call(pInvalidateRect, pn.hwnd, 0, 0)
}

var fadeLast time.Time

func (a *App) startFade() {
	fadeLast = time.Now()
	call(pSetTimer, a.main, timerFade, 16, 0)
}

// fadeStep moves every panel's strength toward its target (about 180 ms in, 400 ms out).
func (a *App) fadeStep() {
	dt := time.Since(fadeLast).Seconds()
	fadeLast = time.Now()
	busy := false
	for id, pn := range a.panels {
		t := a.fadeTarget(id)
		if math.Abs(pn.fade-t) < 0.001 {
			continue
		}
		speed := 1 / 0.18
		if t < pn.fade {
			speed = 1 / 0.4
		}
		step := dt * speed
		if pn.fade < t {
			pn.fade = math.Min(t, pn.fade+step)
		} else {
			pn.fade = math.Max(t, pn.fade-step)
		}
		if math.Abs(pn.fade-t) >= 0.001 {
			busy = true
		}
		a.composePanel(pn, pn.rect)
	}
	if a.iconFadeStep(dt) {
		busy = true
	}
	if !busy {
		call(pKillTimer, a.main, timerFade)
	}
}
