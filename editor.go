package main

import (
	"errors"
	"fmt"
	"image"
	"math"
	"syscall"
	"unsafe"
)

// Editor de capturas: janela própria (Win32 puro, como o seletor de área)
// com barra de ferramentas desenhada à mão. As marcações ficam numa lista,
// então dá para desfazer/refazer; a imagem original nunca é alterada.

const (
	wmChar          = 0x0102
	wmSize          = 0x0005
	wmClose         = 0x0010
	wmSetCursor     = 0x0020
	wmGetMinMaxInfo = 0x0024

	wsOverlappedWindow = 0x00CF0000

	vkBack    = 0x08
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkControl = 0x11
	vkKeyY    = 0x59
	vkKeyZ    = 0x5A

	idcArrow = 32512
	idcIBeam = 32513

	stretchColorOnColor = 3
	stretchHalftone     = 4
)

type btnKind int

const (
	bTool btnKind = iota
	bColor
	bSize
	bAction
)

const (
	actUndo = iota
	actRedo
	actDiscard
	actDone
)

type button struct {
	r     image.Rectangle
	label string
	kind  btnKind
	id    int
}

type editor struct {
	hwnd uintptr

	base, committed, work *bitmap
	ops, redo             []op
	cur                   *op
	typing                bool
	prevB                 image.Rectangle
	dragging              bool
	pendingHigh           uint16

	tl                           tool
	colIdx, sizeIdx              int
	scale                        float64
	uiFont                       uintptr
	fontPx                       int
	brBg, brBtn                  uintptr
	brSel, brCanvas              uintptr
	brWhite, brDone              uintptr
	brSwatch                     []uintptr
	curArrow, curCross, curIBeam uintptr

	backDC, backBmp, backOld uintptr
	bw, bh                   int
	cw, ch                   int
	tbH, stH                 int
	btns                     []button
	minW                     int
	cv                       image.Rectangle
	vs                       float64
	ox, oy                   int

	done, accepted bool
}

var (
	activeEditor *editor
	editorProcCB uintptr
	editorClass  *uint16
)

var toolNames = []string{"Seta", "Retângulo", "Destaque", "Borrar", "Texto", "Número"}

var toolHints = []string{
	"Seta: arraste do começo até a ponta. Shift mantém o ângulo reto.",
	"Retângulo: arraste. Shift faz um quadrado.",
	"Destaque: arraste sobre o texto, como um marca-texto.",
	"Borrar: arraste sobre o que quer esconder (vira um mosaico).",
	"Texto: clique, digite e use Enter para nova linha. Esc ou clique fora termina.",
	"Número: clique para colocar 1, 2, 3… (bom para listar passos).",
}

