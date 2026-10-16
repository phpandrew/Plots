//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Explorer on some systems stops picking up desktop changes until F5 is
// pressed (new files don't appear, deleted ones linger). Plots watches the
// desktop folders itself and refreshes the desktop view when it falls behind.

const (
	msgDesktopChanged = WM_APP + 10
	timerVerify       = 2
)

var (
	watchMu      sync.Mutex
	watchRenames = map[string]string{}
)

func desktopDirs() []string {
	var out []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Desktop, windows.FOLDERID_PublicDesktop} {
		if p, err := windows.KnownFolderPath(id, 0); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func startDesktopWatcher(main HWND) {
	for _, dir := range desktopDirs() {
		go watchDir(dir, main)
	}
}

func watchDir(dir string, main HWND) {
	h, err := windows.CreateFile(u16(dir), 1, /*FILE_LIST_DIRECTORY*/
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		log.Println("watch", dir, err)
		return
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 64*1024)
	mask := uint32(windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME)
	for {
		var n uint32
		if err := windows.ReadDirectoryChanges(h, &buf[0], uint32(len(buf)), false, mask, &n, nil, 0); err != nil {
			log.Println("watch", dir, err)
			return
		}
		var oldName string
		for off := uint32(0); n > 0 && off < n; {
			fni := (*windows.FileNotifyInformation)(unsafe.Pointer(&buf[off]))
			name := windows.UTF16ToString(unsafe.Slice(&fni.FileName, fni.FileNameLength/2))
			switch fni.Action {
			case windows.FILE_ACTION_RENAMED_OLD_NAME:
				oldName = name
			case windows.FILE_ACTION_RENAMED_NEW_NAME:
				if oldName != "" {
					watchMu.Lock()
					watchRenames[filepath.Join(dir, oldName)] = filepath.Join(dir, name)
					watchMu.Unlock()
				}
			}
			if fni.NextEntryOffset == 0 {
				break
			}
			off += fni.NextEntryOffset
		}
		postMessage(main, msgDesktopChanged, 0, 0)
	}
}

func drainRenames() map[string]string {
	watchMu.Lock()
	defer watchMu.Unlock()
	if len(watchRenames) == 0 {
		return nil
	}
	r := watchRenames
	watchRenames = map[string]string{}
	return r
}

// verifyDesktopView compares the desktop folders with what Explorer shows and
// refreshes the view if they disagree.
func (a *App) verifyDesktopView() {
	if a.fv == nil || a.cfg.Hidden || a.renaming() {
		return
	}
	items, err := a.fv.Items()
	if err != nil {
		return
	}
	inView := map[string]bool{}
	for _, it := range items {
		inView[strings.ToLower(it.Path)] = true
	}
	stale := ""
	for _, dir := range desktopDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		onDisk := map[string]bool{}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if isTempName(e.Name()) {
				onDisk[strings.ToLower(p)] = true
				continue
			}
			onDisk[strings.ToLower(p)] = true
			info, err := e.Info()
			if err != nil {
				continue
			}
			if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && d.FileAttributes&(windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_SYSTEM) != 0 {
				continue
			}
			if !inView[strings.ToLower(p)] {
				stale = "missing " + e.Name()
			}
		}
		prefix := strings.ToLower(dir) + `\`
		for p := range inView {
			if strings.HasPrefix(p, prefix) && !strings.Contains(p[len(prefix):], `\`) && !onDisk[p] && !isTempName(p) {
				stale = "still showing deleted " + p[len(prefix):]
			}
		}
	}
	if stale != "" {
		log.Println("desktop view out of date (" + stale + "), refreshing")
		a.fv.Refresh()
		postMessage(a.main, WM_APP+11, 0, 0) // re-read after Explorer re-enumerates
	}
}

// temporary files (browser downloads in progress, Office lock files) come and
// go too fast to be worth a desktop refresh
func isTempName(name string) bool {
	low := strings.ToLower(name)
	for _, ext := range tempExt {
		if strings.HasSuffix(low, ext) {
			return true
		}
	}
	return strings.HasPrefix(filepath.Base(low), "~$")
}
