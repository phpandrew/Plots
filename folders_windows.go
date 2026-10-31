//go:build windows

package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Live-folder plots show a folder's contents (drawn by Plots itself, not
// desktop icons). Thumbnails come from the shell on a background thread.

type folderEntry struct {
	Path  string
	Name  string
	IsDir bool
	Mod   time.Time
	Size  int64
}

type folderState struct {
	entries  []folderEntry
	sig      string
	readAt   time.Time
	err      string
	sel      string // selected path
	lastPath string
	lastTime time.Time
}

const (
	msgThumbsReady = WM_APP + 31
	msgFolderClick = WM_APP + 32
	msgFolderMenu  = WM_APP + 33
)

// ------------------------------------------------------------------ entries

func (a *App) folderState(p *Plot) *folderState {
	if a.folders == nil {
		a.folders = map[string]*folderState{}
	}
	fs := a.folders[p.ID]
	if fs == nil {
		fs = &folderState{}
		a.folders[p.ID] = fs
	}
	return fs
}

// folderEntries returns the folder listing, re-reading it at most every 700ms.
func (a *App) folderEntries(p *Plot) []folderEntry {
	fs := a.folderState(p)
	if time.Since(fs.readAt) < 700*time.Millisecond {
		return fs.entries
	}
	fs.readAt = time.Now()
	des, err := os.ReadDir(p.Folder)
	if err != nil {
		fs.entries, fs.err, fs.sig = nil, err.Error(), "err:"+err.Error()
		return nil
	}
	fs.err = ""
	out := make([]folderEntry, 0, len(des))
	for _, d := range des {
		info, err := d.Info()
		if err != nil {
			continue
		}
		if fa, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && fa.FileAttributes&(windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_SYSTEM) != 0 {
			continue
		}
		if isTempName(d.Name()) || strings.EqualFold(d.Name(), "desktop.ini") {
			continue
		}
		out = append(out, folderEntry{Path: filepath.Join(p.Folder, d.Name()), Name: displayName(d.Name(), d.IsDir()),
			IsDir: d.IsDir(), Mod: info.ModTime(), Size: info.Size()})
	}
	sortEntries(out, p.FolderSort)
	var b strings.Builder
	for _, e := range out {
		fmt.Fprintf(&b, "%s|%d|%d;", e.Name, e.Mod.UnixNano(), e.Size)
	}
	fs.entries, fs.sig = out, fmt.Sprintf("%s#%x", p.FolderSort, hashString(b.String()))
	return out
}

// displayName hides extensions of shortcuts like Explorer does.
func displayName(name string, dir bool) string {
	low := strings.ToLower(name)
	if !dir && (strings.HasSuffix(low, ".lnk") || strings.HasSuffix(low, ".url")) {
		return name[:len(name)-4]
	}
	return name
}

