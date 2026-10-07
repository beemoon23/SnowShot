package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppSettings é o que o app lembra entre uma abertura e outra.
type AppSettings struct {
	OutDir        string `json:"outDir"`
	CopyClipboard bool   `json:"copyClipboard"`
	SaveFile      bool   `json:"saveFile"`
	Delay         int    `json:"delay"` // índice em delayOptions

	// Índices em hotkeyPresets
	HotArea   int `json:"hotArea"`
	HotFull   int `json:"hotFull"`
	HotWindow int `json:"hotWindow"`

	TrayMin bool `json:"trayMin"` // fechar/minimizar manda para a bandeja
}

func defaultSettings() AppSettings {
	home, _ := os.UserHomeDir()
	return AppSettings{
		OutDir:        filepath.Join(home, "Pictures", "SnowShot"),
		CopyClipboard: true,
		SaveFile:      true,
		Delay:         0,
		HotArea:       0, // PrintScreen
		HotFull:       1, // Ctrl+PrintScreen
		HotWindow:     2, // Shift+PrintScreen
		TrayMin:       true,
	}
}

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "SnowShot", "settings.json")
}

func loadSettings() AppSettings {
	s := defaultSettings()
	b, err := os.ReadFile(settingsPath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	if s.OutDir == "" {
		s.OutDir = defaultSettings().OutDir
	}
	s.Delay = clamp(s.Delay, len(delayOptions))
	s.HotArea = clamp(s.HotArea, len(hotkeyPresets))
	s.HotFull = clamp(s.HotFull, len(hotkeyPresets))
	s.HotWindow = clamp(s.HotWindow, len(hotkeyPresets))
	if !s.CopyClipboard && !s.SaveFile {
		s.CopyClipboard = true // sem nenhum destino a captura se perderia
	}
	return s
}

func saveSettings(s AppSettings) {
	p := settingsPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0o644)
}

func clamp(i, n int) int {
	if i < 0 || i >= n {
		return 0
	}
	return i
}
