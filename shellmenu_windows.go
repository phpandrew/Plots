//go:build windows

package main

import (
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The real Explorer right-click menu for a file shown in a live-folder plot.

var (
	IID_IContextMenu  = windows.GUID{0x000214E4, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	IID_IContextMenu2 = windows.GUID{0x000214F4, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

	pSHParseDisplayName = shell32.NewProc("SHParseDisplayName")
	pSHBindToParent     = shell32.NewProc("SHBindToParent")

	// activeMenu receives owner-draw / sub-menu messages while the menu is open
	activeMenu comObj
)

type cmInvokeInfo struct {
	Size       uint32
	Mask       uint32
	Hwnd       HWND
	Verb       uintptr
	Parameters uintptr
	Directory  uintptr
	Show       int32
	HotKey     uint32
	Icon       uintptr
}

func showShellMenu(owner HWND, path string, pt Point) {
	var pidl uintptr
	if hr := call(pSHParseDisplayName, uintptr(unsafe.Pointer(u16(path))), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0); int32(hr) < 0 || pidl == 0 {
		log.Println("context menu: parse failed for", path)
		return
	}
	defer call(pCoTaskMemFree, pidl)
	var folder comObj
	var child uintptr
	if hr := call(pSHBindToParent, pidl, uintptr(unsafe.Pointer(&IID_IShellFolder)), uintptr(unsafe.Pointer(&folder)), uintptr(unsafe.Pointer(&child))); int32(hr) < 0 {
		return
	}
	defer folder.Release()
	var cm comObj
	if hr := folder.vcall(10, owner, 1, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&IID_IContextMenu)), 0, uintptr(unsafe.Pointer(&cm))); int32(hr) < 0 || cm == 0 {
		return
	}
	defer cm.Release()
	if cm2, err := cm.QI(&IID_IContextMenu2); err == nil {
		activeMenu = cm2
		defer func() { cm2.Release(); activeMenu = 0 }()
	}
	menu := call(pCreatePopupMenu)
	defer call(pDestroyMenu, menu)
	const first = 1
	cm.vcall(3, menu, 0, first, 0x7FFF, 0 /*CMF_NORMAL*/)
	call(pSetForegroundWindow, owner)
	cmd := call(pTrackPopupMenu, menu, TPM_RETURNCMD|TPM_RIGHTBUTTON, uintptr(pt.X), uintptr(pt.Y), 0, owner, 0)
	postMessage(owner, 0, 0, 0)
	if cmd < first {
		return
	}
	ci := cmInvokeInfo{Hwnd: owner, Verb: cmd - first, Show: 1}
	ci.Size = uint32(unsafe.Sizeof(ci))
	if hr := cm.vcall(4, uintptr(unsafe.Pointer(&ci))); int32(hr) < 0 {
		log.Printf("context menu command %d failed: 0x%08X", cmd, uint32(hr))
	}
}

// forwardMenuMsg lets the shell draw its "Open with" / "Send to" sub-menus.
func forwardMenuMsg(msg uint32, wp, lp uintptr) bool {
	if activeMenu == 0 {
		return false
	}
	switch msg {
	case 0x0117, 0x002B, 0x002C: // WM_INITMENUPOPUP, WM_DRAWITEM, WM_MEASUREITEM
		activeMenu.vcall(6, uintptr(msg), wp, lp)
		return true
	}
	return false
}
