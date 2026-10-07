package main

import (
	"errors"
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

// Seletor de área: uma janela sem borda que cobre todos os monitores, mostra
// a captura "congelada" e escurecida, e deixa o mouse clarear o retângulo
// escolhido. Roda numa thread própria, com o seu laço de mensagens.

const (
	wmDestroy     = 0x0002
	wmPaint       = 0x000F
	wmEraseBkgnd  = 0x0014
	wmKeyDown     = 0x0100
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204

	vkEscape = 0x1B

	wsPopup         = 0x80000000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	swShow          = 5
	idcCross        = 32515
	nullBrush       = 5
	defaultGUIFont  = 17
	transparentMode = 1
	opaqueMode      = 2
)

type overlay struct {
	sh   *shot
	w, h int

	hwnd uintptr

	origDC, origBmp, origOld uintptr
	dimDC, dimBmp, dimOld    uintptr
	backDC, backBmp, backOld uintptr
	pen                      uintptr

	dragging bool
	sx, sy   int
	cx, cy   int

	done      bool
	cancelled bool
	result    image.Rectangle
}

var (
	activeOverlay *overlay
	overlayProcCB uintptr
	overlayClass  *uint16
)

// selectRegion mostra o seletor e devolve a área escolhida em coordenadas da
// tela virtual. ok=false se o usuário cancelou. Precisa rodar numa goroutine
// travada numa thread do sistema (runtime.LockOSThread).
func selectRegion(sh *shot) (image.Rectangle, bool, error) {
	ov := &overlay{sh: sh, w: sh.W, h: sh.H}
	if err := ov.setup(); err != nil {
		ov.cleanup()
		return image.Rectangle{}, false, err
	}
	defer ov.cleanup()

	activeOverlay = ov
	defer func() { activeOverlay = nil }()

	if overlayProcCB == 0 {
		overlayProcCB = syscall.NewCallback(overlayProc)
		overlayClass = utf16Ptr("SnowShotOverlay")
	}
	hInst := callU(pGetModuleHandleW, 0)
	cursor := callU(pLoadCursorW, 0, idcCross)

	wc := wndClassEx{
		WndProc:   overlayProcCB,
		Instance:  hInst,
		Cursor:    cursor,
		ClassName: overlayClass,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	// Registrar de novo a mesma classe só dá erro "já existe", que ignoramos.
	callU(pRegisterClassExW, uintptr(unsafe.Pointer(&wc)))

	hwnd := callU(pCreateWindowExW,
		wsExTopmost|wsExToolWindow,
		uintptr(unsafe.Pointer(overlayClass)),
		uintptr(unsafe.Pointer(utf16Ptr("SnowShot"))),
		wsPopup,
		uintptr(sh.X), uintptr(sh.Y), uintptr(sh.W), uintptr(sh.H),
		0, 0, hInst, 0)
	if hwnd == 0 {
		return image.Rectangle{}, false, errors.New("não consegui abrir o seletor de área")
	}
	ov.hwnd = hwnd

	callU(pShowWindow, hwnd, swShow)
	callU(pSetForegroundWindow, hwnd)
	callU(pSetFocus, hwnd)

	var m msg
	for !ov.done {
		r := callU(pGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = erro
			break
		}
		callU(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
		callU(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}

	if ov.cancelled || ov.result.Empty() {
		return image.Rectangle{}, false, nil
	}
	return ov.result.Add(image.Pt(sh.X, sh.Y)), true, nil
}

// setup prepara as imagens: a original, a escurecida e um "quadro" para
// desenhar sem piscar.
func (o *overlay) setup() error {
	hdcScr := callU(pGetDC, 0)
	if hdcScr == 0 {
		return errors.New("não consegui acessar a tela")
	}
	defer callU(pReleaseDC, 0, hdcScr)
	return o.fill(hdcScr, o.w*o.h*4)
}

func (o *overlay) fill(hdcScr uintptr, n int) error {
	// Original
	origBmp, origBits := newDIBSection(hdcScr, o.w, o.h)
	if origBmp == 0 || origBits == nil {
		return errors.New("sem memória para o seletor de área")
	}
	o.origBmp = origBmp
	copy(unsafe.Slice((*byte)(origBits), n), o.sh.Pix)

	// Escurecida
	dimBmp, dimBits := newDIBSection(hdcScr, o.w, o.h)
	if dimBmp == 0 || dimBits == nil {
		return errors.New("sem memória para o seletor de área")
	}
	o.dimBmp = dimBmp
	dst := unsafe.Slice((*byte)(dimBits), n)
	for i := 0; i < n; i += 4 {
		dst[i] = o.sh.Pix[i] >> 1
		dst[i+1] = o.sh.Pix[i+1] >> 1
		dst[i+2] = o.sh.Pix[i+2] >> 1
		dst[i+3] = 255
	}

	// DCs
	o.origDC = callU(pCreateCompatibleDC, hdcScr)
	o.dimDC = callU(pCreateCompatibleDC, hdcScr)
	o.backDC = callU(pCreateCompatibleDC, hdcScr)
	o.backBmp = callU(pCreateCompatibleBitmap, hdcScr, uintptr(o.w), uintptr(o.h))
	if o.origDC == 0 || o.dimDC == 0 || o.backDC == 0 || o.backBmp == 0 {
		return errors.New("falha ao preparar o seletor de área")
	}
	o.origOld = callU(pSelectObject, o.origDC, o.origBmp)
	o.dimOld = callU(pSelectObject, o.dimDC, o.dimBmp)
	o.backOld = callU(pSelectObject, o.backDC, o.backBmp)

	const psSolid = 0
	o.pen = callU(pCreatePen, psSolid, 2, rgb(80, 170, 255))
	return nil
}

func (o *overlay) cleanup() {
	if o.hwnd != 0 {
		// Se a janela ainda existe (erro no meio do caminho), fecha.
		callU(pDestroyWindow, o.hwnd)
		o.hwnd = 0
	}
	if o.origDC != 0 {
		callU(pSelectObject, o.origDC, o.origOld)
		callU(pDeleteDC, o.origDC)
		o.origDC = 0
	}
	if o.dimDC != 0 {
		callU(pSelectObject, o.dimDC, o.dimOld)
		callU(pDeleteDC, o.dimDC)
		o.dimDC = 0
	}
	if o.backDC != 0 {
		callU(pSelectObject, o.backDC, o.backOld)
		callU(pDeleteDC, o.backDC)
		o.backDC = 0
	}
	for _, h := range []*uintptr{&o.origBmp, &o.dimBmp, &o.backBmp, &o.pen} {
		if *h != 0 {
			callU(pDeleteObject, *h)
			*h = 0
		}
	}
}

// selection devolve o retângulo atual (já ajustado para dentro da tela).
func (o *overlay) selection() image.Rectangle {
	r := image.Rect(o.sx, o.sy, o.cx, o.cy).Canon()
	return r.Intersect(image.Rect(0, 0, o.w, o.h))
}

func (o *overlay) paint(hdc uintptr) {
	// 1) fundo escurecido
	callU(pBitBlt, o.backDC, 0, 0, uintptr(o.w), uintptr(o.h), o.dimDC, 0, 0, srcCopy)

	gui := callU(pGetStockObject, defaultGUIFont)
	oldFont := callU(pSelectObject, o.backDC, gui)

	sel := o.selection()
	if o.dragging && !sel.Empty() {
		// 2) a parte escolhida volta ao brilho normal
		callU(pBitBlt, o.backDC, uintptr(sel.Min.X), uintptr(sel.Min.Y),
			uintptr(sel.Dx()), uintptr(sel.Dy()), o.origDC, uintptr(sel.Min.X), uintptr(sel.Min.Y), srcCopy)

		// 3) moldura
		oldPen := callU(pSelectObject, o.backDC, o.pen)
		oldBr := callU(pSelectObject, o.backDC, callU(pGetStockObject, nullBrush))
		callU(pRectangle, o.backDC, uintptr(sel.Min.X), uintptr(sel.Min.Y), uintptr(sel.Max.X), uintptr(sel.Max.Y))
		callU(pSelectObject, o.backDC, oldBr)
		callU(pSelectObject, o.backDC, oldPen)

		// 4) medida
		label := fmt.Sprintf(" %d × %d ", sel.Dx(), sel.Dy())
		ly := sel.Min.Y - 24
		if ly < 2 {
			ly = sel.Min.Y + 6
		}
		o.text(label, sel.Min.X, ly)
	} else if !o.dragging {
		// dica no canto do monitor principal (o canto (0,0) da tela virtual)
		o.text("  Arraste para escolher a área   •   Esc ou botão direito cancelam  ", 24-o.sh.X, 24-o.sh.Y)
	}

	callU(pSelectObject, o.backDC, oldFont)

	// 5) tudo de uma vez na tela
	callU(pBitBlt, hdc, 0, 0, uintptr(o.w), uintptr(o.h), o.backDC, 0, 0, srcCopy)
}

func (o *overlay) text(s string, x, y int) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) <= 1 {
		return
	}
	callU(pSetBkMode, o.backDC, opaqueMode)
	callU(pSetBkColor, o.backDC, rgb(25, 28, 35))
	callU(pSetTextColor, o.backDC, rgb(255, 255, 255))
	callU(pTextOutW, o.backDC, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1))
}

func (o *overlay) finish(cancel bool) {
	o.cancelled = cancel
	if !cancel {
		o.result = o.selection()
		if o.result.Dx() < 4 || o.result.Dy() < 4 {
			o.cancelled = true // clique sem arrastar não captura nada
		}
	}
	o.done = true
	callU(pReleaseCapture)
	callU(pDestroyWindow, o.hwnd)
	o.hwnd = 0
}

func overlayProc(hwnd, m, wp, lp uintptr) uintptr {
	o := activeOverlay
	if o == nil {
		return callU(pDefWindowProcW, hwnd, m, wp, lp)
	}
	x := int(int16(lp & 0xFFFF))
	y := int(int16((lp >> 16) & 0xFFFF))

	switch uint32(m) {
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		var ps paintStruct
		hdc := callU(pBeginPaint, hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			o.paint(hdc)
		}
		callU(pEndPaint, hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmLButtonDown:
		o.dragging = true
		o.sx, o.sy, o.cx, o.cy = x, y, x, y
		callU(pSetCapture, hwnd)
		callU(pInvalidateRect, hwnd, 0, 0)
		return 0
	case wmMouseMove:
		if o.dragging {
			o.cx, o.cy = x, y
			callU(pInvalidateRect, hwnd, 0, 0)
		}
		return 0
	case wmLButtonUp:
		if o.dragging {
			o.cx, o.cy = x, y
			o.dragging = false
			o.finish(false)
		}
		return 0
	case wmRButtonDown:
		o.finish(true)
		return 0
	case wmKeyDown:
		if wp == vkEscape {
			o.finish(true)
		}
		return 0
	case wmDestroy:
		o.done = true
		return 0
	}
	return callU(pDefWindowProcW, hwnd, m, wp, lp)
}
