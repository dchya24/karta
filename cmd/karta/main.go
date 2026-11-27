// Command karta menjalankan aplikasi email desktop Karta: memasang store,
// service, worker sinkronisasi, dan window native mygo/ui.
//
// Subcommand `karta export` mengekspor pesan lokal dari SQLite ke mbox
// (default) atau EML tanpa GUI; perilaku lain menjalankan GUI.
package main

import (
	"context"
	"flag"
	"fmt"
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

	if len(os.Args) > 1 && os.Args[1] == "export" {
		if err := runExport(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}

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

// runExport memproses `karta export [direktori] [flags]`: ekspor pesan lokal
// ke mbox/EML lalu cetak ringkasan. Tanpa GUI, keyring, maupun jaringan.
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	format := fs.String("format", "mbox", `format ekspor: "mbox" atau "eml"`)
	account := fs.String("account", "", "filter satu akun (alamat email); kosong = semua akun")
	folder := fs.String("folder", "", "filter peran folder (inbox, archive, dst.); kosong = semua folder")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Penggunaan: karta export [direktori] [--format mbox|eml] [--account email] [--folder role]

Mengekspor pesan lokal dari SQLite ke mbox (satu file per folder:
<email>--<role>.mbox) atau EML (<role>/<UID>-<slug-subjek>.eml).
Direktori tujuan default: direktori kerja saat ini.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := "."
	switch fs.NArg() {
	case 0:
	case 1:
		dir = fs.Arg(0)
	default:
		return fmt.Errorf("terlalu banyak argumen posisional: %v", fs.Args()[1:])
	}

	dbPath, err := databasePath()
	if err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := app.New(db)
	defer svc.Close()

	results, err := svc.Export(context.Background(), app.ExportOptions{
		Format:       *format,
		Directory:    dir,
		AccountEmail: *account,
		FolderRole:   *folder,
	})
	if err != nil {
		return err
	}

	// Ringkasan: akun unik, folder, pesan, dan path. Folder gagal tidak
	// menghentikan lainnya; kegagalannya dilaporkan ke stderr.
	accounts := map[string]bool{}
	folders, messages := 0, 0
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(os.Stderr, "karta export: folder %s (%s) gagal: %s\n",
				r.FolderName, r.AccountEmail, r.Error)
			continue
		}
		accounts[r.AccountEmail] = true
		folders++
		messages += r.Count
		fmt.Printf("%-40s %4d pesan  %s\n", r.AccountEmail+"/"+r.FolderName, r.Count, r.Path)
	}
	fmt.Printf("Ekspor selesai: %d akun, %d folder, %d pesan → %s\n",
		len(accounts), folders, messages, dir)
	return nil
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
