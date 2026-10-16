//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	shlwapi  = windows.NewLazySystemDLL("shlwapi.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")

	pRegisterClassExW           = user32.NewProc("RegisterClassExW")
	pCreateWindowExW            = user32.NewProc("CreateWindowExW")
	pDestroyWindow              = user32.NewProc("DestroyWindow")
	pDefWindowProcW             = user32.NewProc("DefWindowProcW")
	pGetMessageW                = user32.NewProc("GetMessageW")
	pTranslateMessage           = user32.NewProc("TranslateMessage")
	pDispatchMessageW           = user32.NewProc("DispatchMessageW")
	pIsDialogMessageW           = user32.NewProc("IsDialogMessageW")
	pPostMessageW               = user32.NewProc("PostMessageW")
	pSendMessageW               = user32.NewProc("SendMessageW")
	pSendMessageTimeoutW        = user32.NewProc("SendMessageTimeoutW")
	pPostQuitMessage            = user32.NewProc("PostQuitMessage")
	pFindWindowW                = user32.NewProc("FindWindowW")
	pFindWindowExW              = user32.NewProc("FindWindowExW")
	pEnumWindows                = user32.NewProc("EnumWindows")
	pSetParent                  = user32.NewProc("SetParent")
	pGetParent                  = user32.NewProc("GetParent")
	pSetWindowPos               = user32.NewProc("SetWindowPos")
	pShowWindow                 = user32.NewProc("ShowWindow")
	pIsWindow                   = user32.NewProc("IsWindow")
	pIsWindowVisible            = user32.NewProc("IsWindowVisible")
	pGetWindowRect              = user32.NewProc("GetWindowRect")
	pGetClientRect              = user32.NewProc("GetClientRect")
	pClientToScreen             = user32.NewProc("ClientToScreen")
	pScreenToClient             = user32.NewProc("ScreenToClient")
	pUpdateLayeredWindow        = user32.NewProc("UpdateLayeredWindow")
	pGetDC                      = user32.NewProc("GetDC")
	pReleaseDC                  = user32.NewProc("ReleaseDC")
	pSetTimer                   = user32.NewProc("SetTimer")
	pKillTimer                  = user32.NewProc("KillTimer")
	pRegisterWindowMessageW     = user32.NewProc("RegisterWindowMessageW")
	pEnumDisplayMonitors        = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW            = user32.NewProc("GetMonitorInfoW")
	pMonitorFromPoint           = user32.NewProc("MonitorFromPoint")
	pMonitorFromRect            = user32.NewProc("MonitorFromRect")
	pCreatePopupMenu            = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                = user32.NewProc("AppendMenuW")
	pTrackPopupMenu             = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                = user32.NewProc("DestroyMenu")
	pSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	pGetCursorPos               = user32.NewProc("GetCursorPos")
	pSetWindowsHookExW          = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx        = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx             = user32.NewProc("CallNextHookEx")
	pWindowFromPoint            = user32.NewProc("WindowFromPoint")
	pGetClassNameW              = user32.NewProc("GetClassNameW")
	pGetAncestor                = user32.NewProc("GetAncestor")
	pGetDoubleClickTime         = user32.NewProc("GetDoubleClickTime")
	pGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	pLoadImageW                 = user32.NewProc("LoadImageW")
	pLoadCursorW                = user32.NewProc("LoadCursorW")
	pSetWindowTextW             = user32.NewProc("SetWindowTextW")
	pGetWindowTextW             = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW       = user32.NewProc("GetWindowTextLengthW")
	pEnableWindow               = user32.NewProc("EnableWindow")
	pMessageBoxW                = user32.NewProc("MessageBoxW")
	pGetDlgCtrlID               = user32.NewProc("GetDlgCtrlID")
	pSetFocus                   = user32.NewProc("SetFocus")
	pGetDpiForWindow            = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem            = user32.NewProc("GetDpiForSystem")
	pSetProcessDpiAwarenessCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	pInvalidateRect             = user32.NewProc("InvalidateRect")
	pBeginPaint                 = user32.NewProc("BeginPaint")
	pEndPaint                   = user32.NewProc("EndPaint")
	pFillRect                   = user32.NewProc("FillRect")
	pGetSysColorBrush           = user32.NewProc("GetSysColorBrush")
	pCheckMenuItem              = user32.NewProc("CheckMenuItem")
	pGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	pSetWindowLongPtrW          = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW          = user32.NewProc("GetWindowLongPtrW")
	pSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	pRedrawWindow               = user32.NewProc("RedrawWindow")
	pBitBlt                     = gdi32.NewProc("BitBlt")
	pSetBkMode                  = gdi32.NewProc("SetBkMode")
	pSetTextColor               = gdi32.NewProc("SetTextColor")
	pDrawTextW                  = user32.NewProc("DrawTextW")
	pGetKeyState                = user32.NewProc("GetKeyState")
	pGetWindow                  = user32.NewProc("GetWindow")
	pIsWindowEnabled            = user32.NewProc("IsWindowEnabled")
	pDrawFocusRect              = user32.NewProc("DrawFocusRect")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pCreateFontW        = gdi32.NewProc("CreateFontW")
	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pSetBkColor         = gdi32.NewProc("SetBkColor")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")

	pShell_NotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pILGetSize         = shell32.NewProc("ILGetSize")

	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCoCreateInstance = ole32.NewProc("CoCreateInstance")
	pCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")

	pStrRetToStrW = shlwapi.NewProc("StrRetToStrW")

	pInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	pChooseColorW         = comdlg32.NewProc("ChooseColorW")
	pGetDpiForMonitor     = shcore.NewProc("GetDpiForMonitor")
)

