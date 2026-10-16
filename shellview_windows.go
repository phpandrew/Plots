//go:build windows

package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Desktop icon access through the documented shell interfaces:
// IShellWindows -> desktop IShellBrowser -> IShellView -> IFolderView(2).

var (
	CLSID_ShellWindows   = windows.GUID{0x9BA05972, 0xF6A8, 0x11CF, [8]byte{0xA4, 0x42, 0x00, 0xA0, 0xC9, 0x0A, 0x8F, 0x39}}
	IID_IShellWindows    = windows.GUID{0x85CB6900, 0x4D95, 0x11CF, [8]byte{0x96, 0x0C, 0x00, 0x80, 0xC7, 0xF4, 0xEE, 0x85}}
	IID_IServiceProvider = windows.GUID{0x6D5140C1, 0x7436, 0x11CE, [8]byte{0x80, 0x34, 0x00, 0xAA, 0x00, 0x60, 0x09, 0xFA}}
	SID_STopLevelBrowser = windows.GUID{0x4C96BE40, 0x915C, 0x11CF, [8]byte{0x99, 0xD3, 0x00, 0xAA, 0x00, 0x4A, 0xE8, 0x37}}
	IID_IShellBrowser    = windows.GUID{0x000214E2, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	IID_IFolderView      = windows.GUID{0xCDE725B0, 0xCCC9, 0x4519, [8]byte{0x91, 0x7E, 0x32, 0x5D, 0x72, 0xFA, 0xB4, 0xCE}}
	IID_IFolderView2     = windows.GUID{0x1AF3A467, 0x214F, 0x4298, [8]byte{0x90, 0x8E, 0x06, 0xB0, 0x3E, 0x0B, 0x39, 0xF9}}
	IID_IShellFolder     = windows.GUID{0x000214E6, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

const (
	CLSCTX_ALL               = 0x17
	SWC_DESKTOP              = 8
	SWFO_NEEDDISPATCH        = 1
	SHGDN_FORPARSING         = 0x8000
	SVSI_POSITIONITEM        = 0x80
	FWF_AUTOARRANGE          = 0x1
	FWF_SNAPTOGRID           = 0x4
	COINIT_APARTMENTTHREADED = 0x2
)

type comObj uintptr

func (o comObj) vcall(idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(o))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	all := append([]uintptr{uintptr(o)}, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func (o comObj) Release() {
	if o != 0 {
		o.vcall(2)
	}
}

func (o comObj) QI(iid *windows.GUID) (comObj, error) {
	var out uintptr
	hr := o.vcall(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if int32(hr) < 0 {
		return 0, hresult("QueryInterface", hr)
	}
	return comObj(out), nil
}

func hresult(what string, hr uintptr) error {
	return fmt.Errorf("%s failed: 0x%08X", what, uint32(hr))
}

type variant struct {
	VT         uint16
	r1, r2, r3 uint16
	Val        int64
	pad        int64
}

type FolderView struct {
	fv     comObj             // IFolderView
	sv     comObj             // IShellView
	fv2    comObj             // IFolderView2 (may be 0)
	folder comObj             // IShellFolder
	pidls  map[string]uintptr // path -> child pidl (owned)
	names  map[string]string  // pidl bytes -> parsing name cache
}

func OpenFolderView() (*FolderView, error) {
	var sw comObj
	hr := call(pCoCreateInstance, uintptr(unsafe.Pointer(&CLSID_ShellWindows)), 0, CLSCTX_ALL,
		uintptr(unsafe.Pointer(&IID_IShellWindows)), uintptr(unsafe.Pointer(&sw)))
	if int32(hr) < 0 {
		return nil, hresult("CoCreateInstance(ShellWindows)", hr)
	}
	defer sw.Release()

	loc := variant{VT: 3 /*VT_I4*/, Val: 0 /*CSIDL_DESKTOP*/}
	empty := variant{}
	var hwnd int32
	var disp comObj
	hr = sw.vcall(15, uintptr(unsafe.Pointer(&loc)), uintptr(unsafe.Pointer(&empty)), SWC_DESKTOP,
		uintptr(unsafe.Pointer(&hwnd)), SWFO_NEEDDISPATCH, uintptr(unsafe.Pointer(&disp)))
	if int32(hr) < 0 || disp == 0 {
		return nil, hresult("FindWindowSW", hr)
	}
	defer disp.Release()

	sp, err := disp.QI(&IID_IServiceProvider)
	if err != nil {
		return nil, err
	}
	defer sp.Release()
	var sb comObj
	hr = sp.vcall(3, uintptr(unsafe.Pointer(&SID_STopLevelBrowser)), uintptr(unsafe.Pointer(&IID_IShellBrowser)), uintptr(unsafe.Pointer(&sb)))
	if int32(hr) < 0 {
		return nil, hresult("QueryService", hr)
	}
	defer sb.Release()
	var sv comObj
	hr = sb.vcall(15, uintptr(unsafe.Pointer(&sv)))
	if int32(hr) < 0 {
		return nil, hresult("QueryActiveShellView", hr)
	}
	fv, err := sv.QI(&IID_IFolderView)
	if err != nil {
		sv.Release()
		return nil, err
	}
	f := &FolderView{fv: fv, sv: sv, pidls: map[string]uintptr{}, names: map[string]string{}}
	f.fv2, _ = sv.QI(&IID_IFolderView2)
	var folder comObj
	hr = fv.vcall(5, uintptr(unsafe.Pointer(&IID_IShellFolder)), uintptr(unsafe.Pointer(&folder)))
	if int32(hr) < 0 {
		f.Close()
		return nil, hresult("GetFolder", hr)
	}
	f.folder = folder
	return f, nil
}

func (f *FolderView) Close() {
	if f == nil {
		return
	}
	f.freePidls()
	f.folder.Release()
	f.fv2.Release()
	f.fv.Release()
	f.sv.Release()
	f.fv, f.fv2, f.folder, f.sv = 0, 0, 0, 0
}

func (f *FolderView) freePidls() {
	for _, p := range f.pidls {
		call(pCoTaskMemFree, p)
	}
	f.pidls = map[string]uintptr{}
}

func pidlBytes(p uintptr) string {
	n := call(pILGetSize, p)
	if n == 0 || n > 4096 {
		return ""
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
}

var ErrViewGone = errors.New("desktop view unavailable")

// Spacing returns the icon cell size in pixels.
func (f *FolderView) Spacing() Point {
	var pt POINT
	if hr := f.fv.vcall(12, uintptr(unsafe.Pointer(&pt))); int32(hr) < 0 || pt.X < 16 {
		return Point{75, 100}
	}
	return Point{int(pt.X), int(pt.Y)}
}

// Items returns every desktop item with its position in listview coordinates.
func (f *FolderView) Items() ([]Item, error) {
	var n int32
	if hr := f.fv.vcall(7, 2 /*SVGIO_ALLVIEW*/, uintptr(unsafe.Pointer(&n))); int32(hr) < 0 {
		return nil, ErrViewGone
	}
	f.freePidls()
	items := make([]Item, 0, n)
	usedNames := map[string]bool{}
	for i := int32(0); i < n; i++ {
		var pidl uintptr
		if hr := f.fv.vcall(6, uintptr(i), uintptr(unsafe.Pointer(&pidl))); int32(hr) < 0 || pidl == 0 {
			continue
		}
		key := pidlBytes(pidl)
		name, ok := f.names[key]
		if !ok {
			name = f.displayName(pidl)
			if name == "" {
				call(pCoTaskMemFree, pidl)
				continue
			}
			f.names[key] = name
		}
		usedNames[key] = true
		var pt POINT
		if hr := f.fv.vcall(11, pidl, uintptr(unsafe.Pointer(&pt))); int32(hr) < 0 {
			call(pCoTaskMemFree, pidl)
			continue
		}
		if old, dup := f.pidls[name]; dup {
			call(pCoTaskMemFree, old)
		}
		f.pidls[name] = pidl
		items = append(items, Item{Path: name, Pos: Point{int(pt.X), int(pt.Y)}})
	}
	for k := range f.names {
		if !usedNames[k] {
			delete(f.names, k)
		}
	}
	return items, nil
}

func (f *FolderView) displayName(pidl uintptr) string {
	var strret [272]byte
	if hr := f.folder.vcall(11, pidl, SHGDN_FORPARSING, uintptr(unsafe.Pointer(&strret[0]))); int32(hr) < 0 {
		return ""
	}
	var psz *uint16
	if hr := call(pStrRetToStrW, uintptr(unsafe.Pointer(&strret[0])), pidl, uintptr(unsafe.Pointer(&psz))); int32(hr) < 0 || psz == nil {
		return ""
	}
	s := windows.UTF16PtrToString(psz)
	call(pCoTaskMemFree, uintptr(unsafe.Pointer(psz)))
	return s
}

// Position moves items (listview coordinates). Selection is left untouched.
func (f *FolderView) Position(moves map[string]Point) error {
	var pidls []uintptr
	var pts []POINT
	for path, p := range moves {
		pidl, ok := f.pidls[path]
		if !ok {
			continue
		}
		pidls = append(pidls, pidl)
		pts = append(pts, POINT{int32(p.X), int32(p.Y)})
	}
	if len(pidls) == 0 {
		return nil
	}
	hr := f.fv.vcall(16, uintptr(len(pidls)), uintptr(unsafe.Pointer(&pidls[0])), uintptr(unsafe.Pointer(&pts[0])), SVSI_POSITIONITEM)
	if int32(hr) < 0 {
		return hresult("SelectAndPositionItems", hr)
	}
	return nil
}

// EnsureFreeLayout turns off "Auto arrange icons" and "Align icons to grid",
// which would otherwise fight with plot layouts.
func (f *FolderView) EnsureFreeLayout() {
	if f.fv2 == 0 {
		return
	}
	var flags uint32
	if hr := f.fv2.vcall(25, uintptr(unsafe.Pointer(&flags))); int32(hr) < 0 {
		return
	}
	if flags&(FWF_AUTOARRANGE|FWF_SNAPTOGRID) != 0 {
		f.fv2.vcall(24, FWF_AUTOARRANGE|FWF_SNAPTOGRID, 0)
	}
}

// Refresh re-enumerates the desktop (like pressing F5 on it). Icon positions are kept.
func (f *FolderView) Refresh() {
	if f.sv != 0 {
		f.sv.vcall(8)
	}
}

// IconSize returns the desktop icon image size (logical px), 48 if unknown.
func (f *FolderView) IconSize() int {
	if f.fv2 == 0 {
		return 48
	}
	var mode, size int32
	if hr := f.fv2.vcall(36, uintptr(unsafe.Pointer(&mode)), uintptr(unsafe.Pointer(&size))); int32(hr) < 0 || size < 16 || size > 256 {
		return 48
	}
	return int(size)
}