// runEditor abre o editor e devolve a imagem editada. ok=false se o usuário
// descartou. Precisa rodar numa goroutine travada numa thread do sistema.
func runEditor(bm *bitmap) (*bitmap, bool, error) {
	e := &editor{base: bm, tl: toolArrow, sizeIdx: 1, scale: 0}
	e.committed = cloneBitmap(bm)
	e.work = cloneBitmap(bm)
	defer e.cleanup()

	e.brBg = callU(pCreateSolidBrush, rgb(30, 32, 38))
	e.brBtn = callU(pCreateSolidBrush, rgb(58, 62, 74))
	e.brSel = callU(pCreateSolidBrush, rgb(0, 120, 212))
	e.brDone = callU(pCreateSolidBrush, rgb(30, 150, 80))
	e.brCanvas = callU(pCreateSolidBrush, rgb(44, 44, 46))
	e.brWhite = callU(pCreateSolidBrush, rgb(255, 255, 255))
	for _, c := range palette {
		e.brSwatch = append(e.brSwatch, callU(pCreateSolidBrush, rgb(c[0], c[1], c[2])))
	}
	e.curArrow = callU(pLoadCursorW, 0, idcArrow)
	e.curCross = callU(pLoadCursorW, 0, idcCross)
	e.curIBeam = callU(pLoadCursorW, 0, idcIBeam)

	activeEditor = e
	defer func() { activeEditor = nil }()

	if editorProcCB == 0 {
		editorProcCB = syscall.NewCallback(editorProc)
		editorClass = utf16Ptr("SnowShotEditor")
	}
	hInst := callU(pGetModuleHandleW, 0)
	wc := wndClassEx{
		WndProc:   editorProcCB,
		Instance:  hInst,
		Cursor:    e.curArrow,
		Icon:      callU(pLoadIconW, hInst, 2),
		ClassName: editorClass,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	callU(pRegisterClassExW, uintptr(unsafe.Pointer(&wc)))

	// Tamanho inicial: cabe na tela do monitor do mouse.
	mon, ok := monitorUnderCursor()
	if !ok {
		mon = image.Rect(0, 0, 1280, 720)
	}
	s0 := 1.0
	if pGetDpiForSystem.Find() == nil {
		if d := int(callU(pGetDpiForSystem)); d > 0 {
			s0 = float64(d) / 96
		}
	}
	e.layoutMetrics(s0)
	tb := int(math.Round(46 * s0))
	st := int(math.Round(24 * s0))
	cw := bm.W
	if cw < e.minW {
		cw = e.minW
	}
	if max := mon.Dx() * 92 / 100; cw > max {
		cw = max
	}
	chImg := bm.H
	if chImg < int(240*s0) {
		chImg = int(240 * s0)
	}
	if max := mon.Dy()*88/100 - tb - st - int(60*s0); chImg > max {
		chImg = max
	}
	r := rect{0, 0, int32(cw), int32(tb + st + chImg)}
	callU(pAdjustWindowRectEx, uintptr(unsafe.Pointer(&r)), wsOverlappedWindow, 0, 0)
	ww, wh := int(r.Right-r.Left), int(r.Bottom-r.Top)
	wx := mon.Min.X + (mon.Dx()-ww)/2
	wy := mon.Min.Y + (mon.Dy()-wh)/2

	hwnd := callU(pCreateWindowExW, 0,
		uintptr(unsafe.Pointer(editorClass)),
		uintptr(unsafe.Pointer(utf16Ptr("SnowShot — editor de capturas"))),
		wsOverlappedWindow,
		uintptr(wx), uintptr(wy), uintptr(ww), uintptr(wh),
		0, 0, hInst, 0)
	if hwnd == 0 {
		return nil, false, errors.New("não consegui abrir o editor")
	}
	e.hwnd = hwnd
	e.relayout()

	callU(pShowWindow, hwnd, swShow)
	callU(pSetForegroundWindow, hwnd)
	callU(pSetFocus, hwnd)

	var m msg
	for !e.done {
		rr := callU(pGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(rr) <= 0 {
			break
		}
		callU(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
		callU(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}

	if !e.accepted {
		return nil, false, nil
	}
	return e.committed, true, nil
}

func (e *editor) cleanup() {
	if e.hwnd != 0 {
		callU(pDestroyWindow, e.hwnd)
		e.hwnd = 0
	}
	if e.backDC != 0 {
		callU(pSelectObject, e.backDC, e.backOld)
		callU(pDeleteDC, e.backDC)
		e.backDC = 0
	}
	hs := []uintptr{e.backBmp, e.uiFont, e.brBg, e.brBtn, e.brSel, e.brDone, e.brCanvas, e.brWhite}
	hs = append(hs, e.brSwatch...)
	for _, h := range hs {
		if h != 0 {
			callU(pDeleteObject, h)
		}
	}
	e.backBmp, e.uiFont = 0, 0
}

// ------------------------------------------------------------- layout

func (e *editor) measure(label string) (int, int) {
	hdc := callU(pGetDC, 0)
	if hdc == 0 {
		return len(label) * 8, 16
	}
	defer callU(pReleaseDC, 0, hdc)
	old := callU(pSelectObject, hdc, e.uiFont)
	defer callU(pSelectObject, hdc, old)
	u := utf16Of(label)
	var sz sizeStruct
	if len(u) > 0 {
		callU(pGetTextExtentPoint32W, hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)), uintptr(unsafe.Pointer(&sz)))
	}
	return int(sz.CX), int(sz.CY)
}

// layoutMetrics (re)cria a fonte da interface para a escala pedida e calcula
// a largura mínima da barra.
func (e *editor) layoutMetrics(s float64) {
	if s != e.scale || e.uiFont == 0 {
		e.scale = s
		if e.uiFont != 0 {
			callU(pDeleteObject, e.uiFont)
		}
		e.fontPx = int(math.Round(14 * s))
		e.uiFont = makeFont(e.fontPx, false)
	}
	e.buildButtons(0)
}

func (e *editor) buildButtons(cw int) {
	s := e.scale
	pad := int(math.Round(8 * s))
	gap := int(math.Round(6 * s))
	gap2 := int(math.Round(16 * s))
	e.tbH = int(math.Round(46 * s))
	e.stH = int(math.Round(24 * s))
	by := int(math.Round(7 * s))
	bh := e.tbH - 2*by

	e.btns = e.btns[:0]
	x := pad
	add := func(k btnKind, id int, label string, w int) {
		e.btns = append(e.btns, button{image.Rect(x, by, x+w, by+bh), label, k, id})
		x += w + gap
	}
	for i, n := range toolNames {
		tw, _ := e.measure(n)
		add(bTool, i, n, tw+int(22*s))
	}
	x += gap2 - gap
	for i := range palette {
		add(bColor, i, "", bh)
	}
	x += gap2 - gap
	for i, n := range []string{"P", "M", "G"} {
		add(bSize, i, n, bh)
	}
	leftEnd := x - gap

	acts := []struct {
		id    int
		label string
	}{{actUndo, "Desfazer"}, {actRedo, "Refazer"}, {actDiscard, "Descartar"}, {actDone, "Concluir"}}
	widths := make([]int, len(acts))
	total := 0
	for i, a := range acts {
		tw, _ := e.measure(a.label)
		widths[i] = tw + int(26*s)
		total += widths[i] + gap
	}
	total -= gap
	e.minW = leftEnd + gap2 + total + pad
	rx := cw - pad - total
	if rx < leftEnd+gap2 {
		rx = leftEnd + gap2
	}
	for i, a := range acts {
		e.btns = append(e.btns, button{image.Rect(rx, by, rx+widths[i], by+bh), a.label, bAction, a.id})
		rx += widths[i] + gap
	}
}

// relayout recalcula tudo a partir do tamanho atual da janela.
func (e *editor) relayout() {
	if e.hwnd == 0 {
		return
	}
	dpi := 96
	if pGetDpiForWindow.Find() == nil {
		if d := int(callU(pGetDpiForWindow, e.hwnd)); d > 0 {
			dpi = d
		}
	}
	e.layoutMetrics(float64(dpi) / 96)
	var cr rect
	callU(pGetClientRect, e.hwnd, uintptr(unsafe.Pointer(&cr)))
	e.cw, e.ch = int(cr.Right), int(cr.Bottom)
	e.buildButtons(e.cw)

	e.cv = image.Rect(0, e.tbH, e.cw, e.ch-e.stH)
	e.vs = 1
	if e.cv.Dx() > 0 && e.cv.Dy() > 0 {
		e.vs = math.Min(1, math.Min(float64(e.cv.Dx())/float64(e.base.W), float64(e.cv.Dy())/float64(e.base.H)))
	}
	dw, dh := int(float64(e.base.W)*e.vs), int(float64(e.base.H)*e.vs)
	e.ox = e.cv.Min.X + (e.cv.Dx()-dw)/2
	e.oy = e.cv.Min.Y + (e.cv.Dy()-dh)/2
}

// ------------------------------------------------------------- desenho

func (e *editor) fill(dc uintptr, r image.Rectangle, br uintptr) {
	rr := rect{int32(r.Min.X), int32(r.Min.Y), int32(r.Max.X), int32(r.Max.Y)}
	callU(pFillRect, dc, uintptr(unsafe.Pointer(&rr)), br)
}

func (e *editor) label(dc uintptr, r image.Rectangle, s string, center bool) {
	u := utf16Of(s)
	if len(u) == 0 {
		return
	}
	tw, th := e.measure(s)
	x := r.Min.X + int(6*e.scale)
	if center {
		x = r.Min.X + (r.Dx()-tw)/2
	}
	y := r.Min.Y + (r.Dy()-th)/2
	callU(pTextOutW, dc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)))
}