type HWND = uintptr

const (
	WS_OVERLAPPED        = 0x00000000
	WS_POPUP             = 0x80000000
	WS_CHILD             = 0x40000000
	WS_VISIBLE           = 0x10000000
	WS_CAPTION           = 0x00C00000
	WS_SYSMENU           = 0x00080000
	WS_MINIMIZEBOX       = 0x00020000
	WS_TABSTOP           = 0x00010000
	WS_GROUP             = 0x00020000
	WS_BORDER            = 0x00800000
	WS_VSCROLL           = 0x00200000
	WS_CLIPSIBLINGS      = 0x04000000
	WS_CLIPCHILDREN      = 0x02000000
	WS_EX_LAYERED        = 0x00080000
	WS_EX_TRANSPARENT    = 0x00000020
	WS_EX_TOOLWINDOW     = 0x00000080
	WS_EX_TOPMOST        = 0x00000008
	WS_EX_NOACTIVATE     = 0x08000000
	WS_EX_NOPARENTNOTIFY = 0x00000004
	WS_EX_CLIENTEDGE     = 0x00000200
	WS_EX_CONTROLPARENT  = 0x00010000

	WM_CREATE           = 0x0001
	WM_DESTROY          = 0x0002
	WM_SIZE             = 0x0005
	WM_PAINT            = 0x000F
	WM_CLOSE            = 0x0010
	WM_ERASEBKGND       = 0x0014
	WM_SETTINGCHANGE    = 0x001A
	WM_SETFONT          = 0x0030
	WM_WINDOWPOSCHANGED = 0x0047
	WM_NCHITTEST        = 0x0084
	WM_COMMAND          = 0x0111
	WM_TIMER            = 0x0113
	WM_HSCROLL          = 0x0114
	WM_CTLCOLORSTATIC   = 0x0138
	WM_CTLCOLORBTN      = 0x0135
	WM_MOUSEMOVE        = 0x0200
	WM_LBUTTONDOWN      = 0x0201
	WM_LBUTTONUP        = 0x0202
	WM_LBUTTONDBLCLK    = 0x0203
	WM_RBUTTONDOWN      = 0x0204
	WM_RBUTTONUP        = 0x0205
	WM_NCLBUTTONDBLCLK  = 0x00A3
	WM_NCRBUTTONUP      = 0x00A5
	WM_NCRBUTTONDOWN    = 0x00A4
	WM_CONTEXTMENU      = 0x007B
	WM_EXITSIZEMOVE     = 0x0232
	WM_DISPLAYCHANGE    = 0x007E
	WM_DPICHANGED       = 0x02E0
	WM_USER             = 0x0400
	WM_APP              = 0x8000
	WM_KEYDOWN          = 0x0100
	WM_GETMINMAXINFO    = 0x0024

	HTCLIENT      = 1
	HTCAPTION     = 2
	HTLEFT        = 10
	HTRIGHT       = 11
	HTTOP         = 12
	HTTOPLEFT     = 13
	HTTOPRIGHT    = 14
	HTBOTTOM      = 15
	HTBOTTOMLEFT  = 16
	HTBOTTOMRIGHT = 17

	SW_HIDE           = 0
	SW_SHOW           = 5
	SW_SHOWNA         = 8
	SW_SHOWNOACTIVATE = 4

	SWP_NOSIZE        = 0x0001
	SWP_NOMOVE        = 0x0002
	SWP_NOZORDER      = 0x0004
	SWP_NOACTIVATE    = 0x0010
	SWP_SHOWWINDOW    = 0x0040
	SWP_NOOWNERZORDER = 0x0200

	ULW_ALPHA    = 2
	AC_SRC_OVER  = 0
	AC_SRC_ALPHA = 1

	MF_STRING       = 0x0
	MF_SEPARATOR    = 0x800
	MF_CHECKED      = 0x8
	MF_GRAYED       = 0x1
	MF_POPUP        = 0x10
	TPM_RETURNCMD   = 0x0100
	TPM_RIGHTBUTTON = 0x0002
	TPM_NONOTIFY    = 0x0080

	WH_MOUSE_LL = 14

	SMTO_NORMAL = 0x0

	MONITOR_DEFAULTTONEAREST = 2
	MONITORINFOF_PRIMARY     = 1

	BS_PUSHBUTTON        = 0x0
	BS_AUTOCHECKBOX      = 0x3
	BS_GROUPBOX          = 0x7
	BS_DEFPUSHBUTTON     = 0x1
	SS_LEFT              = 0x0
	SS_NOTIFY            = 0x100
	SS_CENTERIMAGE       = 0x200
	ES_AUTOHSCROLL       = 0x80
	LBS_NOTIFY           = 0x1
	LBS_NOINTEGRALHEIGHT = 0x100
	LBS_OWNERDRAWFIXED   = 0x10
	LBS_HASSTRINGS       = 0x40
	BN_CLICKED           = 0
	EN_CHANGE            = 0x300
	LBN_SELCHANGE        = 1
	LBN_DBLCLK           = 2
	LB_ADDSTRING         = 0x180
	LB_RESETCONTENT      = 0x184
	LB_SETCURSEL         = 0x186
	LB_GETCURSEL         = 0x188
	BM_GETCHECK          = 0xF0
	BM_SETCHECK          = 0xF1
	TBM_GETPOS           = 0x400
	TBM_SETPOS           = 0x405
	TBM_SETRANGE         = 0x406
	TBS_HORZ             = 0x0
	TBS_NOTICKS          = 0x10

	IMAGE_ICON     = 1
	LR_DEFAULTSIZE = 0x40
	LR_SHARED      = 0x8000
	IDC_ARROW      = 32512

	MB_OK          = 0
	MB_YESNO       = 4
	MB_ICONWARNING = 0x30
	MB_ICONINFO    = 0x40
	IDYES          = 6

	GA_ROOT = 2

	CC_RGBINIT  = 0x1
	CC_FULLOPEN = 0x2

	COLOR_WINDOW  = 5
	COLOR_BTNFACE = 15

	GWLP_USERDATA = -21
)

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }
type SIZE struct{ CX, CY int32 }

