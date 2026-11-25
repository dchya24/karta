// prefs.json menyimpan preferensi tampilan Karta lintas sesi; hanya
// berisi pilihan tema eksplisit pengguna. Kosong berarti ikut sistem.
package ui

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type prefs struct {
	Appearance string `json:"appearance,omitempty"` // "" | "light" | "dark"
}

// prefsPath mengembalikan lokasi prefs.json di direktori config pengguna.
func prefsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "karta", "prefs.json"), nil
}

// loadPrefs membaca prefs.json; gagal berarti pakai default (ikut sistem).
func loadPrefs() prefs {
	path, err := prefsPath()
	if err != nil {
		return prefs{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return prefs{}
	}
	var p prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return prefs{}
	}
	return p
}

// savePrefs menulis prefs.json; kegagalan hanya dicatat log.
func savePrefs(p prefs) {
	path, err := prefsPath()
	if err != nil {
		log.Printf("ui: path prefs: %v", err)
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		log.Printf("ui: serial prefs: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		log.Printf("ui: simpan prefs: %v", err)
	}
}