func (e *editor) makeBack(hdc uintptr) {
	if e.backDC != 0 {
		callU(pSelectObject, e.backDC, e.backOld)
		callU(pDeleteDC, e.backDC)
		e.backDC = 0
	}
	if e.backBmp != 0 {
		callU(pDeleteObject, e.backBmp)
		e.backBmp = 0
	}
	e.backDC = callU(pCreateCompatibleDC, hdc)
	e.backBmp = callU(pCreateCompatibleBitmap, hdc, uintptr(e.cw), uintptr(e.ch))
	e.backOld = callU(pSelectObject, e.backDC, e.backBmp)
	e.bw, e.bh = e.cw, e.ch
}

func (e *editor) enabled(b button) bool {
	switch {
	case b.kind == bAction && b.id == actUndo:
		return len(e.ops) > 0 || e.typing
	case b.kind == bAction && b.id == actRedo:
		return len(e.redo) > 0
	}
	return true
}

func (e *editor) paint(hdc uintptr) {
	if e.cw <= 0 || e.ch <= 0 {
		return
	}
	if e.backDC == 0 || e.bw != e.cw || e.bh != e.ch {
		e.makeBack(hdc)
	}
	dc := e.backDC
	e.fill(dc, image.Rect(0, 0, e.cw, e.ch), e.brCanvas)

	// imagem
	if e.vs > 0 {
		mode := uintptr(stretchHalftone)
		if e.dragging || e.vs >= 1 {
			mode = stretchColorOnColor
		}
		callU(pSetStretchBltMode, dc, mode)
		bi := bitmapInfoHeader{
			Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(e.work.W), Height: -int32(e.work.H),
			Planes: 1, BitCount: 32,
		}
		dw, dh := int(float64(e.base.W)*e.vs), int(float64(e.base.H)*e.vs)
		callU(pStretchDIBits, dc, uintptr(e.ox), uintptr(e.oy), uintptr(dw), uintptr(dh),
			0, 0, uintptr(e.work.W), uintptr(e.work.H),
			uintptr(unsafe.Pointer(&e.work.Pix[0])), uintptr(unsafe.Pointer(&bi)), 0, srcCopy)
	}

	// barras
	e.fill(dc, image.Rect(0, 0, e.cw, e.tbH), e.brBg)
	e.fill(dc, image.Rect(0, e.ch-e.stH, e.cw, e.ch), e.brBg)

	oldFont := callU(pSelectObject, dc, e.uiFont)
	callU(pSetBkMode, dc, transparentMode)
	white, gray := rgb(255, 255, 255), rgb(125, 130, 142)
	inset := int(3 * e.scale)

	for _, b := range e.btns {
		on := e.enabled(b)
		callU(pSetTextColor, dc, white)
		switch b.kind {
		case bTool:
			br := e.brBtn
			if int(e.tl) == b.id {
				br = e.brSel
			}
			e.fill(dc, b.r, br)
			e.label(dc, b.r, b.label, true)
		case bColor:
			if e.colIdx == b.id {
				e.fill(dc, b.r, e.brWhite)
				e.fill(dc, b.r.Inset(inset), e.brSwatch[b.id])
			} else {
				e.fill(dc, b.r, e.brBtn)
				e.fill(dc, b.r.Inset(int(2*e.scale)), e.brSwatch[b.id])
			}
		case bSize:
			br := e.brBtn
			if e.sizeIdx == b.id {
				br = e.brSel
			}
			e.fill(dc, b.r, br)
			e.label(dc, b.r, b.label, true)
		case bAction:
			br := e.brBtn
			if b.id == actDone {
				br = e.brDone
			}
			e.fill(dc, b.r, br)
			if !on {
				callU(pSetTextColor, dc, gray)
			}
			e.label(dc, b.r, b.label, true)
		}
	}

	// barra de baixo
	callU(pSetTextColor, dc, rgb(190, 195, 205))
	st := image.Rect(0, e.ch-e.stH, e.cw, e.ch)
	e.label(dc, st, toolHints[e.tl], false)
	right := fmt.Sprintf("Ctrl+Z desfaz  •  Enter conclui  •  Esc descarta  •  %d×%d", e.base.W, e.base.H)
	if e.vs < 1 {
		right += fmt.Sprintf(" (%d%%)", int(e.vs*100))
	}
	tw, _ := e.measure(right)
	e.label(dc, image.Rect(e.cw-tw-int(18*e.scale), st.Min.Y, e.cw, st.Max.Y), right, false)

	callU(pSelectObject, dc, oldFont)
	callU(pBitBlt, hdc, 0, 0, uintptr(e.cw), uintptr(e.ch), dc, 0, 0, srcCopy)
}

