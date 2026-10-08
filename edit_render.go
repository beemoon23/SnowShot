package main

import (
	"image"
	"math"
	"strings"
)

// Motor de desenho do editor. É Go puro (sem chamadas ao Windows): recebe a
// imagem em BGRA e aplica as marcações (seta, retângulo, destaque, borrar,
// texto, número) por cima, com bordas suavizadas. O texto vem de uma função
// plugável (no Windows, usa as fontes do sistema; nos testes, é um bloco).

type tool int

const (
	toolArrow tool = iota
	toolRect
	toolHighlight
	toolPixelate
	toolText
	toolNumber
)

// op é uma marcação. As coordenadas são em pixels da imagem original.
type op struct {
	kind           tool
	x1, y1, x2, y2 int
	col            [3]byte // RGB
	w              int     // espessura do traço
	px             int     // altura da fonte (texto e número)
	text           string
	n              int // número (toolNumber)
}

// textMaskFn devolve a "cobertura" (0-255) do texto desenhado: w×h bytes.
type textMaskFn func(text string, px int) (cov []byte, w, h int)

var maskText textMaskFn = func(text string, px int) ([]byte, int, int) {
	// Padrão (testes): um bloco por caractere.
	lines := strings.Split(text, "\n")
	mw := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > mw {
			mw = n
		}
	}
	w, h := mw*px/2, len(lines)*px
	if w <= 0 || h <= 0 {
		return nil, 0, 0
	}
	cov := make([]byte, w*h)
	for i := range cov {
		cov[i] = 255
	}
	return cov, w, h
}

// Paleta do editor.
var palette = [][3]byte{
	{255, 59, 48},  // vermelho
	{255, 204, 0},  // amarelo
	{52, 199, 89},  // verde
	{10, 132, 255}, // azul
	{255, 255, 255},
	{20, 20, 20},
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// blendPx mistura a cor (RGB) sobre o pixel (x,y) com a cobertura cov (0-1).
func blendPx(b *bitmap, x, y int, c [3]byte, cov float64) {
	if cov <= 0 || x < 0 || y < 0 || x >= b.W || y >= b.H {
		return
	}
	if cov > 1 {
		cov = 1
	}
	i := (y*b.W + x) * 4
	b.Pix[i] = byte(float64(b.Pix[i]) + (float64(c[2])-float64(b.Pix[i]))*cov + 0.5)
	b.Pix[i+1] = byte(float64(b.Pix[i+1]) + (float64(c[1])-float64(b.Pix[i+1]))*cov + 0.5)
	b.Pix[i+2] = byte(float64(b.Pix[i+2]) + (float64(c[0])-float64(b.Pix[i+2]))*cov + 0.5)
	b.Pix[i+3] = 255
}

func clipRect(b *bitmap, r image.Rectangle) image.Rectangle {
	return r.Canon().Intersect(image.Rect(0, 0, b.W, b.H))
}

// drawLine desenha um segmento grosso com pontas arredondadas.
func drawLine(b *bitmap, x1, y1, x2, y2 float64, w float64, c [3]byte) {
	hw := w / 2
	pad := hw + 2
	r := clipRect(b, image.Rect(int(math.Floor(math.Min(x1, x2)-pad)), int(math.Floor(math.Min(y1, y2)-pad)),
		int(math.Ceil(math.Max(x1, x2)+pad)), int(math.Ceil(math.Max(y1, y2)+pad))))
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			t := 0.0
			if l2 > 0 {
				t = clampf(((px-x1)*dx+(py-y1)*dy)/l2, 0, 1)
			}
			d := math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
			blendPx(b, x, y, c, hw+0.5-d)
		}
	}
}

