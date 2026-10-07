package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

const appTitle = "SnowShot"

// delayOptions são as opções do "Atraso" antes de capturar (bom para pegar menus).
var delayOptions = []struct {
	Name string
	Sec  int
}{
	{"Sem atraso", 0},
	{"3 segundos", 3},
	{"5 segundos", 5},
	{"10 segundos", 10},
}

// SnowApp junta janela, bandeja, atalhos e configurações.
type SnowApp struct {
	mw       *walk.MainWindow
	statusLB *walk.Label
	recentLB *walk.ListBox
	ni       *walk.NotifyIcon

	cfgMu    sync.Mutex
	settings AppSettings

	persistFn  func()
	trayMin    bool
	trayHinted bool
	quitting   bool

	capturing int32 // 1 enquanto uma captura está em andamento

	recent []string // capturas recentes (mais nova primeiro)

	// atalhos globais
	hkMu    sync.Mutex
	hkSel   [3]int
	hkReady chan struct{}
	hkTID   uint32
}

func init() {
	// A interface do Windows exige que tudo rode sempre na mesma thread.
	runtime.LockOSThread()
}

func main() {
	// walk.InitApp() TEM que ser a primeira chamada da biblioteca de janela:
	// é ela que registra a classe da janela principal. Sem isso o Windows
	// responde "CreateWindowEx" e a janela nunca abre.
	if _, err := walk.InitApp(); err != nil {
		walk.MsgBox(nil, appTitle, "Erro ao iniciar a interface:\n\n"+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}

	startTray := false
	for _, arg := range os.Args[1:] {
		if arg == "--tray" {
			startTray = true
		}
	}

	if !acquireSingleInstance() {
		walk.MsgBox(nil, appTitle,
			"O SnowShot já está aberto. Procure o ícone dele na bandeja, perto do relógio.",
			walk.MsgBoxIconInformation)
		return
	}

	cleanupOldExe()

	a := &SnowApp{settings: loadSettings()}
	a.trayMin = a.settings.TrayMin
	a.hkSel = [3]int{a.settings.HotArea, a.settings.HotFull, a.settings.HotWindow}

	if err := a.run(startTray); err != nil {
		walk.MsgBox(nil, appTitle, "Erro ao abrir a janela:\n\n"+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}
}

// acquireSingleInstance impede duas cópias abertas (os atalhos brigariam).
// Espera um pouco, porque numa atualização a cópia nova abre enquanto a
// antiga ainda está terminando de fechar.
func acquireSingleInstance() bool {
	name := utf16Ptr(`Local\SnowShot-instance`)
	for i := 0; i < 30; i++ {
		r, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
		if r == 0 {
			return true // não deu para criar a trava; segue sem ela
		}
		if errno, ok := err.(syscall.Errno); ok && errno == 183 { // ERROR_ALREADY_EXISTS
			// Solta o handle que acabamos de pegar: se ficasse aberto, a trava
			// continuaria existindo mesmo depois da cópia antiga fechar.
			callU(pCloseHandle, r)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		return true // trava criada: fica com o handle até o programa fechar
	}
	return false
}

// ui roda uma função na thread da janela (obrigatório para mexer em widgets).
func (a *SnowApp) ui(f func()) {
	if a.mw != nil {
		a.mw.Synchronize(f)
	}
}

func (a *SnowApp) setStatus(s string) {
	a.ui(func() {
		if a.statusLB != nil {
			a.statusLB.SetText(s)
		}
	})
}

func (a *SnowApp) cfg() AppSettings {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.settings
}

func (a *SnowApp) setCfg(s AppSettings) {
	a.cfgMu.Lock()
	a.settings = s
	a.cfgMu.Unlock()
}

func (a *SnowApp) currentOutDir() string {
	dir := a.cfg().OutDir
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// ------------------------------------------------------------------ captura

// capture tira a foto conforme o tipo pedido. Pode ser chamada de qualquer
// thread. restoreWindow=true quando o clique veio da janela do app (ela se
// esconde antes de capturar e volta depois).
func (a *SnowApp) capture(kind captureKind, restoreWindow bool) {
	if !atomic.CompareAndSwapInt32(&a.capturing, 0, 1) {
		if restoreWindow {
			a.ui(a.restoreFromTray)
		}
		return
	}
	defer atomic.StoreInt32(&a.capturing, 0)
	if restoreWindow {
		defer a.ui(a.restoreFromTray)
	}

	// O seletor de área tem a sua própria janela e laço de mensagens: precisa
	// ficar sempre na mesma thread do sistema.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cfg := a.cfg()

	if restoreWindow {
		time.Sleep(350 * time.Millisecond) // deixa a janela sumir de verdade
	}
	if sec := delayOptions[clamp(cfg.Delay, len(delayOptions))].Sec; sec > 0 {
		for i := sec; i > 0; i-- {
			a.setStatus(fmt.Sprintf("Capturando em %d…", i))
			time.Sleep(time.Second)
		}
		a.setStatus("Capturando…")
	}

	sh, err := captureVirtual()
	if err != nil {
		a.fail(err)
		return
	}

	var bm *bitmap
	switch kind {
	case kindArea:
		r, ok, err := selectRegion(sh)
		if err != nil {
			a.fail(err)
			return
		}
		if !ok {
			a.setStatus("Captura cancelada.")
			return
		}
		if bm, err = sh.cropScreen(r); err != nil {
			a.fail(err)
			return
		}
	case kindFull:
		if r, ok := monitorUnderCursor(); ok {
			if bm, err = sh.cropScreen(r); err != nil {
				a.fail(err)
				return
			}
		} else {
			bm = &sh.bitmap // não achou o monitor: manda a tela virtual inteira
		}
	case kindWindow:
		r, ok := foregroundWindowRect()
		if !ok {
			a.fail(fmt.Errorf("não achei nenhuma janela ativa"))
			return
		}
		if bm, err = sh.cropScreen(r); err != nil {
			a.fail(err)
			return
		}
	}

	a.deliver(bm, cfg)
}

func (a *SnowApp) fail(err error) {
	a.setStatus("Erro: " + err.Error())
	a.notify("SnowShot", "Não consegui capturar: "+err.Error(), true)
}

// deliver manda a imagem para onde o usuário escolheu (área de transferência
// e/ou arquivo) e avisa.
func (a *SnowApp) deliver(bm *bitmap, cfg AppSettings) {
	pngData, err := bm.encodePNG()
	if err != nil {
		a.fail(err)
		return
	}

	var copied bool
	var copyErr error
	if cfg.CopyClipboard {
		if copyErr = copyToClipboard(bm, pngData); copyErr == nil {
			copied = true
		}
	}

	var path string
	var saveErr error
	if cfg.SaveFile {
		path, saveErr = saveShot(cfg.OutDir, pngData)
	}
	saved := path != ""

	// Nada deu certo: avisa o erro.
	if !copied && !saved {
		var errs []string
		if copyErr != nil {
			errs = append(errs, copyErr.Error())
		}
		if saveErr != nil {
			errs = append(errs, saveErr.Error())
		}
		a.fail(fmt.Errorf("%s", strings.Join(errs, "; ")))
		return
	}

	var what []string
	if copied {
		what = append(what, "copiada")
	}
	if saved {
		what = append(what, "salva")
	}
	title := fmt.Sprintf("Captura %s (%d×%d)", strings.Join(what, " e "), bm.W, bm.H)
	body := "Cole com Ctrl+V."
	if saved {
		body = filepath.Base(path)
	}
	a.notify(title, body, false)
	a.setStatus(title)

	// Deu certo em um destino e falhou no outro: avisa também.
	if copyErr != nil {
		a.notify("Não consegui copiar para a área de transferência", copyErr.Error(), true)
	}
	if saveErr != nil {
		a.notify("Não consegui salvar o arquivo", saveErr.Error(), true)
	}

	if saved {
		a.ui(func() { a.addRecent(path) })
	}
}

// ------------------------------------------------------------- lista recente

func (a *SnowApp) addRecent(path string) {
	a.recent = append([]string{path}, a.recent...)
	if len(a.recent) > 30 {
		a.recent = a.recent[:30]
	}
	a.refreshRecent()
}

func (a *SnowApp) refreshRecent() {
	if a.recentLB == nil {
		return
	}
	names := make([]string, len(a.recent))
	for i, p := range a.recent {
		names[i] = strings.TrimSuffix(filepath.Base(p), ".png")
	}
	_ = a.recentLB.SetModel(names)
	if len(names) > 0 {
		_ = a.recentLB.SetCurrentIndex(0)
	}
}

func (a *SnowApp) selectedRecent() string {
	if a.recentLB == nil {
		return ""
	}
	i := a.recentLB.CurrentIndex()
	if i < 0 || i >= len(a.recent) {
		return ""
	}
	return a.recent[i]
}

// ------------------------------------------------------------------- janela

func (a *SnowApp) run(startTray bool) error {
	var (
		areaCB, fullCB, winCB, delayCB *walk.ComboBox
		outEdit                        *walk.LineEdit
		copyCK, saveCK, trayCK, autoCK *walk.CheckBox
	)
	ready := false
	s := a.settings

	persist := func() {
		if !ready {
			return
		}
		cur := AppSettings{
			OutDir:        strings.TrimSpace(outEdit.Text()),
			CopyClipboard: copyCK.Checked(),
			SaveFile:      saveCK.Checked(),
			Delay:         clamp(delayCB.CurrentIndex(), len(delayOptions)),
			HotArea:       clamp(areaCB.CurrentIndex(), len(hotkeyPresets)),
			HotFull:       clamp(fullCB.CurrentIndex(), len(hotkeyPresets)),
			HotWindow:     clamp(winCB.CurrentIndex(), len(hotkeyPresets)),
			TrayMin:       trayCK.Checked(),
		}
		if !cur.CopyClipboard && !cur.SaveFile {
			cur.CopyClipboard = true
			copyCK.SetChecked(true)
		}
		if cur.OutDir == "" {
			cur.OutDir = defaultSettings().OutDir
			outEdit.SetText(cur.OutDir)
		}
		a.trayMin = cur.TrayMin
		a.setCfg(cur)
		saveSettings(cur)
	}
	a.persistFn = persist

	applyHotkeys := func() {
		if !ready {
			return
		}
		persist()
		cur := a.cfg()
		a.hkMu.Lock()
		a.hkSel = [3]int{cur.HotArea, cur.HotFull, cur.HotWindow}
		a.hkMu.Unlock()
		a.reloadHotkeys()
	}

	// Clique num botão da janela: esconde a janela, captura e traz de volta.
	fromWindow := func(kind captureKind) func() {
		return func() {
			if atomic.LoadInt32(&a.capturing) != 0 {
				return
			}
			persist()
			a.mw.Hide()
			go a.capture(kind, true)
		}
	}

	err := MainWindow{
		AssignTo: &a.mw,
		Title:    appTitle + versionSuffix() + " — capturas de tela",
		MinSize:  Size{Width: 640, Height: 640},
		Size:     Size{Width: 700, Height: 700},
		Visible:  !startTray,
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "Capturar agora",
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "Área", MinSize: Size{Width: 110, Height: 34}, OnClicked: fromWindow(kindArea)},
					PushButton{Text: "Tela inteira", MinSize: Size{Width: 130, Height: 34}, OnClicked: fromWindow(kindFull)},
					PushButton{Text: "Janela ativa", MinSize: Size{Width: 130, Height: 34}, OnClicked: fromWindow(kindWindow)},
					HSpacer{},
					Label{Text: "Atraso:"},
					ComboBox{
						AssignTo:     &delayCB,
						Model:        delayNames(),
						CurrentIndex: clamp(s.Delay, len(delayOptions)),
						OnCurrentIndexChanged: func() {
							persist()
						},
					},
				},
			},
			GroupBox{
				Title:  "Atalhos (funcionam mesmo com o programa na bandeja)",
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "Capturar área:"},
					ComboBox{AssignTo: &areaCB, Model: hotkeyNames(), CurrentIndex: clamp(s.HotArea, len(hotkeyPresets)), OnCurrentIndexChanged: applyHotkeys},
					Label{Text: "Capturar tela inteira (monitor do mouse):"},
					ComboBox{AssignTo: &fullCB, Model: hotkeyNames(), CurrentIndex: clamp(s.HotFull, len(hotkeyPresets)), OnCurrentIndexChanged: applyHotkeys},
					Label{Text: "Capturar janela ativa:"},
					ComboBox{AssignTo: &winCB, Model: hotkeyNames(), CurrentIndex: clamp(s.HotWindow, len(hotkeyPresets)), OnCurrentIndexChanged: applyHotkeys},
				},
			},
			GroupBox{
				Title:  "Ao capturar",
				Layout: VBox{},
				Children: []Widget{
					CheckBox{AssignTo: &copyCK, Text: "Copiar para a área de transferência (cole com Ctrl+V)", Checked: s.CopyClipboard, OnCheckedChanged: persist},
					CheckBox{AssignTo: &saveCK, Text: "Salvar um arquivo PNG na pasta abaixo", Checked: s.SaveFile, OnCheckedChanged: persist},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							Label{Text: "Pasta:"},
							LineEdit{AssignTo: &outEdit, Text: s.OutDir, OnEditingFinished: persist},
							PushButton{
								Text: "Escolher…",
								OnClicked: func() {
									dlg := new(walk.FileDialog)
									dlg.Title = "Escolha a pasta das capturas"
									dlg.FilePath = outEdit.Text()
									if ok, err := dlg.ShowBrowseFolder(a.mw); err == nil && ok {
										outEdit.SetText(dlg.FilePath)
										persist()
									}
								},
							},
							PushButton{Text: "Abrir", OnClicked: func() { persist(); openFolder(a.currentOutDir()) }},
						},
					},
				},
			},
			GroupBox{
				Title:  "Geral",
				Layout: VBox{},
				Children: []Widget{
					CheckBox{AssignTo: &trayCK, Text: "Ao fechar ou minimizar, continuar na bandeja (os atalhos seguem funcionando)", Checked: s.TrayMin, OnCheckedChanged: persist},
					CheckBox{
						AssignTo: &autoCK,
						Text:     "Abrir junto com o Windows (já na bandeja)",
						Checked:  isAutoStart(),
						OnCheckedChanged: func() {
							if !ready {
								return
							}
							if err := setAutoStart(autoCK.Checked()); err != nil {
								walk.MsgBox(a.mw, appTitle, "Não consegui mudar isso:\n\n"+err.Error(), walk.MsgBoxIconError)
							}
						},
					},
				},
			},
			Label{Text: "Últimas capturas (duplo clique abre a imagem):"},
			ListBox{
				AssignTo: &a.recentLB,
				Model:    []string{},
				OnItemActivated: func() {
					if p := a.selectedRecent(); p != "" {
						openFile(p)
					}
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{
						Text: "Copiar de novo",
						OnClicked: func() {
							p := a.selectedRecent()
							if p == "" {
								return
							}
							go func() {
								bm, err := loadBitmapFile(p)
								if err == nil {
									var data []byte
									if data, err = bm.encodePNG(); err == nil {
										err = copyToClipboard(bm, data)
									}
								}
								if err != nil {
									a.setStatus("Não consegui copiar: " + err.Error())
									return
								}
								a.setStatus("Imagem copiada. Cole com Ctrl+V.")
							}()
						},
					},
					PushButton{Text: "Abrir", OnClicked: func() {
						if p := a.selectedRecent(); p != "" {
							openFile(p)
						}
					}},
					PushButton{Text: "Mostrar na pasta", OnClicked: func() {
						if p := a.selectedRecent(); p != "" {
							showInFolder(p)
						}
					}},
					HSpacer{},
					PushButton{Text: "Atualizar app", OnClicked: func() { a.checkAppUpdate(true) }},
				},
			},
			Label{AssignTo: &a.statusLB, Text: "Preparando…"},
			Label{Text: "Criado por Guilherme Souto", TextColor: walk.RGB(120, 120, 120)},
		},
	}.Create()
	if err != nil {
		return err
	}

	icon := loadAppIcon()
	_ = a.mw.SetIcon(icon)
	a.setupTray(icon)

	// Capturas que já estão na pasta.
	a.recent = listRecent(a.currentOutDir(), 30)
	a.refreshRecent()

	// Minimizar manda para a bandeja (se a opção estiver ligada).
	a.mw.SizeChanged().Attach(func() {
		if !a.trayMin || !win.IsIconic(a.mw.Handle()) {
			return
		}
		a.mw.Synchronize(func() {
			if !a.trayMin || !win.IsIconic(a.mw.Handle()) {
				return
			}
			a.mw.Hide()
			a.trayHint()
		})
	})

	// O X da janela também só esconde (o "Sair" fica no menu da bandeja).
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !a.quitting && a.trayMin && reason == walk.CloseReasonUser {
			*canceled = true
			a.mw.Hide()
			a.trayHint()
			return
		}
		persist()
		a.stopHotkeys()
		if a.ni != nil {
			_ = a.ni.Dispose()
		}
	})

	ready = true
	a.persistFn()

	a.startHotkeys()

	go func() {
		time.Sleep(3 * time.Second)
		cleanupOldExe()
		a.autoChecks()
	}()

	// Nesta versão do walk o loop principal roda via walk.App().Run().
	walk.App().Run()
	return nil
}

func (a *SnowApp) trayHint() {
	if a.trayHinted {
		return
	}
	a.trayHinted = true
	a.notify("SnowShot continua rodando",
		"Os atalhos seguem funcionando. Clique no ícone da bandeja para abrir.", false)
}

func delayNames() []string {
	out := make([]string, len(delayOptions))
	for i, d := range delayOptions {
		out[i] = d.Name
	}
	return out
}