func (e *editor) invalidate() {
	if e.hwnd != 0 { // com 0 o Windows redesenharia a tela inteira
		callU(pInvalidateRect, e.hwnd, 0, 0)
	}
}

// ------------------------------------------------------------- marcações

func (e *editor) color() [3]byte { return palette[e.colIdx] }

func (e *editor) toImage(x, y int) (int, int) {
	ix := int(math.Round(float64(x-e.ox) / e.vs))
	iy := int(math.Round(float64(y-e.oy) / e.vs))
	if ix < 0 {
		ix = 0
	}
	if iy < 0 {
		iy = 0
	}
	if ix > e.base.W {
		ix = e.base.W
	}
	if iy > e.base.H {
		iy = e.base.H
	}
	return ix, iy
}

func (e *editor) inCanvas(x, y int) bool { return image.Pt(x, y).In(e.cv) }

func shiftDown() bool { return callU(pGetKeyState, vkShift)&0x8000 != 0 }
func ctrlDown() bool  { return callU(pGetKeyState, vkControl)&0x8000 != 0 }

// drawPreview mostra a marcação em andamento sobre a imagem confirmada.
func (e *editor) drawPreview() {
	restoreRect(e.work, e.committed, e.prevB)
	e.prevB = image.Rectangle{}
	if e.cur != nil {
		o := *e.cur
		if e.typing {
			o.text += "|"
		}
		drawOp(e.work, o)
		e.prevB = opBounds(e.work, o)
	}
	e.invalidate()
}