// fillTriangle preenche um triângulo com bordas suavizadas (4×4 amostras).
func fillTriangle(b *bitmap, ax, ay, bx, by, cx, cy float64, c [3]byte) {
	minx := math.Floor(math.Min(ax, math.Min(bx, cx)))
	maxx := math.Ceil(math.Max(ax, math.Max(bx, cx)))
	miny := math.Floor(math.Min(ay, math.Min(by, cy)))
	maxy := math.Ceil(math.Max(ay, math.Max(by, cy)))
	r := clipRect(b, image.Rect(int(minx), int(miny), int(maxx)+1, int(maxy)+1))
	sign := func(px, py, x1, y1, x2, y2 float64) float64 { return (px-x2)*(y1-y2) - (x1-x2)*(py-y2) }
	const n = 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			hit := 0
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					px := float64(x) + (float64(sx)+0.5)/n
					py := float64(y) + (float64(sy)+0.5)/n
					d1 := sign(px, py, ax, ay, bx, by)
					d2 := sign(px, py, bx, by, cx, cy)
					d3 := sign(px, py, cx, cy, ax, ay)
					neg := d1 < 0 || d2 < 0 || d3 < 0
					pos := d1 > 0 || d2 > 0 || d3 > 0
					if !(neg && pos) {
						hit++
					}
				}
			}
			if hit > 0 {
				blendPx(b, x, y, c, float64(hit)/(n*n))
			}
		}
	}
}

// arrowGeom calcula a haste e a ponta da seta (tail → tip).
func arrowGeom(o op) (bx, by, h1x, h1y, h2x, h2y float64, ok bool) {
	tx, ty, px, py := float64(o.x1), float64(o.y1), float64(o.x2), float64(o.y2)
	L := math.Hypot(px-tx, py-ty)
	if L < 3 {
		return 0, 0, 0, 0, 0, 0, false
	}
	ux, uy := (px-tx)/L, (py-ty)/L
	nx, ny := -uy, ux
	hl := math.Min(L*0.7, math.Max(16, 5*float64(o.w)))
	hh := hl * 0.48
	bx, by = px-ux*hl, py-uy*hl
	return bx + ux*hl*0.15, by + uy*hl*0.15, bx + nx*hh, by + ny*hh, bx - nx*hh, by - ny*hh, true
}

func drawArrow(b *bitmap, o op) {
	sx, sy, h1x, h1y, h2x, h2y, ok := arrowGeom(o)
	if !ok {
		return
	}
	drawLine(b, float64(o.x1), float64(o.y1), sx, sy, float64(o.w), o.col)
	fillTriangle(b, float64(o.x2), float64(o.y2), h1x, h1y, h2x, h2y, o.col)
}

func fillRectSolid(b *bitmap, r image.Rectangle, c [3]byte) {
	r = clipRect(b, r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			blendPx(b, x, y, c, 1)
		}
	}
}

func drawRectOutline(b *bitmap, o op) {
	r := image.Rect(o.x1, o.y1, o.x2, o.y2).Canon()
	w := o.w
	if w < 1 {
		w = 1
	}
	h := w / 2
	// quatro faixas; os cantos ficam quadrados e sem falhas
	fillRectSolid(b, image.Rect(r.Min.X-h, r.Min.Y-h, r.Max.X+w-h, r.Min.Y-h+w), o.col)
	fillRectSolid(b, image.Rect(r.Min.X-h, r.Max.Y-h, r.Max.X+w-h, r.Max.Y-h+w), o.col)
	fillRectSolid(b, image.Rect(r.Min.X-h, r.Min.Y-h, r.Min.X-h+w, r.Max.Y+w-h), o.col)
	fillRectSolid(b, image.Rect(r.Max.X-h, r.Min.Y-h, r.Max.X-h+w, r.Max.Y+w-h), o.col)
}

// drawHighlight multiplica a área por uma versão clara da cor: o texto
// escuro por baixo continua legível, como num marca-texto de verdade.
func drawHighlight(b *bitmap, o op) {
	r := clipRect(b, image.Rect(o.x1, o.y1, o.x2, o.y2))
	m := [3]float64{}
	for i := 0; i < 3; i++ {
		m[i] = (255 - (255-float64(o.col[i]))*0.55) / 255
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := (y*b.W + x) * 4
			b.Pix[i] = byte(float64(b.Pix[i]) * m[2])
			b.Pix[i+1] = byte(float64(b.Pix[i+1]) * m[1])
			b.Pix[i+2] = byte(float64(b.Pix[i+2]) * m[0])
		}
	}
}

