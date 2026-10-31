//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Memory: Plots keeps its working set small by collecting garbage sooner
// than Go's default, handing freed memory back to Windows after big one-off
// jobs (decoding a wallpaper or picture), and mapping system font files
// instead of copying them into its own memory.

var (
	pGdiFlush                = gdi32.NewProc("GdiFlush")
	pK32GetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	lastMemLog               time.Time
)

func init() {
	readFontFile = mapFontFile
}

// tuneMemory runs once at start-up.
func tuneMemory() {
	debug.SetGCPercent(50) // heap grows to 1.5x what's live instead of 2x
}

type processMemoryCounters struct {
	cb                                                 uint32
	PageFaultCount                                     uint32
	PeakWorkingSetSize, WorkingSetSize                 uintptr
	QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage       uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage uintptr
	PagefileUsage, PeakPagefileUsage                   uintptr
}

// logMemory writes a line about memory use to the log every half hour.
func logMemory(force bool) {
	if !force && time.Since(lastMemLog) < 30*time.Minute {
		return
	}
	lastMemLog = time.Now()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	var pmc processMemoryCounters
	pmc.cb = uint32(unsafe.Sizeof(pmc))
	call(pK32GetProcessMemoryInfo, uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.cb))
	mb := func(n uint64) float64 { return float64(n) / (1 << 20) }
	log.Printf("memory: private %.1f MB, working set %.1f MB (peak %.1f), Go heap %.1f MB in use, wallpapers %d, thumbnails %.1f MB",
		mb(uint64(pmc.PagefileUsage)), mb(uint64(pmc.WorkingSetSize)), mb(uint64(pmc.PeakWorkingSetSize)), mb(ms.HeapInuse), len(wc.images), mb(uint64(thumbBytes)))
}

// mapFontFile maps a font from the Windows fonts folder into memory
// read-only instead of reading it: the pages are shared with every other
// program using that font and Windows can drop them when they're idle.
// Fonts elsewhere (or if mapping fails) are read normally.
func mapFontFile(path string) ([]byte, error) {
	win := os.Getenv("WINDIR")
	if win == "" || !strings.HasPrefix(strings.ToLower(filepath.Clean(path)), strings.ToLower(filepath.Join(win, "Fonts"))+`\`) {
		return os.ReadFile(path)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	f, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(f)
	st, err := os.Stat(path)
	if err != nil || st.Size() <= 0 || st.Size() > 1<<30 {
		return os.ReadFile(path)
	}
	size := st.Size()
	m, err := windows.CreateFileMapping(f, nil, windows.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		return os.ReadFile(path)
	}
	defer windows.CloseHandle(m)
	addr, err := windows.MapViewOfFile(m, windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		return os.ReadFile(path)
	}
	// stays mapped for the life of the process, like the font cache that uses it
	return unsafe.Slice((*byte)(unsafe.Pointer(addr)), int(size)), nil
}
