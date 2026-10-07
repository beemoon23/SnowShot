package main

import (
	"github.com/tailscale/walk"
)

// loadAppIcon carrega o ícone embutido no .exe (a ferramenta rsrc o grava
// com o número 2 quando o manifesto vem primeiro). Se não achar, usa o
// ícone padrão do Windows.
func loadAppIcon() *walk.Icon {
	for _, id := range []int{2, 7} {
		if ic, err := walk.NewIconFromResourceId(id); err == nil && ic != nil {
			return ic
		}
	}
	return walk.IconApplication()
}

// notify mostra um balão do Windows na bandeja do sistema.
func (a *SnowApp) notify(title, msg string, isErr bool) {
	a.ui(func() {
		if a.ni == nil {
			return
		}
		if isErr {
			_ = a.ni.ShowError(title, msg)
		} else {
			_ = a.ni.ShowInfo(title, msg)
		}
	})
}

// shorten corta textos longos para caberem em balões e na barra de status.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
