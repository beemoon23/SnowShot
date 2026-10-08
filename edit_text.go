package main

import (
	"strings"
	"sync"
	"unsafe"
)

// Texto do editor: desenhamos com as fontes do Windows numa imagem preta
// (letras brancas, suavizadas) e usamos o brilho de cada pixel como
// "cobertura" da letra. Assim o texto sai nítido em qualquer cor.

const (
	fwBold             = 700
	defaultCharset     = 1
	antialiasedQuality = 4
)

type textKey struct {
	s  string
	px int
}

type textVal struct {
	cov  []byte
	w, h int
}

var (
	textCacheMu sync.Mutex
	textCache   = map[textKey]textVal{}
)

type sizeStruct struct{ CX, CY int32 }

// makeFont cria uma fonte Segoe UI com a altura (em pixels) pedida.
func makeFont(px int, bold bool) uintptr {
	weight := 400
	if bold {
		weight = fwBold
	}
	return callU(pCreateFontW, uintptr(-px), 0, 0, 0, uintptr(weight), 0, 0, 0,
		defaultCharset, 0, 0, antialiasedQuality, 0,
		uintptr(unsafe.Pointer(utf16Ptr("Segoe UI"))))
}

func init() { maskText = gdiMask }

// gdiMask desenha o texto (várias linhas se tiver \n) e devolve a cobertura.
func gdiMask(text string, px int) ([]byte, int, int) {
	key := textKey{text, px}
	textCacheMu.Lock()
	if v, ok := textCache[key]; ok {
		textCacheMu.Unlock()
		return v.cov, v.w, v.h
	}
	textCacheMu.Unlock()

	lines := strings.Split(text, "\n")
	hdcScr := callU(pGetDC, 0)
	if hdcScr == 0 {
		return nil, 0, 0
	}
	defer callU(pReleaseDC, 0, hdcScr)
	dc := callU(pCreateCompatibleDC, hdcScr)
	if dc == 0 {
		return nil, 0, 0
	}
	defer callU(pDeleteDC, dc)
	font := makeFont(px, true)
	if font == 0 {
		return nil, 0, 0
	}
	defer callU(pDeleteObject, font)
	oldFont := callU(pSelectObject, dc, font)
	defer callU(pSelectObject, dc, oldFont)

	// medidas
	widths := make([]int, len(lines))
	lineH := 0
	maxW := 0
	for i, l := range lines {
		u := utf16Of(l)
		var sz sizeStruct
		if len(u) > 0 {
			callU(pGetTextExtentPoint32W, dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)), uintptr(unsafe.Pointer(&sz)))
		} else {
			callU(pGetTextExtentPoint32W, dc, uintptr(unsafe.Pointer(utf16Ptr("Ag"))), 2, uintptr(unsafe.Pointer(&sz)))
			sz.CX = 0
		}
		widths[i] = int(sz.CX)
		if int(sz.CY) > lineH {
			lineH = int(sz.CY)
		}
		if widths[i] > maxW {
			maxW = widths[i]
		}
	}
	w, h := maxW+4, lineH*len(lines)+2
	if w <= 4 || h <= 2 || w > 8000 || h > 8000 {
		return nil, 0, 0
	}

	bmp, bits := newDIBSection(hdcScr, w, h)
	if bmp == 0 || bits == nil {
		return nil, 0, 0
	}
	defer callU(pDeleteObject, bmp)
	oldBmp := callU(pSelectObject, dc, bmp)
	defer callU(pSelectObject, dc, oldBmp)

	callU(pSetBkMode, dc, transparentMode)
	callU(pSetTextColor, dc, rgb(255, 255, 255))
	for i, l := range lines {
		u := utf16Of(l)
		if len(u) == 0 {
			continue
		}
		callU(pTextOutW, dc, 2, uintptr(1+i*lineH), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)))
	}
	callU(pGdiFlush)

	pix := unsafe.Slice((*byte)(bits), w*h*4)
	cov := make([]byte, w*h)
	for i := range cov {
		cov[i] = pix[i*4+1] // canal verde = brilho da letra
	}

	textCacheMu.Lock()
	if len(textCache) > 400 {
		textCache = map[textKey]textVal{}
	}
	textCache[key] = textVal{cov, w, h}
	textCacheMu.Unlock()
	return cov, w, h
}

// utf16Of converte para UTF-16 sem o zero final.
func utf16Of(s string) []uint16 {
	u := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			u = append(u, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			u = append(u, uint16(r))
		}
	}
	return u
}