// commitCur confirma a marcação em andamento (se for válida).
func (e *editor) commitCur() {
	o := e.cur
	e.cur = nil
	e.typing = false
	restoreRect(e.work, e.committed, e.prevB)
	e.prevB = image.Rectangle{}
	if o != nil && validOp(*o) {
		drawOp(e.committed, *o)
		drawOp(e.work, *o)
		e.ops = append(e.ops, *o)
		e.redo = nil
	}
	e.invalidate()
}

func (e *editor) undo() {
	if e.typing {
		e.cur = nil
		e.typing = false
		e.drawPreview()
		return
	}
	if len(e.ops) == 0 {
		return
	}
	e.redo = append(e.redo, e.ops[len(e.ops)-1])
	e.ops = e.ops[:len(e.ops)-1]
	e.rebuild()
}

func (e *editor) redoOp() {
	if len(e.redo) == 0 || e.typing {
		return
	}
	e.ops = append(e.ops, e.redo[len(e.redo)-1])
	e.redo = e.redo[:len(e.redo)-1]
	e.rebuild()
}

func (e *editor) rebuild() {
	e.committed = renderOps(e.base, e.ops)
	e.work = cloneBitmap(e.committed)
	e.prevB = image.Rectangle{}
	e.invalidate()
}

func (e *editor) selectTool(t tool) {
	if e.typing {
		e.commitCur()
	}
	e.tl = t
	e.invalidate()
}

