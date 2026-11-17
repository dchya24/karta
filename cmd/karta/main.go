// Command karta menjalankan aplikasi email desktop Karta: memasang store,
// service, worker sinkronisasi, dan window native mygo/ui.
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"karta/internal/app"
	"karta/internal/bridge"
	"karta/internal/store"
	"karta/internal/ui"
)

func main() {
	startTime := time.Now()
	dbPath, err := databasePath()
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	svc := app.New(db)
	defer svc.Close()

	if err := bridge.Start(); err != nil {
		log.Printf("bridge: %v", err)
	}
	ui.SetReadyLogger(func() {
		log.Printf("karta: jendela siap dalam %s", time.Since(startTime))
	})
	if err := ui.Run(svc); err != nil {
		log.Fatal(err)
	}
}

// databasePath mengembalikan lokasi SQLite di direktori config pengguna.
func databasePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "karta")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "karta.db"), nil
}
