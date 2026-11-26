package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"karta/internal/app"
	"karta/internal/store"
)

// welcomeService membuat Service di atas SQLite in-memory tanpa akun —
// kondisi first-run yang memicu layar welcome.
func welcomeService(t *testing.T) *app.Service {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := app.New(db)
	svc.SetKeyringForTesting(app.NewFakeKeyring())
	t.Cleanup(func() { svc.Close() })
	return svc
}

// TestWelcomeScreenFirstRun memastikan saat belum ada akun, view menampilkan
// layar welcome layar penuh (logo, nama app, tagline, tombol utama) alih-alih
// 3-pane kosong.
func TestWelcomeScreenFirstRun(t *testing.T) {
	st := &state{svc: welcomeService(t), fetching: map[int]bool{}, fetchErr: map[int]string{}}
	st.reloadFolders() // 0 akun → sections kosong
	if len(st.sections) != 0 {
		t.Fatalf("sections=%d, mau 0", len(st.sections))
	}
	tt := ui.NewTester(st.view, 1100, 720)
	if !tt.HasText("Karta") {
		t.Fatal("nama app tidak tampil di welcome")
	}
	if !tt.HasText("Email client native. Cepat, tenang, teks-first.") {
		t.Fatal("tagline tidak tampil di welcome")
	}
	if !tt.HasText("Tambah akun") {
		t.Fatal("tombol Tambah akun tidak tampil di welcome")
	}
	// 3-pane tidak boleh ikut tampil: placeholder daftar & toolbar aksi
	// reading pane harus absah.
	if tt.HasText("Tulis") || tt.HasText("Cari…") || tt.HasText("Pilih pesan untuk dibaca") {
		t.Fatal("welcome harus menggantikan 3-pane, bukan menumpuk")
	}
}

// TestWelcomeButtonOpensAccountForm memastikan klik "Tambah akun" membuka
// form tambah akun layar penuh (path s.showAccount yang sudah ada).
func TestWelcomeButtonOpensAccountForm(t *testing.T) {
	st := &state{svc: welcomeService(t), fetching: map[int]bool{}, fetchErr: map[int]string{}}
	st.reloadFolders()
	tt := ui.NewTester(st.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("Tambah akun pertama"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !st.showAccount {
		t.Fatal("klik Tambah akun tidak membuka form akun")
	}
	if !tt.HasText("Tambah Akun") || !tt.HasText("IMAP host") {
		t.Fatal("form akun tidak tampil setelah klik tombol welcome")
	}
	// Welcome tidak boleh tampil lagi bersama form.
	if tt.HasText("Email client native") {
		t.Fatal("welcome masih tampil saat form akun terbuka")
	}
}

// TestEmptyFolderPlaceholder memastikan folder terpilih tapi 0 pesan
// menampilkan placeholder "Tidak ada pesan" di pane tengah.
func TestEmptyFolderPlaceholder(t *testing.T) {
	st := &state{svc: testService(t)}
	st.reloadFolders()
	// Pilih folder yang tidak punya pesan (Trash dari testService kosong).
	found := false
	for _, sec := range st.sections {
		for _, f := range sec.folders {
			if f.Role == "trash" {
				st.account, st.folder, st.folderSel = sec.acc.ID, f.ID, sectionKey(f)
				found = true
			}
		}
	}
	if !found {
		t.Fatal("folder Trash tidak ditemukan di testService")
	}
	st.reloadMessages()
	if len(st.msgs) != 0 {
		t.Fatalf("msgs=%d, mau 0 di folder kosong", len(st.msgs))
	}
	tt := ui.NewTester(st.view, 1100, 720)
	if !tt.HasText("Tidak ada pesan") {
		t.Fatal("placeholder 'Tidak ada pesan' tidak tampil di daftar kosong")
	}
}
