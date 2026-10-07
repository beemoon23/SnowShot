package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"
)

// bitmap guarda uma imagem em BGRA (o formato do Windows), de cima para
// baixo, com alfa sempre 255.
type bitmap struct {
	W, H int
	Pix  []byte
}

// shot é a captura da área virtual inteira (todos os monitores juntos).
// X e Y são a posição do canto dela na "tela virtual" do Windows
// (pode ser negativa quando há monitor à esquerda/acima do principal).
type shot struct {
	bitmap
	X, Y int
}

// captureVirtual tira uma foto de todos os monitores de uma vez.
func captureVirtual() (*shot, error) {
	x, y := metric(smXVirtualScreen), metric(smYVirtualScreen)
	w, h := metric(smCXVirtualScreen), metric(smCYVirtualScreen)
	if w <= 0 || h <= 0 {
		return nil, errors.New("não consegui descobrir o tamanho da tela")
	}

	hdcScr := callU(pGetDC, 0)
	if hdcScr == 0 {
		return nil, errors.New("não consegui acessar a tela")
	}
	defer callU(pReleaseDC, 0, hdcScr)

	memDC := callU(pCreateCompatibleDC, hdcScr)
	if memDC == 0 {
		return nil, errors.New("falha ao preparar a captura")
	}
	defer callU(pDeleteDC, memDC)

	hbm, bits := newDIBSection(hdcScr, w, h)
	if hbm == 0 || bits == nil {
		return nil, errors.New("sem memória para a captura")
	}
	defer callU(pDeleteObject, hbm)

	old := callU(pSelectObject, memDC, hbm)
	defer callU(pSelectObject, memDC, old)

	ok := callU(pBitBlt, memDC, 0, 0, uintptr(w), uintptr(h),
		hdcScr, uintptr(x), uintptr(y), srcCopy|captureBlt)
	if ok == 0 {
		return nil, errors.New("o Windows recusou a captura da tela")
	}

	n := w * h * 4
	pix := make([]byte, n)
	copy(pix, unsafe.Slice((*byte)(bits), n))
	for i := 3; i < n; i += 4 {
		pix[i] = 255 // o GDI deixa o alfa em 0
	}
	return &shot{bitmap: bitmap{W: w, H: h, Pix: pix}, X: x, Y: y}, nil
}

// cropScreen recorta um retângulo dado em coordenadas da tela virtual.
func (s *shot) cropScreen(r image.Rectangle) (*bitmap, error) {
	local := r.Sub(image.Pt(s.X, s.Y)).Intersect(image.Rect(0, 0, s.W, s.H))
	if local.Dx() < 2 || local.Dy() < 2 {
		return nil, errors.New("a área escolhida ficou fora da tela")
	}
	return s.crop(local), nil
}

// crop recorta um retângulo dado em coordenadas da própria imagem.
func (b *bitmap) crop(r image.Rectangle) *bitmap {
	w, h := r.Dx(), r.Dy()
	out := &bitmap{W: w, H: h, Pix: make([]byte, w*h*4)}
	for row := 0; row < h; row++ {
		src := ((r.Min.Y+row)*b.W + r.Min.X) * 4
		copy(out.Pix[row*w*4:(row+1)*w*4], b.Pix[src:src+w*4])
	}
	return out
}

// toRGBA converte para o formato que o pacote image do Go entende.
func (b *bitmap) toRGBA() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, b.W, b.H))
	for i := 0; i+3 < len(b.Pix); i += 4 {
		img.Pix[i] = b.Pix[i+2]
		img.Pix[i+1] = b.Pix[i+1]
		img.Pix[i+2] = b.Pix[i]
		img.Pix[i+3] = 255
	}
	return img
}

func bitmapFromImage(src image.Image) *bitmap {
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	b := &bitmap{W: rgba.Rect.Dx(), H: rgba.Rect.Dy(), Pix: make([]byte, len(rgba.Pix))}
	for i := 0; i+3 < len(rgba.Pix); i += 4 {
		b.Pix[i] = rgba.Pix[i+2]
		b.Pix[i+1] = rgba.Pix[i+1]
		b.Pix[i+2] = rgba.Pix[i]
		b.Pix[i+3] = 255
	}
	return b
}