// pixelBlock é o tamanho do mosaico, proporcional à imagem.
func pixelBlock(b *bitmap) int {
	m := b.W
	if b.H < m {
		m = b.H
	}
	n := m / 80
	if n < 8 {
		n = 8
	}
	return n
}

// drawPixelate cobre a área com um mosaico (esconde senhas, nomes, etc.).
func drawPixelate(b *bitmap, o op) {
	r := clipRect(b, image.Rect(o.x1, o.y1, o.x2, o.y2))
	if r.Empty() {
		return
	}
	bs := pixelBlock(b)
	for by := r.Min.Y; by < r.Max.Y; by += bs {
		for bx := r.Min.X; bx < r.Max.X; bx += bs {
			x2, y2 := bx+bs, by+bs
			if x2 > r.Max.X {
				x2 = r.Max.X
			}
			if y2 > r.Max.Y {
				y2 = r.Max.Y
			}
			var sb, sg, sr, n int
			for y := by; y < y2; y++ {
				for x := bx; x < x2; x++ {
					i := (y*b.W + x) * 4
					sb += int(b.Pix[i])
					sg += int(b.Pix[i+1])
					sr += int(b.Pix[i+2])
					n++
				}
			}
			if n == 0 {
				continue
			}
			vb, vg, vr := byte(sb/n), byte(sg/n), byte(sr/n)
			for y := by; y < y2; y++ {
				for x := bx; x < x2; x++ {
					i := (y*b.W + x) * 4
					b.Pix[i], b.Pix[i+1], b.Pix[i+2] = vb, vg, vr
				}
			}
		}
	}
}

func drawTextMask(b *bitmap, x0, y0 int, cov []byte, w, h int, c [3]byte) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if v := cov[y*w+x]; v > 0 {
				blendPx(b, x0+x, y0+y, c, float64(v)/255)
			}
		}
	}
}

func drawText(b *bitmap, o op) {
	if strings.TrimSpace(o.text) == "" {
		return
	}
	cov, w, h := maskText(o.text, o.px)
	if w > 0 && h > 0 {
		drawTextMask(b, o.x1, o.y1, cov, w, h, o.col)
	}
}

func luma(c [3]byte) float64 {
	return 0.299*float64(c[0]) + 0.587*float64(c[1]) + 0.114*float64(c[2])
}

func numberRadius(o op) float64 { return float64(o.px) * 0.8 }