func (e *editor) setColor(i int) {
	e.colIdx = i
	if e.typing && e.cur != nil {
		e.cur.col = e.color()
		e.drawPreview()
		return
	}
	e.invalidate()
}

func (e *editor) setSize(i int) {
	e.sizeIdx = i
	if e.typing && e.cur != nil {
		e.cur.px = fontFor(e.base, i)
		e.drawPreview()
		return
	}
	e.invalidate()
}

func (e *editor) countNumbers() int {
	n := 0
	for _, o := range e.ops {
		if o.kind == toolNumber {
			n++
		}
	}
	return n
}

// finish encerra o editor. accept=true entrega a imagem editada.
func (e *editor) finish(accept bool) {
	if e.typing {
		e.commitCur()
	}
	if !accept && (len(e.ops) > 0) {
		const mbYesNoQuestion = 0x24
		const idYes = 6
		r := callU(pMessageBoxW, e.hwnd,
			uintptr(unsafe.Pointer(utf16Ptr("Descartar as alterações e fechar o editor?\n\nA captura não será copiada nem salva."))),
			uintptr(unsafe.Pointer(utf16Ptr("SnowShot"))), mbYesNoQuestion)
		if r != idYes {
			return
		}
	}
	e.accepted = accept
	e.done = true
	callU(pReleaseCapture)
	callU(pDestroyWindow, e.hwnd)
	e.hwnd = 0
}

func (e *editor) action(id int) {
	switch id {
	case actUndo:
		e.undo()
	case actRedo:
		e.redoOp()
	case actDiscard:
		e.finish(false)
	case actDone:
		e.finish(true)
	}
}

// ------------------------------------------------------------- mouse e teclado

func (e *editor) mouseDown(x, y int) {
	for _, b := range e.btns {
		if !image.Pt(x, y).In(b.r) {
			continue
		}
		switch b.kind {
		case bTool:
			e.selectTool(tool(b.id))
		case bColor:
			e.setColor(b.id)
		case bSize:
			e.setSize(b.id)
		case bAction:
			if e.enabled(b) {
				e.action(b.id)
			}
		}
		return
	}
	if !e.inCanvas(x, y) {
		return
	}
	if e.typing {
		e.commitCur() // clicar fora termina o texto atual
	}
	ix, iy := e.toImage(x, y)
	col := e.color()
	switch e.tl {
	case toolText:
		e.typing = true
		e.cur = &op{kind: toolText, x1: ix, y1: iy, px: fontFor(e.base, e.sizeIdx), col: col}
		e.drawPreview()
	case toolNumber:
		e.cur = &op{kind: toolNumber, x1: ix, y1: iy, px: fontFor(e.base, e.sizeIdx), col: col, n: e.countNumbers() + 1}
		e.commitCur()
	default:
		e.dragging = true
		e.cur = &op{kind: e.tl, x1: ix, y1: iy, x2: ix, y2: iy, col: col, w: strokeFor(e.base, e.sizeIdx)}
		callU(pSetCapture, e.hwnd)
		e.drawPreview()
	}
}

func (e *editor) mouseMove(x, y int) {
	if !e.dragging || e.cur == nil {
		return
	}
	e.cur.x2, e.cur.y2 = e.toImage(x, y)
	if shiftDown() {
		constrain(e.cur)
	}
	e.drawPreview()
}

