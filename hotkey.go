package main

import (
	"runtime"
	"strings"
	"unsafe"
)

// Atalhos globais: funcionam mesmo com o SnowShot minimizado na bandeja.
// Ficam numa thread própria, porque o Windows entrega o aviso de "tecla
// apertada" para a thread que registrou o atalho.

type hotkey struct {
	Name string
	Mods uint32
	VK   uint32
}

const vkSnapshot = 0x2C // PrintScreen

// A mesma lista aparece nos três seletores de atalho.
var hotkeyPresets = []hotkey{
	{"PrintScreen", 0, vkSnapshot},
	{"Ctrl + PrintScreen", modControl, vkSnapshot},
	{"Shift + PrintScreen", modShift, vkSnapshot},
	{"Ctrl + Shift + S", modControl | modShift, 'S'},
	{"Ctrl + Shift + A", modControl | modShift, 'A'},
	{"Ctrl + Shift + F", modControl | modShift, 'F'},
	{"Ctrl + Shift + W", modControl | modShift, 'W'},
	{"Ctrl + Alt + S", modControl | modAlt, 'S'},
	{"Ctrl + Alt + A", modControl | modAlt, 'A'},
	{"Ctrl + Alt + F", modControl | modAlt, 'F'},
	{"Ctrl + Alt + W", modControl | modAlt, 'W'},
	{"Desativado", 0, 0},
}

func hotkeyNames() []string {
	out := make([]string, len(hotkeyPresets))
	for i, h := range hotkeyPresets {
		out[i] = h.Name
	}
	return out
}

type captureKind int

const (
	kindArea captureKind = iota + 1
	kindFull
	kindWindow
)

func (k captureKind) label() string {
	switch k {
	case kindArea:
		return "Área"
	case kindFull:
		return "Tela inteira"
	default:
		return "Janela ativa"
	}
}

const wmReloadHotkeys = wmApp + 1

// startHotkeys sobe a thread dos atalhos.
func (a *SnowApp) startHotkeys() {
	a.hkReady = make(chan struct{})
	go func() {
		runtime.LockOSThread()
		a.hkTID = uint32(callU(pGetCurrentThreadId))

		// Força o Windows a criar a fila de mensagens desta thread.
		var m msg
		callU(pPeekMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0)
		close(a.hkReady)

		a.registerHotkeys()

		for {
			r := callU(pGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			switch m.Message {
			case wmHotkey:
				if k := captureKind(m.WParam); k >= kindArea && k <= kindWindow {
					go a.capture(k, false)
				}
			case wmReloadHotkeys:
				a.registerHotkeys()
			default:
				callU(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
				callU(pDispatchMessageW, uintptr(unsafe.Pointer(&m)))
			}
		}
		for k := kindArea; k <= kindWindow; k++ {
			callU(pUnregisterHotKey, 0, uintptr(k))
		}
	}()
}

// reloadHotkeys pede à thread dos atalhos para registrar tudo de novo
// (usado quando o usuário troca um atalho na tela).
func (a *SnowApp) reloadHotkeys() {
	if a.hkReady == nil {
		return
	}
	<-a.hkReady
	callU(pPostThreadMessageW, uintptr(a.hkTID), wmReloadHotkeys, 0, 0)
}

func (a *SnowApp) stopHotkeys() {
	if a.hkReady == nil {
		return
	}
	<-a.hkReady
	callU(pPostThreadMessageW, uintptr(a.hkTID), wmQuit, 0, 0)
}

// registerHotkeys roda na thread dos atalhos.
func (a *SnowApp) registerHotkeys() {
	a.hkMu.Lock()
	sel := a.hkSel
	a.hkMu.Unlock()

	var failed []string
	for i, k := range []captureKind{kindArea, kindFull, kindWindow} {
		callU(pUnregisterHotKey, 0, uintptr(k))
		hk := hotkeyPresets[clamp(sel[i], len(hotkeyPresets))]
		if hk.VK == 0 {
			continue
		}
		r := callU(pRegisterHotKey, 0, uintptr(k), uintptr(hk.Mods|modNoRepeat), uintptr(hk.VK))
		if r == 0 {
			failed = append(failed, k.label()+" ("+hk.Name+")")
		}
	}

	if len(failed) > 0 {
		a.setStatus("Atalho indisponível, outro programa já usa: " + strings.Join(failed, ", ") + ". Escolha outro.")
	} else {
		a.setStatus("Pronto. Use os atalhos ou os botões acima.")
	}
}