// drawNumber desenha uma bolinha numerada (para listar passos).
func drawNumber(b *bitmap, o op) {
	r := numberRadius(o)
	cx, cy := float64(o.x1), float64(o.y1)
	rr := clipRect(b, image.Rect(int(cx-r-2), int(cy-r-2), int(cx+r+3), int(cy+r+3)))
	for y := rr.Min.Y; y < rr.Max.Y; y++ {
		for x := rr.Min.X; x < rr.Max.X; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			blendPx(b, x, y, o.col, r+0.5-d)
		}
	}
	txt := itoa(o.n)
	cov, w, h := maskText(txt, int(r*1.15))
	if w > 0 && h > 0 {
		tc := [3]byte{255, 255, 255}
		if luma(o.col) > 160 {
			tc = [3]byte{20, 20, 20}
		}
		drawTextMask(b, int(cx)-w/2, int(cy)-h/2, cov, w, h, tc)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// drawOp aplica uma marcação sobre a imagem.
func drawOp(b *bitmap, o op) {
	switch o.kind {
	case toolArrow:
		drawArrow(b, o)
	case toolRect:
		drawRectOutline(b, o)
	case toolHighlight:
		drawHighlight(b, o)
	case toolPixelate:
		drawPixelate(b, o)
	case toolText:
		drawText(b, o)
	case toolNumber:
		drawNumber(b, o)
	}
}

func cloneBitmap(b *bitmap) *bitmap {
	c := &bitmap{W: b.W, H: b.H, Pix: make([]byte, len(b.Pix))}
	copy(c.Pix, b.Pix)
	return c
}

// renderOps devolve uma cópia da imagem com todas as marcações aplicadas.
func renderOps(base *bitmap, ops []op) *bitmap {
	out := cloneBitmap(base)
	for _, o := range ops {
		drawOp(out, o)
	}
	return out
}

// opBounds é a área da imagem que a marcação pode alterar (com folga).
func opBounds(b *bitmap, o op) image.Rectangle {
	var r image.Rectangle
	switch o.kind {
	case toolArrow:
		pad := int(math.Max(18, 5*float64(o.w))) + 3
		r = image.Rect(o.x1, o.y1, o.x2, o.y2).Canon().Inset(-pad)
	case toolRect:
		r = image.Rect(o.x1, o.y1, o.x2, o.y2).Canon().Inset(-(o.w + 2))
	case toolHighlight, toolPixelate:
		r = image.Rect(o.x1, o.y1, o.x2, o.y2).Canon().Inset(-1)
	case toolText:
		_, w, h := maskText(o.text+"|", o.px)
		r = image.Rect(o.x1-2, o.y1-2, o.x1+w+4, o.y1+h+4)
	case toolNumber:
		rad := int(numberRadius(o)) + 3
		r = image.Rect(o.x1-rad, o.y1-rad, o.x1+rad, o.y1+rad)
	}
	return r.Intersect(image.Rect(0, 0, b.W, b.H))
}

// restoreRect copia de src para dst só a área r (para refazer a prévia).
func restoreRect(dst, src *bitmap, r image.Rectangle) {
	r = r.Intersect(image.Rect(0, 0, dst.W, dst.H))
	if r.Empty() {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		i := (y*dst.W + r.Min.X) * 4
		copy(dst.Pix[i:i+r.Dx()*4], src.Pix[i:i+r.Dx()*4])
	}
}

// strokeWidth e fontPx dependem do tamanho da imagem e da escolha P/M/G.
func baseStroke(b *bitmap) int {
	m := b.W
	if b.H < m {
		m = b.H
	}
	n := int(math.Round(float64(m) / 250))
	if n < 3 {
		n = 3
	}
	return n
}

var sizeMul = []float64{0.6, 1, 1.8}

func strokeFor(b *bitmap, size int) int {
	w := int(math.Round(float64(baseStroke(b)) * sizeMul[size]))
	if w < 2 {
		w = 2
	}
	return w
}

func fontFor(b *bitmap, size int) int {
	p := int(math.Round(float64(baseStroke(b)) * 6 * sizeMul[size]))
	if p < 14 {
		p = 14
	}
	return p
}

func validOp(o op) bool {
	switch o.kind {
	case toolArrow:
		return math.Hypot(float64(o.x2-o.x1), float64(o.y2-o.y1)) >= 6
	case toolRect, toolHighlight, toolPixelate:
		return abs(o.x2-o.x1) >= 4 && abs(o.y2-o.y1) >= 4
	case toolText:
		for _, r := range o.text {
			if r != ' ' && r != '\n' {
				return true
			}
		}
		return false
	}
	return true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// constrain aplica o Shift: ângulo reto (seta) ou quadrado.
func constrain(o *op) {
	dx, dy := o.x2-o.x1, o.y2-o.y1
	switch o.kind {
	case toolArrow:
		l := math.Hypot(float64(dx), float64(dy))
		a := math.Round(math.Atan2(float64(dy), float64(dx))/(math.Pi/4)) * (math.Pi / 4)
		o.x2 = o.x1 + int(math.Round(l*math.Cos(a)))
		o.y2 = o.y1 + int(math.Round(l*math.Sin(a)))
	case toolRect, toolHighlight, toolPixelate:
		s := abs(dx)
		if abs(dy) > s {
			s = abs(dy)
		}
		sx, sy := 1, 1
		if dx < 0 {
			sx = -1
		}
		if dy < 0 {
			sy = -1
		}
		o.x2, o.y2 = o.x1+sx*s, o.y1+sy*s
	}
}