func (b *bitmap) encodePNG() ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, b.toRGBA()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dib monta o formato "CF_DIB" da área de transferência (cabeçalho + pixels
// de baixo para cima).
func (b *bitmap) dib() []byte {
	hdr := bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     int32(b.W),
		Height:    int32(b.H), // positivo = de baixo para cima
		Planes:    1,
		BitCount:  32,
		SizeImage: uint32(b.W * b.H * 4),
	}
	var buf bytes.Buffer
	buf.Grow(int(hdr.Size) + len(b.Pix))
	_ = binary.Write(&buf, binary.LittleEndian, hdr)
	stride := b.W * 4
	for row := b.H - 1; row >= 0; row-- {
		buf.Write(b.Pix[row*stride : (row+1)*stride])
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------- alvos

// monitorUnderCursor devolve o retângulo (em coordenadas da tela virtual)
// do monitor onde está o mouse.
func monitorUnderCursor() (image.Rectangle, bool) {
	var pt point
	if callU(pGetCursorPos, uintptr(unsafe.Pointer(&pt))) == 0 {
		return image.Rectangle{}, false
	}
	// Em 64 bits o POINT inteiro viaja num único registrador (y no alto, x embaixo).
	packed := uintptr(uint64(uint32(pt.Y))<<32 | uint64(uint32(pt.X)))
	hmon := callU(pMonitorFromPoint, packed, 2) // 2 = o mais próximo
	if hmon == 0 {
		return image.Rectangle{}, false
	}
	var mi monitorInfo
	mi.Size = uint32(unsafe.Sizeof(mi))
	if callU(pGetMonitorInfoW, hmon, uintptr(unsafe.Pointer(&mi))) == 0 {
		return image.Rectangle{}, false
	}
	return image.Rect(int(mi.Monitor.Left), int(mi.Monitor.Top), int(mi.Monitor.Right), int(mi.Monitor.Bottom)), true
}

// foregroundWindowRect devolve a área da janela ativa (sem a sombra invisível
// que o Windows 10/11 coloca em volta).
func foregroundWindowRect() (image.Rectangle, bool) {
	hwnd := callU(pGetForegroundWindow)
	if hwnd == 0 {
		return image.Rectangle{}, false
	}
	var r rect
	const dwmwaExtendedFrameBounds = 9
	hr := callU(pDwmGetWindowAttribute, hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))
	if hr != 0 {
		if callU(pGetWindowRect, hwnd, uintptr(unsafe.Pointer(&r))) == 0 {
			return image.Rectangle{}, false
		}
	}
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)), true
}

// ---------------------------------------------------- área de transferência

var pngClipFormat uintptr

func setClipData(format uintptr, data []byte) error {
	if len(data) == 0 {
		return errors.New("dados vazios")
	}
	const gmemMoveable = 0x0002
	h := callU(pGlobalAlloc, gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return errors.New("sem memória")
	}
	p := callU(pGlobalLock, h)
	if p == 0 {
		callU(pGlobalFree, h)
		return errors.New("falha ao travar a memória")
	}
	copy(unsafe.Slice((*byte)(ptr(p)), len(data)), data)
	callU(pGlobalUnlock, h)
	if callU(pSetClipboardData, format, h) == 0 {
		callU(pGlobalFree, h) // se o Windows não aceitou, a memória ainda é nossa
		return errors.New("o Windows recusou os dados")
	}
	return nil
}

// copyToClipboard põe a imagem na área de transferência em dois formatos:
// o clássico (serve para Paint, Word, Discord...) e o PNG (apps modernos).
func copyToClipboard(b *bitmap, pngData []byte) error {
	opened := false
	for i := 0; i < 15; i++ {
		if callU(pOpenClipboard, 0) != 0 {
			opened = true
			break
		}
		time.Sleep(25 * time.Millisecond) // outro programa está usando; tenta de novo
	}
	if !opened {
		return errors.New("a área de transferência está ocupada por outro programa")
	}
	defer callU(pCloseClipboard)

	callU(pEmptyClipboard)
	if err := setClipData(cfDIB, b.dib()); err != nil {
		return err
	}
	if pngClipFormat == 0 {
		pngClipFormat = callU(pRegisterClipboardFormatW, uintptr(unsafe.Pointer(utf16Ptr("PNG"))))
	}
	if pngClipFormat != 0 && len(pngData) > 0 {
		_ = setClipData(pngClipFormat, pngData) // o PNG é um extra: se falhar, o resto vale
	}
	return nil
}

// ----------------------------------------------------------------- arquivos

func newShotPath(dir string) string {
	base := "SnowShot " + time.Now().Format("2006-01-02 15-04-05")
	cand := filepath.Join(dir, base+".png")
	for i := 2; i < 1000; i++ {
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			break
		}
		cand = filepath.Join(dir, fmt.Sprintf("%s (%d).png", base, i))
	}
	return cand
}

func saveShot(dir string, pngData []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("não consegui criar a pasta: %w", err)
	}
	p := newShotPath(dir)
	if err := os.WriteFile(p, pngData, 0o644); err != nil {
		return "", fmt.Errorf("não consegui salvar: %w", err)
	}
	return p, nil
}

// listRecent devolve as capturas mais novas da pasta (mais recente primeiro).
func listRecent(dir string, max int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type item struct {
		path string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "SnowShot ") || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	if len(items) > max {
		items = items[:max]
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.path
	}
	return out
}

func loadBitmapFile(path string) (*bitmap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	return bitmapFromImage(img), nil
}
