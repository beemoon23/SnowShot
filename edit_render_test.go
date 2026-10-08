package main

import (
	"image"
	"testing"
)

func solid(w, h int, v byte) *bitmap {
	b := &bitmap{W: w, H: h, Pix: make([]byte, w*h*4)}
	for i := range b.Pix {
		b.Pix[i] = v
	}
	return b
}

func px(b *bitmap, x, y int) [3]byte { // RGB
	i := (y*b.W + x) * 4
	return [3]byte{b.Pix[i+2], b.Pix[i+1], b.Pix[i]}
}

func changed(a, b *bitmap) image.Rectangle {
	var r image.Rectangle
	for y := 0; y < a.H; y++ {
		for x := 0; x < a.W; x++ {
			i := (y*a.W + x) * 4
			if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

var red = [3]byte{255, 0, 0}

func TestArrowDrawsAndKeepsBase(t *testing.T) {
	base := solid(200, 100, 255)
	out := renderOps(base, []op{{kind: toolArrow, x1: 20, y1: 50, x2: 160, y2: 50, col: red, w: 4}})
	if px(out, 100, 50) != red {
		t.Fatalf("haste não pintou: %v", px(out, 100, 50))
	}
	if px(out, 150, 50) != red {
		t.Fatalf("ponta não pintou: %v", px(out, 158, 50))
	}
	if px(out, 100, 80) != [3]byte{255, 255, 255} {
		t.Fatal("pintou fora da seta")
	}
	if px(base, 100, 50) != [3]byte{255, 255, 255} {
		t.Fatal("alterou a imagem original")
	}
}

func TestRectOutlineIsHollow(t *testing.T) {
	out := renderOps(solid(100, 100, 255), []op{{kind: toolRect, x1: 20, y1: 20, x2: 80, y2: 70, col: red, w: 4}})
	if px(out, 50, 20) != red || px(out, 20, 40) != red || px(out, 80, 40) != red || px(out, 50, 70) != red {
		t.Fatal("bordas incompletas")
	}
	if px(out, 50, 45) != [3]byte{255, 255, 255} {
		t.Fatal("o retângulo deveria ser vazado")
	}
	if px(out, 19, 19) != red && px(out, 18, 18) != red {
		t.Fatal("canto aberto")
	}
}

func TestHighlightKeepsDarkText(t *testing.T) {
	b := solid(50, 50, 255)
	i := (10*50 + 10) * 4
	b.Pix[i], b.Pix[i+1], b.Pix[i+2] = 0, 0, 0 // pixel preto
	out := renderOps(b, []op{{kind: toolHighlight, x1: 0, y1: 0, x2: 50, y2: 50, col: [3]byte{255, 204, 0}}})
	if px(out, 10, 10) != [3]byte{0, 0, 0} {
		t.Fatal("o destaque clareou o texto escuro")
	}
	if p := px(out, 30, 30); p[0] != 255 || p[2] >= 255 {
		t.Fatalf("fundo branco deveria ficar amarelado: %v", p)
	}
}

func TestPixelateMakesBlocks(t *testing.T) {
	b := solid(160, 160, 0)
	for i := 0; i < len(b.Pix); i += 4 { // degradê horizontal
		x := (i / 4) % b.W
		b.Pix[i], b.Pix[i+1], b.Pix[i+2] = byte(x), byte(x), byte(x)
	}
	out := renderOps(b, []op{{kind: toolPixelate, x1: 16, y1: 16, x2: 96, y2: 96}})
	if px(out, 17, 17) != px(out, 20, 20) {
		t.Fatal("pixels do mesmo bloco deveriam ser iguais")
	}
	if px(out, 5, 5) != px(b, 5, 5) || px(out, 120, 120) != px(b, 120, 120) {
		t.Fatal("alterou fora da área")
	}
}

func TestOpBoundsCoversEverything(t *testing.T) {
	ops := []op{
		{kind: toolArrow, x1: 30, y1: 30, x2: 150, y2: 90, col: red, w: 5},
		{kind: toolArrow, x1: 150, y1: 140, x2: 40, y2: 20, col: red, w: 9},
		{kind: toolRect, x1: 40, y1: 40, x2: 120, y2: 100, col: red, w: 6},
		{kind: toolHighlight, x1: 10, y1: 10, x2: 70, y2: 40, col: red},
		{kind: toolPixelate, x1: 10, y1: 10, x2: 70, y2: 40},
		{kind: toolText, x1: 20, y1: 20, px: 16, text: "oi\nmundo", col: red},
		{kind: toolNumber, x1: 100, y1: 100, px: 18, n: 12, col: red},
	}
	for _, o := range ops {
		base := solid(200, 160, 200)
		out := cloneBitmap(base)
		drawOp(out, o)
		ch := changed(base, out)
		if !ch.Empty() && !ch.In(opBounds(base, o)) {
			t.Fatalf("op %d mexeu fora de opBounds: mudou %v, bounds %v", o.kind, ch, opBounds(base, o))
		}
	}
}

func TestPreviewRestoreMatchesFullRender(t *testing.T) {
	base := solid(200, 160, 220)
	committed := renderOps(base, []op{{kind: toolRect, x1: 10, y1: 10, x2: 60, y2: 60, col: red, w: 3}})
	work := cloneBitmap(committed)
	prev := op{kind: toolArrow, x1: 20, y1: 20, x2: 150, y2: 120, col: red, w: 5}
	drawOp(work, prev)
	restoreRect(work, committed, opBounds(work, prev))
	if !changed(work, committed).Empty() {
		t.Fatalf("restaurar a área da prévia deveria voltar ao estado confirmado: %v", changed(work, committed))
	}
}

func TestTextAndNumber(t *testing.T) {
	out := renderOps(solid(200, 100, 255), []op{
		{kind: toolText, x1: 10, y1: 10, px: 20, text: "abc", col: red},
		{kind: toolNumber, x1: 150, y1: 50, px: 20, n: 3, col: red},
	})
	if px(out, 12, 12) != red {
		t.Fatal("texto não apareceu")
	}
	if px(out, 150-14, 50) != red {
		t.Fatal("bolinha não apareceu")
	}
}

func TestSizes(t *testing.T) {
	b := solid(1920, 1080, 0)
	if !(strokeFor(b, 0) < strokeFor(b, 1) && strokeFor(b, 1) < strokeFor(b, 2)) {
		t.Fatal("espessuras fora de ordem")
	}
	if fontFor(solid(100, 100, 0), 0) < 14 {
		t.Fatal("fonte mínima")
	}
}

func TestValidOp(t *testing.T) {
	if validOp(op{kind: toolArrow, x1: 0, y1: 0, x2: 2, y2: 1}) {
		t.Fatal("seta minúscula não deveria valer")
	}
	if !validOp(op{kind: toolArrow, x1: 0, y1: 0, x2: 30, y2: 0}) {
		t.Fatal("seta normal deveria valer")
	}
	if validOp(op{kind: toolRect, x1: 5, y1: 5, x2: 6, y2: 40}) {
		t.Fatal("retângulo fino demais")
	}
	if validOp(op{kind: toolText, text: "  \n "}) || !validOp(op{kind: toolText, text: "oi"}) {
		t.Fatal("texto vazio/não vazio")
	}
}

func TestConstrain(t *testing.T) {
	a := op{kind: toolArrow, x1: 10, y1: 10, x2: 110, y2: 22}
	constrain(&a)
	if a.y2 != 10 || a.x2 < 105 {
		t.Fatalf("seta deveria ficar horizontal: %+v", a)
	}
	r := op{kind: toolRect, x1: 10, y1: 10, x2: 60, y2: -20}
	constrain(&r)
	if abs(r.x2-r.x1) != abs(r.y2-r.y1) || r.y2 >= r.y1 {
		t.Fatalf("quadrado com sinais preservados: %+v", r)
	}
}
