//go:build windows

package main

import (
	"fmt"
	"image"
	"log"
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
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	bi := BITMAPINFOHEADER{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp := call(pCreateDIBSection, 0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		return 0
	}
	dst := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		o := dst[y*w*4 : (y+1)*w*4]
		for x := 0; x < w*4; x += 4 {
			o[x], o[x+1], o[x+2], o[x+3] = row[x+2], row[x+1], row[x], 255
		}
	}
	return bmp
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
				a.status = "Could not create plot panel on the desktop."
				continue
			}
			pn = &panel{hwnd: h}
			a.panels[v.ID] = pn
		}
		o := a.renderOpts(p, v, false)
		key := fmt.Sprintf("%s|%v|%s", a.lookKey(o), v.Rect, mode)
		if pn.rect != v.Rect {
			old := pn.rect
			pos := screenToClient(a.desk.Host, Point{v.Rect.L, v.Rect.T})
			call(pSetWindowPos, pn.hwnd, a.desk.InsertAfter, uintptr(pos.X), uintptr(pos.Y), uintptr(v.Rect.W()), uintptr(v.Rect.H()), SWP_NOACTIVATE)
			pn.rect = v.Rect
			a.repaintDesktop(old)
			log.Printf("panel %q screen=%v client=%v actual=%v", p.Title, v.Rect, pos, windowRect(pn.hwnd))
		}
		if key != pn.key {
			plot := RenderPlot(o)
			if mode == "ulw" {
				if !pushLayered(pn.hwnd, plot, nil) {
					log.Printf("UpdateLayeredWindow failed for panel %q: %v", p.Title, windows.GetLastError())
				}
			} else {
				bg, origin := wallpaperUnder(a.mons, v.Rect)
				img := ComposePanel(bg, origin, v.Rect, plot)
				if pn.hbm != 0 {
					call(pDeleteObject, pn.hbm)
				}
				pn.hbm, pn.w, pn.h = makeBitmap(img), img.Bounds().Dx(), img.Bounds().Dy()
				call(pInvalidateRect, pn.hwnd, 0, 0)
			}
			pn.key = key
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
}
