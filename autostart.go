package main

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "SnowShot"
)

// isAutoStart diz se o SnowShot está configurado para abrir com o Windows.
func isAutoStart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValueName)
	return err == nil && strings.TrimSpace(v) != ""
}

// setAutoStart liga ou desliga "abrir com o Windows" (só para o seu usuário,
// sem precisar de administrador). Ele abre direto na bandeja.
func setAutoStart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue(runValueName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValueName, `"`+exe+`" --tray`)
}