func hashString(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func sortEntries(es []folderEntry, by string) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		switch by {
		case "date":
			return a.Mod.After(b.Mod)
		case "size":
			if a.IsDir != b.IsDir {
				return a.IsDir
			}
			return a.Size > b.Size
		case "type":
			if a.IsDir != b.IsDir {
				return a.IsDir
			}
			ea, eb := strings.ToLower(filepath.Ext(a.Name)), strings.ToLower(filepath.Ext(b.Name))
			if ea != eb {
				return ea < eb
			}
		default:
			if a.IsDir != b.IsDir {
				return a.IsDir
			}
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func (a *App) FolderCount(p *Plot) int { return len(a.folderEntries(p)) }

// folderKey changes whenever the folder plot needs repainting.
func (a *App) folderKey(p *Plot) string {
	if p.Folder == "" {
		return ""
	}
	fs := a.folderState(p)
	thumbMu.Lock()
	gen := thumbGen
	thumbMu.Unlock()
	return fmt.Sprintf("%s|%s|%s|%d", p.Folder, fs.sig, fs.sel, gen)
}

// ------------------------------------------------------------------ thumbnails (background STA thread)

var (
	IID_IShellItemImageFactory   = windows.GUID{0xBCC18B79, 0xBA16, 0x442F, [8]byte{0x80, 0xC4, 0x8A, 0x59, 0xC3, 0x0C, 0x46, 0x3B}}
	pSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	pGetDIBits                   = gdi32.NewProc("GetDIBits")
	pGetObjectW                  = gdi32.NewProc("GetObjectW")

	thumbMu      sync.Mutex
	thumbCache   = map[string]*image.RGBA{}
	thumbBytes   int // pixels held by thumbCache
	thumbPending = map[string]bool{}
	thumbQueue   = make(chan thumbReq, 4096)
	thumbGen     int
	thumbOnce    sync.Once
)

type thumbReq struct {
	key, path string
	size      int
}

func thumbKey(e folderEntry, size int) string {
	return fmt.Sprintf("%s|%d|%d|%d", e.Path, e.Mod.UnixNano(), e.Size, size)
}

// thumb returns a cached thumbnail, or nil and queues it.
func (a *App) thumb(e folderEntry, size int) *image.RGBA {
	k := thumbKey(e, size)
	thumbMu.Lock()
	img, ok := thumbCache[k]
	pending := thumbPending[k]
	if !ok && !pending {
		thumbPending[k] = true
	}
	thumbMu.Unlock()
	if ok {
		return img
	}
	if !pending {
		thumbOnce.Do(func() { go thumbWorker(a.main) })
		select {
		case thumbQueue <- thumbReq{k, e.Path, size}:
		default:
		}
	}
	return nil
}

func thumbWorker(main HWND) {
	runtime.LockOSThread()
	call(pCoInitializeEx, 0, COINIT_APARTMENTTHREADED)
	batch := 0
	for {
		var req thumbReq
		select {
		case req = <-thumbQueue:
		case <-time.After(150 * time.Millisecond):
			if batch > 0 {
				batch = 0
				postMessage(main, msgThumbsReady, 0, 0)
			}
			continue
		}
		img := shellThumb(req.path, req.size)
		thumbMu.Lock()
		if thumbBytes > thumbBudget {
			// start over; what's on screen is fetched again in the background
			thumbCache, thumbBytes = map[string]*image.RGBA{}, 0
		}
		thumbCache[req.key] = img
		if img != nil {
			thumbBytes += len(img.Pix)
		}
		delete(thumbPending, req.key)
		thumbGen++
		thumbMu.Unlock()
		batch++
		if batch >= 24 {
			batch = 0
			postMessage(main, msgThumbsReady, 0, 0)
		}
	}
}

type bitmapInfo struct {
	Type, Width, Height, WidthBytes int32
	Planes, BitsPixel               uint16
	Bits                            uintptr
}

// shellThumb asks the shell for an image (thumbnail if available, else icon).
func shellThumb(path string, size int) *image.RGBA {
	var f comObj
	hr := call(pSHCreateItemFromParsingName, uintptr(unsafe.Pointer(u16(path))), 0,
		uintptr(unsafe.Pointer(&IID_IShellItemImageFactory)), uintptr(unsafe.Pointer(&f)))
	if int32(hr) < 0 || f == 0 {
		return nil
	}
	defer f.Release()
	sz := uintptr(uint32(size)) | uintptr(uint32(size))<<32 // SIZE passed by value
	var hbm uintptr
	if hr := f.vcall(3, sz, 0, uintptr(unsafe.Pointer(&hbm))); int32(hr) < 0 || hbm == 0 {
		return nil
	}
	defer call(pDeleteObject, hbm)
	var bm bitmapInfo
	call(pGetObjectW, hbm, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	w, h := int(bm.Width), int(bm.Height)
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 {
		return nil
	}
	bi := BITMAPINFOHEADER{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	buf := make([]byte, w*h*4)
	dc := call(pGetDC, 0)
	call(pGetDIBits, dc, hbm, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	call(pReleaseDC, 0, dc)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	anyAlpha, straight := false, false
	for i := 0; i < len(buf); i += 4 {
		b, g, r, al := buf[i], buf[i+1], buf[i+2], buf[i+3]
		if al != 0 {
			anyAlpha = true
		}
		if r > al || g > al || b > al {
			straight = true
		}
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, al
	}
	for i := 0; i < len(img.Pix); i += 4 {
		switch {
		case !anyAlpha: // no alpha channel at all: opaque bitmap
			img.Pix[i+3] = 255
		case straight: // not premultiplied: premultiply for image.RGBA
			al := uint32(img.Pix[i+3])
			img.Pix[i] = uint8(uint32(img.Pix[i]) * al / 255)
			img.Pix[i+1] = uint8(uint32(img.Pix[i+1]) * al / 255)
			img.Pix[i+2] = uint8(uint32(img.Pix[i+2]) * al / 255)
		}
	}
	return img
}

// ------------------------------------------------------------------ drawing

func (a *App) iconPx(scale float64) int {
	if a.eng.IconW > 0 {
		return a.eng.IconW
	}
	return int(math.Round(48 * scale))
}

// drawFolder paints the visible entries of a live-folder plot into img.
func (a *App) drawFolder(img *image.RGBA, p *Plot, v *PlotView) {
	fs := a.folderState(p)
	entries := a.folderEntries(p)
	sx, sy := v.Cell.X, v.Cell.Y
	if v.Collapsed {
		return
	}
	if fs.err != "" || len(entries) == 0 {
		msg := T("This folder is empty.")
		if fs.err != "" {
			msg = Tf("Can't open folder: %s", p.Folder)
		}
		DrawFolderCells(img, []FolderCell{{Name: msg, X: 0, Y: v.Grid.Y - v.Rect.T, W: v.Rect.W(), H: sy}}, 0, v.Scale)
		return
	}
	// file names in the colour that reads best on this plot and wallpaper
	var ink color.NRGBA
	{
		bg, origin := wallpaperUnder(a.mons, v.Rect)
		body := image.Rect(0, v.Grid.Y-v.Rect.T, v.Rect.W(), v.Rect.H())
		ink, _ = Ink(Lum(img, bg, image.Pt(v.Rect.L-origin.X, v.Rect.T-origin.Y), body))
	}
	px := a.iconPx(v.Scale)
	if p.IconSize > 0 {
		px = int(math.Round(float64(p.IconSize) * v.Scale))
	}
	first := v.Scroll * v.Cols
	visible := len(entries) - first
	if v.Rows > 0 && !a.cfg.GrowToFit {
		visible = min(visible, v.Rows*v.Cols)
	}
	cells := make([]FolderCell, 0, max(0, visible))
	for i := 0; i < visible; i++ {
		e := entries[first+i]
		col, row := i%v.Cols, i/v.Cols
		cells = append(cells, FolderCell{
			Icon: a.tinted(p, e, px), Name: e.Name,
			X: v.Grid.X - v.Rect.L + col*sx, Y: v.Grid.Y - v.Rect.T + row*sy, W: sx, H: sy,
			Selected: e.Path == fs.sel, Ink: ink,
		})
	}
	DrawFolderCells(img, cells, px, v.Scale)
}

// folderHit returns the entry under a screen point, or nil.
func (a *App) folderHit(p *Plot, pt Point) *folderEntry {
	v := a.eng.View(p.ID)
	if v == nil || v.Collapsed {
		return nil
	}
	sx, sy := max(1, v.Cell.X), max(1, v.Cell.Y)
	if pt.X < v.Grid.X || pt.Y < v.Grid.Y {
		return nil
	}
	col, row := (pt.X-v.Grid.X)/sx, (pt.Y-v.Grid.Y)/sy
	if col >= v.Cols || (v.Rows > 0 && !a.cfg.GrowToFit && row >= v.Rows) {
		return nil
	}
	idx := (row+v.Scroll)*v.Cols + col
	entries := a.folderEntries(p)
	if idx < 0 || idx >= len(entries) {
		return nil
	}
	return &entries[idx]
}

func unpackPt(lp uintptr) Point {
	return Point{int(int16(uint16(lp))), int(int16(uint16(lp >> 16)))}
}

func packPt(p Point) uintptr {
	return uintptr(uint16(int16(p.X))) | uintptr(uint16(int16(p.Y)))<<16
}

func (a *App) onFolderClick(idx uintptr, lp uintptr) {
	p := a.plotAtIndex(idx)
	if p == nil || p.Folder == "" {
		return
	}
	fs := a.folderState(p)
	e := a.folderHit(p, unpackPt(lp))
	if e == nil {
		fs.sel = ""
		a.updatePanels()
		return
	}
	dbl := time.Duration(call(pGetDoubleClickTime)) * time.Millisecond
	if fs.lastPath == e.Path && time.Since(fs.lastTime) <= dbl {
		fs.lastTime = time.Time{}
		openURL(e.Path) // ShellExecute "open": runs files, opens folders in Explorer
	} else {
		fs.lastPath, fs.lastTime = e.Path, time.Now()
	}
	fs.sel = e.Path
	a.updatePanels()
}

func (a *App) onFolderMenu(idx uintptr, lp uintptr) {
	p := a.plotAtIndex(idx)
	if p == nil || p.Folder == "" {
		return
	}
	pt := unpackPt(lp)
	e := a.folderHit(p, pt)
	if e == nil {
		a.plotMenu(p.ID, pt)
		return
	}
	a.folderState(p).sel = e.Path
	a.updatePanels()
	showShellMenu(a.main, e.Path, pt)
	a.folderState(p).readAt = time.Time{} // the command may have changed the folder
	a.tick()
}

// ------------------------------------------------------------------ moving files into a folder plot

func (a *App) moveIntoFolder(path string, p *Plot) {
	dst := uniquePath(p.Folder, filepath.Base(path))
	err := os.Rename(path, dst)
	if err != nil {
		// different drive: copy then delete (files only)
		if st, e2 := os.Stat(path); e2 == nil && !st.IsDir() {
			if err = copyFile(path, dst); err == nil {
				err = os.Remove(path)
			}
		}
	}
	if err != nil {
		log.Printf("could not move %q into %q: %v", path, p.Folder, err)
		return
	}
	log.Printf("moved %q into live folder %q", path, p.Folder)
	a.folderState(p).readAt = time.Time{}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// ------------------------------------------------------------------ folder picker

type browseInfo struct {
	Owner       HWND
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Fn          uintptr
	Param       uintptr
	Image       int32
}

var (
	pSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
)

func pickFolder(owner HWND, title string) string {
	var name [260]uint16
	bi := browseInfo{Owner: owner, DisplayName: &name[0], Title: u16(title), Flags: 0x1 | 0x10 | 0x40}
	pidl := call(pSHBrowseForFolderW, uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer call(pCoTaskMemFree, pidl)
	var buf [1024]uint16
	if call(pSHGetPathFromIDListW, pidl, uintptr(unsafe.Pointer(&buf[0]))) == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

// newFolderPlot asks for a folder and creates a live-folder plot for it.
func (a *App) newFolderPlot(owner HWND) {
	dir := pickFolder(owner, T("Choose a folder to show in a live plot"))
	if dir == "" {
		return
	}
	a.newPlot(cursorPos())
	p := a.cfg.Plot(a.selected)
	if p == nil {
		return
	}
	p.Folder = dir
	p.Title = filepath.Base(dir)
	if p.Title == "" || p.Title == "." || p.Title == `\` {
		p.Title = dir
	}
	a.plotChanged()
	a.tick()
}

// Thumbnail caches are capped by size rather than count: 3,000 large
// thumbnails could otherwise hold hundreds of MB.
const thumbBudget, tintBudget = 24 << 20, 12 << 20

var (
	tintCache = map[string]*image.RGBA{}
	tintBytes int
)

// tinted is the entry's icon with the live-folder tint applied (Appearance).
func (a *App) tinted(p *Plot, e folderEntry, px int) image.Image {
	img := a.thumb(e, px)
	if img == nil || a.cfg.TintMode == TintOff || a.cfg.TintStrength <= 0 {
		if img == nil {
			return nil
		}
		return img
	}
	c := a.cfg.TintColor
	if a.cfg.TintMode == TintPlot {
		c = p.Color
		if a.cfg.UniformLook {
			c = a.cfg.UniformColor
		}
		// lift dark plot colours so the tint is visible on the panel
		col := ParseHex(c)
		c = ToHex(color.NRGBA{uint8(128 + int(col.R)/2), uint8(128 + int(col.G)/2), uint8(128 + int(col.B)/2), 255})
	}
	key := fmt.Sprintf("%s|%p|%s|%d", thumbKey(e, px), img, c, a.cfg.TintStrength)
	if t, ok := tintCache[key]; ok {
		return t
	}
	if tintBytes > tintBudget {
		tintCache, tintBytes = map[string]*image.RGBA{}, 0
	}
	t := TintImage(img, ParseHex(c), a.cfg.TintStrength)
	tintCache[key] = t
	tintBytes += len(t.Pix)
	return t
}
