//go:build windows

package main

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	CLSID_DesktopWallpaper = windows.GUID{0xC2CF3110, 0x460E, 0x4FC1, [8]byte{0xB9, 0xD0, 0x8A, 0x1C, 0x0C, 0x9C, 0xC4, 0xBD}}
	IID_IDesktopWallpaper  = windows.GUID{0xB92B56A9, 0x8B55, 0x4E14, [8]byte{0x9A, 0x89, 0x01, 0x99, 0xBB, 0xB6, 0xF9, 0x3B}}
)

type wallMonitor struct {
	rect Rect
	path string
}

type wallCache struct {
	sig    string                 // changes when anything about the wallpaper changes
	images map[string]*image.RGBA // monitor rect string -> rendered wallpaper
}

var wc = &wallCache{images: map[string]*image.RGBA{}}

func coString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	call(pCoTaskMemFree, uintptr(unsafe.Pointer(p)))
	return s
}

// queryWallpaper asks the shell what is shown on every monitor.
func queryWallpaper() (mons []wallMonitor, pos int, bg color.RGBA, err error) {
	var dw comObj
	hr := call(pCoCreateInstance, uintptr(unsafe.Pointer(&CLSID_DesktopWallpaper)), 0, CLSCTX_ALL,
		uintptr(unsafe.Pointer(&IID_IDesktopWallpaper)), uintptr(unsafe.Pointer(&dw)))
	if int32(hr) < 0 {
		return nil, 0, bg, hresult("DesktopWallpaper", hr)
	}
	defer dw.Release()
	var n uint32
	dw.vcall(6, uintptr(unsafe.Pointer(&n)))
	for i := uint32(0); i < n; i++ {
		var idp *uint16
		if hr := dw.vcall(5, uintptr(i), uintptr(unsafe.Pointer(&idp))); int32(hr) < 0 {
			continue
		}
		id := coString(idp)
		var r RECT
		if hr := dw.vcall(7, uintptr(unsafe.Pointer(u16(id))), uintptr(unsafe.Pointer(&r))); int32(hr) < 0 {
			continue // monitor not attached
		}
		var wp *uint16
		dw.vcall(4, uintptr(unsafe.Pointer(u16(id))), uintptr(unsafe.Pointer(&wp)))
		mons = append(mons, wallMonitor{rect: Rect{int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)}, path: coString(wp)})
	}
	var p int32
	dw.vcall(11, uintptr(unsafe.Pointer(&p)))
	var c uint32
	dw.vcall(9, uintptr(unsafe.Pointer(&c)))
	return mons, int(p), color.RGBA{uint8(c), uint8(c >> 8), uint8(c >> 16), 255}, nil
}

func fileSig(p string) string {
	if st, err := os.Stat(p); err == nil {
		return fmt.Sprintf("%s:%d:%d", p, st.Size(), st.ModTime().UnixNano())
	}
	return p
}

// refreshWallpaper re-reads the wallpaper state and reports whether it changed.
func refreshWallpaper(ms []Monitor) bool {
	wm, pos, bg, err := queryWallpaper()
	if err != nil {
		log.Println("wallpaper:", err)
	}
	transcoded := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Themes\TranscodedWallpaper`)
	sig := fmt.Sprintf("%d|%v|%s", pos, bg, fileSig(transcoded))
	for _, m := range wm {
		sig += "|" + fmt.Sprint(m.rect) + "=" + fileSig(m.path)
	}
	for _, m := range ms {
		sig += "|" + fmt.Sprint(m.Rect)
	}
	if sig == wc.sig {
		return false
	}
	wc.sig = sig
	wc.images = map[string]*image.RGBA{}

	virt := Rect{}
	for i, m := range ms {
		if i == 0 {
			virt = m.Rect
		}
		virt = Rect{min(virt.L, m.Rect.L), min(virt.T, m.Rect.T), max(virt.R, m.Rect.R), max(virt.B, m.Rect.B)}
	}
	decoded := map[string]image.Image{}
	get := func(p string) image.Image {
		if p == "" {
			p = transcoded
		}
		if img, ok := decoded[p]; ok {
			return img
		}
		img, err := loadImage(p)
		if err != nil && p != transcoded {
			img, err = loadImage(transcoded)
		}
		if err != nil {
			log.Println("wallpaper load:", p, err)
		}
		decoded[p] = img
		return img
	}
	for _, m := range ms {
		path := ""
		for _, w := range wm {
			if w.rect == m.Rect {
				path = w.path
			}
		}
		spec := WallSpec{Path: path, Position: pos, BG: bg, Area: m.Rect}
		if pos == WPSpan {
			spec.Area = virt
		}
		wc.images[fmt.Sprint(m.Rect)] = RenderWallpaper(spec, get(path), m.Rect)
		log.Printf("wallpaper for %s %v: %q pos=%d", m.Name, m.Rect, path, pos)
	}
	return true
}

// wallpaperUnder returns the rendered wallpaper of the monitor containing r.
func wallpaperUnder(ms []Monitor, r Rect) (*image.RGBA, Point) {
	m := monitorAt(ms, r.Center())
	return wc.images[fmt.Sprint(m.Rect)], Point{m.Rect.L, m.Rect.T}
}
