//go:build windows

package main

import (
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Settings snapshots, for documentation and support: create a file named
// "snap.request" next to config.json and Plots saves a PNG of every settings
// page into a "snapshots" folder there, then deletes the request.

var (
	pPrintWindow           = user32.NewProc("PrintWindow")
	pDwmGetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
	nextSnapScan           time.Time
)

func (a *App) checkSnapshotRequest() {
	if time.Now().Before(nextSnapScan) {
		return
	}
	nextSnapScan = time.Now().Add(2 * time.Second)
	dir := filepath.Dir(a.cfgPath)
	req := filepath.Join(dir, "snap.request")
	b, err := os.ReadFile(req)
	if err != nil {
		return
	}
	os.Remove(req)
	var pages []string
	lang := ""
	for _, f := range strings.Fields(string(b)) {
		if strings.HasPrefix(f, "lang=") {
			lang = strings.TrimPrefix(f, "lang=")
		} else {
			pages = append(pages, f)
		}
	}
	if len(pages) == 0 {
		pages = []string{"plot", "look", "rules", "general", "backup", "about"}
	}
	if lang != "" {
		// capture in another language, then put everything back
		SetLanguage(lang)
		defer func() {
			if a.settings != nil {
				call(pDestroyWindow, a.settings.hwnd)
			}
			a.applyLanguage()
		}()
		if a.settings != nil {
			call(pDestroyWindow, a.settings.hwnd)
		}
	}
	a.showSettings("")
	s := a.settings
	if s == nil {
		return
	}
	out := filepath.Join(dir, "snapshots")
	if lang != "" {
		out = filepath.Join(out, lang)
	}
	os.MkdirAll(out, 0o755)
	for _, pg := range pages {
		if pg == "plot" && len(a.cfg.Plots) > 0 {
			s.selectPlot(a.cfg.Plots[0].ID)
		} else {
			s.showPage(pg)
		}
		snapWindow(s.hwnd, filepath.Join(out, pg+".png"))
		if p := s.pages[s.page]; p != nil && p.height > s.paneHeight() {
			s.scrollTo(1<<30, false)
			snapWindow(s.hwnd, filepath.Join(out, pg+"-end.png"))
			s.scrollTo(0, false)
		}
	}
	log.Println("settings snapshots saved to", out)
}

var pPeekMessageW = user32.NewProc("PeekMessageW")

// pumpMessages lets pending paint and owner-draw messages run before a capture.
func pumpMessages(d time.Duration) {
	end := time.Now().Add(d)
	var m MSG
	for time.Now().Before(end) {
		if r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1 /*PM_REMOVE*/); r == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if m.Message == WM_TIMER && m.Hwnd == app.main {
			continue // no desktop ticks (and no nested snapshot) while capturing
		}
		call(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
}

func snapWindow(h HWND, path string) {
	pumpMessages(150 * time.Millisecond)
	call(pRedrawWindow, h, 0, 0, RDW_INVALIDATE|RDW_ERASE|RDW_ALLCHILDREN|0x100 /*UPDATENOW*/)
	r := windowRect(h)
	w, ht := r.W(), r.H()
	bi := BITMAPINFOHEADER{Width: int32(w), Height: -int32(ht), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp := call(pCreateDIBSection, 0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		return
	}
	defer call(pDeleteObject, bmp)
	mem := call(pCreateCompatibleDC, 0)
	old := call(pSelectObject, mem, bmp)
	if os.Getenv("PLOTS_SNAP_PRINTWINDOW") != "" {
		call(pPrintWindow, h, mem, 2 /*PW_RENDERFULLCONTENT*/)
	} else {
		// copy what is actually on screen (the window is brought to the front)
		call(pSetForegroundWindow, h)
		call(pSetWindowPos, h, ^uintptr(0) /*TOPMOST*/, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE)
		pumpMessages(250 * time.Millisecond)
		screen := call(pGetDC, 0)
		call(pBitBlt, mem, 0, 0, uintptr(w), uintptr(ht), screen, uintptr(r.L), uintptr(r.T), SRCCOPY|0x40000000 /*CAPTUREBLT*/)
		call(pReleaseDC, 0, screen)
		call(pSetWindowPos, h, ^uintptr(1) /*NOTOPMOST*/, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE)
	}
	call(pSelectObject, mem, old)
	call(pDeleteDC, mem)
	src := unsafe.Slice((*byte)(bits), w*ht*4)
	full := image.NewRGBA(image.Rect(0, 0, w, ht))
	for i := 0; i < w*ht*4; i += 4 {
		full.Pix[i], full.Pix[i+1], full.Pix[i+2], full.Pix[i+3] = src[i+2], src[i+1], src[i], 255
	}
	// crop the invisible resize borders (they capture as black)
	var vis RECT
	var img image.Image = full
	if hr, _, _ := pDwmGetWindowAttribute.Call(h, 9 /*DWMWA_EXTENDED_FRAME_BOUNDS*/, uintptr(unsafe.Pointer(&vis)), unsafe.Sizeof(vis)); hr == 0 {
		c := image.Rect(int(vis.Left)-r.L, int(vis.Top)-r.T, int(vis.Right)-r.L, int(vis.Bottom)-r.T).Intersect(full.Bounds())
		if !c.Empty() {
			img = full.SubImage(c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	png.Encode(f, img)
	f.Close()
}