func (e *editor) mouseUp(x, y int) {
	if !e.dragging {
		return
	}
	e.mouseMove(x, y)
	e.dragging = false
	callU(pReleaseCapture)
	e.commitCur()
}

func (e *editor) typeRune(r rune) {
	if e.cur == nil {
		return
	}
	e.cur.text += string(r)
	e.drawPreview()
}

func (e *editor) onChar(c uint16) {
	if e.typing {
		switch {
		case c == vkBack:
			if rs := []rune(e.cur.text); len(rs) > 0 {
				e.cur.text = string(rs[:len(rs)-1])
				e.drawPreview()
			}
		case c == vkReturn:
			e.typeRune('\n')
		case c >= 0xD800 && c <= 0xDBFF:
			e.pendingHigh = c
		case c >= 0xDC00 && c <= 0xDFFF:
			if e.pendingHigh != 0 {
				e.typeRune(rune(e.pendingHigh-0xD800)<<10 + rune(c-0xDC00) + 0x10000)
				e.pendingHigh = 0
			}
		case c >= 32:
			e.typeRune(rune(c))
		}
		return
	}
	switch c {
	case 'a', 'A':
		e.selectTool(toolArrow)
	case 'r', 'R':
		e.selectTool(toolRect)
	case 'h', 'H':
		e.selectTool(toolHighlight)
	case 'b', 'B':
		e.selectTool(toolPixelate)
	case 't', 'T':
		e.selectTool(toolText)
	case 'n', 'N':
		e.selectTool(toolNumber)
	case '1', '2', '3':
		e.setSize(int(c - '1'))
	}
}

func (e *editor) onKeyDown(vk uintptr) {
	switch vk {
	case vkEscape:
		if e.typing {
			e.commitCur()
		} else {
			e.finish(false)
		}
	case vkReturn:
		if ctrlDown() || !e.typing {
			e.finish(true)
		}
	case vkKeyZ:
		if ctrlDown() {
			if shiftDown() {
				e.redoOp()
			} else {
				e.undo()
			}
		}
	case vkKeyY:
		if ctrlDown() {
			e.redoOp()
		}
	}
}

func editorProc(hwnd, m, wp, lp uintptr) uintptr {
	e := activeEditor
	if e == nil || (e.hwnd != 0 && hwnd != e.hwnd) {
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
			e.paint(hdc)
		}
		callU(pEndPaint, hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmSize:
		e.relayout()
		e.invalidate()
		return 0
	case wmGetMinMaxInfo:
		mm := (*[10]int32)(ptr(lp))
		mm[6] = int32(e.minW + int(16*e.scale)) // largura mínima da janela (com a moldura)
		mm[7] = int32(320 * e.scale)
		return 0
	case wmSetCursor:
		if lp&0xFFFF == 1 { // HTCLIENT
			var pt point
			callU(pGetCursorPos, uintptr(unsafe.Pointer(&pt)))
			callU(pScreenToClient, hwnd, uintptr(unsafe.Pointer(&pt)))
			cur := e.curArrow
			if e.inCanvas(int(pt.X), int(pt.Y)) {
				cur = e.curCross
				if e.tl == toolText {
					cur = e.curIBeam
				}
			}
			callU(pSetCursor, cur)
			return 1
		}
	case wmLButtonDown:
		e.mouseDown(x, y)
		return 0
	case wmMouseMove:
		e.mouseMove(x, y)
		return 0
	case wmLButtonUp:
		e.mouseUp(x, y)
		return 0
	case wmChar:
		e.onChar(uint16(wp))
		return 0
	case wmKeyDown:
		e.onKeyDown(wp)
		return 0
	case wmClose:
		e.finish(false)
		return 0
	case wmDestroy:
		e.done = true
		return 0
	}
	return callU(pDefWindowProcW, hwnd, m, wp, lp)
}
