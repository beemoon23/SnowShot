package main

import (
	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

// setupTray cria o ícone da bandeja (perto do relógio) com o menu de captura.
func (a *SnowApp) setupTray(icon *walk.Icon) {
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return
	}
	_ = ni.SetIcon(icon)
	_ = ni.SetToolTip(appTitle)

	add := func(text string, f func()) {
		act := walk.NewAction()
		_ = act.SetText(text)
		act.Triggered().Attach(f)
		_ = ni.ContextMenu().Actions().Add(act)
	}
	add("Capturar área", func() { go a.capture(kindArea, false) })
	add("Capturar tela inteira", func() { go a.capture(kindFull, false) })
	add("Capturar janela ativa", func() { go a.capture(kindWindow, false) })
	_ = ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	add("Abrir pasta das capturas", func() { openFolder(a.currentOutDir()) })
	add("Abrir o SnowShot", a.restoreFromTray)
	_ = ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	add("Sair", func() {
		a.quitting = true
		_ = a.mw.Close()
	})

	// Clique com o botão esquerdo no ícone: traz a janela de volta.
	ni.MouseDown().Attach(func(x, y int, b walk.MouseButton) {
		if b == walk.LeftButton {
			a.restoreFromTray()
		}
	})

	_ = ni.SetVisible(true)
	a.ni = ni
}

// restoreFromTray mostra a janela de novo.
func (a *SnowApp) restoreFromTray() {
	hwnd := a.mw.Handle()
	a.mw.SetVisible(true)
	win.ShowWindow(hwnd, win.SW_RESTORE)
	win.SetForegroundWindow(hwnd)
}
