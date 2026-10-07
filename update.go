package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tailscale/walk"
)

// buildID é preenchido pelo GitHub Actions na hora de compilar
// (-X main.buildID=<sha>). Em compilação local fica "dev".
var buildID = "dev"

const (
	updateAPI = "https://api.github.com/repos/beemoon23/SnowShot/releases/tags/latest"
	exeAsset  = "SnowShot.exe"
)

var buildRe = regexp.MustCompile(`build:([0-9a-f]{7,40})`)

type remoteInfo struct {
	Build string
	URL   string
}

func versionSuffix() string {
	if buildID == "dev" {
		return ""
	}
	return " " + buildID
}

func sameBuild(a, b string) bool {
	return a != "" && b != "" && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

// fetchRemote lê a release "latest" do GitHub e devolve o build e o link do .exe.
func fetchRemote() (remoteInfo, error) {
	req, err := http.NewRequest("GET", updateAPI, nil)
	if err != nil {
		return remoteInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "SnowShot")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return remoteInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return remoteInfo{}, fmt.Errorf("o GitHub respondeu %s", resp.Status)
	}

	var rel struct {
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return remoteInfo{}, err
	}

	var info remoteInfo
	if m := buildRe.FindStringSubmatch(rel.Body); m != nil {
		info.Build = m[1]
	}
	for _, as := range rel.Assets {
		if strings.EqualFold(as.Name, exeAsset) {
			info.URL = as.URL
		}
	}
	if info.Build == "" || info.URL == "" {
		return remoteInfo{}, fmt.Errorf("a release publicada não tem o formato esperado")
	}
	return info, nil
}

// downloadFile baixa uma URL para dest, avisando o progresso.
func downloadFile(url, dest string, onProgress func(done, total int64)) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("servidor respondeu %s", resp.Status)
	}

	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	var done int64
	buf := make([]byte, 128*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(tmp)
				return werr
			}
			done += int64(n)
			if onProgress != nil {
				onProgress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(dest)
	return os.Rename(tmp, dest)
}

func human(done, total int64) string {
	mb := func(b int64) float64 { return float64(b) / 1024 / 1024 }
	if total > 0 {
		return fmt.Sprintf("%.1f / %.1f MB", mb(done), mb(total))
	}
	return fmt.Sprintf("%.1f MB", mb(done))
}

// swapExe baixa o .exe novo e troca pelo atual. O Windows deixa RENOMEAR um
// programa em execução (só não deixa apagar/sobrescrever), então o atual vira
// ".old" e o novo ocupa o lugar. Devolve o caminho do .exe para reabrir.
func swapExe(url string, onProgress func(done, total int64)) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	newPath := exe + ".new"
	oldPath := exe + ".old"

	if err := downloadFile(url, newPath, onProgress); err != nil {
		return "", fmt.Errorf("falha ao baixar: %w", err)
	}

	// Confere se é mesmo um programa do Windows (começa com "MZ") e tem tamanho razoável.
	st, err := os.Stat(newPath)
	if err != nil || st.Size() < 1<<20 {
		os.Remove(newPath)
		return "", fmt.Errorf("o arquivo baixado veio incompleto")
	}
	if f, err := os.Open(newPath); err == nil {
		head := make([]byte, 2)
		_, _ = f.Read(head)
		f.Close()
		if string(head) != "MZ" {
			os.Remove(newPath)
			return "", fmt.Errorf("o arquivo baixado não é um programa válido")
		}
	}

	os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		os.Remove(newPath)
		return "", fmt.Errorf("não consegui trocar o programa (%v).\n\nSe ele está em \"Arquivos de Programas\", reinstale pelo Setup mais recente", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(oldPath, exe) // volta o antigo
		return "", err
	}
	return exe, nil
}

// cleanupOldExe apaga restos de atualizações anteriores.
func cleanupOldExe() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
		os.Remove(exe + ".new")
	}
}

// checkAppUpdate confere se há versão nova. manual=true também avisa quando
// não há nada novo ou quando algo deu errado (a checagem automática é calada).
func (a *SnowApp) checkAppUpdate(manual bool) {
	go func() {
		r, err := fetchRemote()
		a.ui(func() {
			switch {
			case err != nil:
				if manual {
					walk.MsgBox(a.mw, appTitle, "Não consegui verificar atualização:\n\n"+err.Error(), walk.MsgBoxIconError)
				}
			case buildID == "dev":
				if manual {
					walk.MsgBox(a.mw, appTitle,
						"Esta cópia não tem número de versão (foi compilada fora do GitHub), então não dá para comparar.\n\n"+
							"Baixe uma vez a versão da aba Releases do GitHub. A partir dela, as atualizações passam a ser automáticas.",
						walk.MsgBoxIconInformation)
				}
			case sameBuild(r.Build, buildID):
				if manual {
					walk.MsgBox(a.mw, appTitle, "Você já está com a versão mais recente ("+buildID+").", walk.MsgBoxIconInformation)
				}
			default:
				a.askUpdate(r)
			}
		})
	}()
}

// askUpdate pergunta se pode atualizar (roda na thread da janela).
func (a *SnowApp) askUpdate(r remoteInfo) {
	// Se a janela está escondida na bandeja, traz para a frente antes de perguntar.
	if !a.mw.Visible() {
		a.restoreFromTray()
	}
	msg := "Tem uma versão nova do SnowShot (" + r.Build + ").\n\n" +
		"Atualizar agora? O programa fecha e abre de novo sozinho."
	if walk.MsgBox(a.mw, appTitle, msg, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}

	if atomic.LoadInt32(&a.capturing) != 0 {
		walk.MsgBox(a.mw, appTitle,
			"Tem uma captura em andamento. Termine e clique em \"Atualizar app\".",
			walk.MsgBoxIconInformation)
		return
	}

	a.setStatus("Baixando atualização…")
	go func() {
		exe, err := swapExe(r.URL, func(d, t int64) {
			a.setStatus("Baixando atualização… " + human(d, t))
		})
		a.ui(func() {
			if err != nil {
				a.setStatus("A atualização falhou.")
				walk.MsgBox(a.mw, appTitle, "Não consegui atualizar:\n\n"+err.Error(), walk.MsgBoxIconError)
				return
			}
			if a.persistFn != nil {
				a.persistFn() // salva as opções antes de abrir a versão nova
			}
			a.quitting = true
			a.stopHotkeys() // libera os atalhos para a versão nova poder usá-los
			cmd := exec.Command(exe)
			cmd.Dir = filepath.Dir(exe)
			if err := cmd.Start(); err != nil {
				walk.MsgBox(a.mw, appTitle, "Atualizei, mas não consegui reabrir sozinho. Abra o programa de novo.", walk.MsgBoxIconInformation)
			}
			_ = a.mw.Close()
		})
	}()
}

// autoChecks roda em segundo plano na abertura: procura versão nova do app.
func (a *SnowApp) autoChecks() {
	if buildID != "dev" {
		a.checkAppUpdate(false)
	}
}