type MSG struct {
	Hwnd    HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	_       uint32
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type BLENDFUNCTION struct {
	BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat byte
}

type BITMAPINFOHEADER struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type MONITORINFOEXW struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
	SzDevice  [32]uint16
}

type MSLLHOOKSTRUCT struct {
	Pt          POINT
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

type CHOOSECOLORW struct {
	LStructSize    uint32
	HwndOwner      HWND
	HInstance      uintptr
	RgbResult      uint32
	LpCustColors   *uint32
	Flags          uint32
	LCustData      uintptr
	LpfnHook       uintptr
	LpTemplateName *uint16
}

type PAINTSTRUCT struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

func call(p *windows.LazyProc, a ...uintptr) uintptr {
	r, _, _ := p.Call(a...)
	return r
}

func u16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func up(s string) uintptr { return uintptr(unsafe.Pointer(u16(s))) }

func loword(v uintptr) uint16    { return uint16(v & 0xffff) }
func hiword(v uintptr) uint16    { return uint16((v >> 16) & 0xffff) }
func getXLParam(l uintptr) int32 { return int32(int16(loword(l))) }
func getYLParam(l uintptr) int32 { return int32(int16(hiword(l))) }

func sendMessage(h HWND, msg uint32, w, l uintptr) uintptr {
	return call(pSendMessageW, h, uintptr(msg), w, l)
}
func postMessage(h HWND, msg uint32, w, l uintptr) {
	call(pPostMessageW, h, uintptr(msg), w, l)
}

func findWindow(class string) HWND { return call(pFindWindowW, up(class), 0) }
func findWindowEx(parent, after HWND, class string) HWND {
	return call(pFindWindowExW, parent, after, up(class), 0)
}

func className(h HWND) string {
	var buf [64]uint16
	n := call(pGetClassNameW, h, uintptr(unsafe.Pointer(&buf[0])), 64)
	return windows.UTF16ToString(buf[:n])
}

func windowText(h HWND) string {
	n := call(pGetWindowTextLengthW, h)
	buf := make([]uint16, n+1)
	call(pGetWindowTextW, h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

func setWindowText(h HWND, s string) { call(pSetWindowTextW, h, up(s)) }

func windowRect(h HWND) Rect {
	var r RECT
	call(pGetWindowRect, h, uintptr(unsafe.Pointer(&r)))
	return Rect{int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)}
}

func clientToScreen(h HWND, p Point) Point {
	pt := POINT{int32(p.X), int32(p.Y)}
	call(pClientToScreen, h, uintptr(unsafe.Pointer(&pt)))
	return Point{int(pt.X), int(pt.Y)}
}

func screenToClient(h HWND, p Point) Point {
	pt := POINT{int32(p.X), int32(p.Y)}
	call(pScreenToClient, h, uintptr(unsafe.Pointer(&pt)))
	return Point{int(pt.X), int(pt.Y)}
}

func cursorPos() Point {
	var pt POINT
	call(pGetCursorPos, uintptr(unsafe.Pointer(&pt)))
	return Point{int(pt.X), int(pt.Y)}
}

func isWindow(h HWND) bool { return h != 0 && call(pIsWindow, h) != 0 }

func messageBox(owner HWND, text, title string, flags uintptr) uintptr {
	return call(pMessageBoxW, owner, up(text), up(title), flags)
}

func hInstance() uintptr { return call(pGetModuleHandleW, 0) }

var registeredClasses = map[string]bool{}

func registerClass(name string, proc uintptr, icon uintptr) {
	if registeredClasses[name] {
		return
	}
	wc := WNDCLASSEXW{
		LpfnWndProc:   proc,
		HInstance:     hInstance(),
		LpszClassName: u16(name),
		HCursor:       call(pLoadCursorW, 0, IDC_ARROW),
		HIcon:         icon,
		HIconSm:       icon,
		HbrBackground: COLOR_BTNFACE + 1,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	call(pRegisterClassExW, uintptr(unsafe.Pointer(&wc)))
	registeredClasses[name] = true
}

func createWindow(exStyle uintptr, class, title string, style uintptr, x, y, w, h int, parent HWND, id uintptr) HWND {
	return call(pCreateWindowExW, exStyle, up(class), up(title), style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, id, hInstance(), 0)
}

func appIcon(size int) uintptr {
	return call(pLoadImageW, hInstance(), up("APP"), IMAGE_ICON, uintptr(size), uintptr(size), 0)
}

func dpiForWindow(h HWND) int {
	if pGetDpiForWindow.Find() == nil {
		if d := call(pGetDpiForWindow, h); d != 0 {
			return int(d)
		}
	}
	return 96
}

func dpiForSystem() int {
	if pGetDpiForSystem.Find() == nil {
		return int(call(pGetDpiForSystem))
	}
	return 96
}

var _ = syscall.SyscallN
