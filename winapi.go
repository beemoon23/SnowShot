package main

import (
	"syscall"
	"unsafe"
)

// Chamadas diretas à API do Windows (user32, gdi32, kernel32, dwmapi).
// Tudo aqui é "cola": nada de lógica do app.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	// user32
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pGetDC                    = user32.NewProc("GetDC")
	pReleaseDC                = user32.NewProc("ReleaseDC")
	pRegisterHotKey           = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pPeekMessageW             = user32.NewProc("PeekMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostThreadMessageW       = user32.NewProc("PostThreadMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pShowWindow               = user32.NewProc("ShowWindow")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pSetFocus                 = user32.NewProc("SetFocus")
	pSetCapture               = user32.NewProc("SetCapture")
	pReleaseCapture           = user32.NewProc("ReleaseCapture")
	pInvalidateRect           = user32.NewProc("InvalidateRect")
	pBeginPaint               = user32.NewProc("BeginPaint")
	pEndPaint                 = user32.NewProc("EndPaint")
	pLoadCursorW              = user32.NewProc("LoadCursorW")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pMonitorFromPoint         = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	pOpenClipboard            = user32.NewProc("OpenClipboard")
	pCloseClipboard           = user32.NewProc("CloseClipboard")
	pEmptyClipboard           = user32.NewProc("EmptyClipboard")
	pSetClipboardData         = user32.NewProc("SetClipboardData")
	pRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	pAdjustWindowRectEx       = user32.NewProc("AdjustWindowRectEx")
	pGetClientRect            = user32.NewProc("GetClientRect")
	pFillRect                 = user32.NewProc("FillRect")
	pSetCursor                = user32.NewProc("SetCursor")
	pScreenToClient           = user32.NewProc("ScreenToClient")
	pGetKeyState              = user32.NewProc("GetKeyState")
	pMessageBoxW              = user32.NewProc("MessageBoxW")
	pGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	pLoadIconW                = user32.NewProc("LoadIconW")

	// gdi32
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pCreatePen              = gdi32.NewProc("CreatePen")
	pGetStockObject         = gdi32.NewProc("GetStockObject")
	pRectangle              = gdi32.NewProc("Rectangle")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pSetBkColor             = gdi32.NewProc("SetBkColor")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pTextOutW               = gdi32.NewProc("TextOutW")
	pStretchDIBits          = gdi32.NewProc("StretchDIBits")
	pSetStretchBltMode      = gdi32.NewProc("SetStretchBltMode")
	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pGetTextExtentPoint32W  = gdi32.NewProc("GetTextExtentPoint32W")
	pGdiFlush               = gdi32.NewProc("GdiFlush")

	// kernel32
	pGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	pGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	pGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	pGlobalFree         = kernel32.NewProc("GlobalFree")
	pGlobalLock         = kernel32.NewProc("GlobalLock")
	pGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	pCreateMutexW       = kernel32.NewProc("CreateMutexW")
	pCloseHandle        = kernel32.NewProc("CloseHandle")

	// dwmapi
	pDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

// Constantes usadas em mais de um arquivo.
const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srcCopy    = 0x00CC0020
	captureBlt = 0x40000000

	cfDIB = 8

	wmQuit   = 0x0012
	wmHotkey = 0x0312
	wmApp    = 0x8000

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modNoRepeat = 0x4000
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type paintStruct struct {
	Hdc      uintptr
	FErase   int32
	RcPaint  rect
	FRestore int32
	FIncUpd  int32
	Reserved [32]byte
}

type bitmapInfoHeader struct {
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

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

// callU chama uma função da API e devolve só o valor de retorno.
func callU(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

// ptr converte um uintptr devolvido pelo Windows em unsafe.Pointer sem
// acionar o aviso do "go vet" (o bloco de memória é do Windows, não do Go).
func ptr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

func metric(i int) int {
	return int(int32(callU(pGetSystemMetrics, uintptr(i))))
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func rgb(r, g, b byte) uintptr {
	return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16
}

// newDIBSection cria um bitmap de 32 bits (de cima para baixo) cujos pixels
// podem ser lidos/gravados direto pela memória.
func newDIBSection(hdc uintptr, w, h int) (uintptr, unsafe.Pointer) {
	bi := bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(w),
		Height:   -int32(h), // negativo = de cima para baixo
		Planes:   1,
		BitCount: 32,
	}
	var bits unsafe.Pointer
	hbm := callU(pCreateDIBSection, hdc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	return hbm, bits
}
