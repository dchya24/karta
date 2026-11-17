package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"karta/internal/app"
	"karta/internal/store"
)

// testService membuat Service di atas SQLite in-memory dengan satu akun,
// satu folder, dua pesan — tanpa keyring nyata.
func testService(t *testing.T) *app.Service {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	accID, err := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	ms := []struct {
		uid    int
		subj   string
		sender string
	}{{1, "Salam pembuka", "Budi <budi@contoh.id>"}, {2, "Laporan mingguan", "Cici <cici@contoh.id>"}}
	for _, m := range ms {
		if _, err := db.UpsertMessage(&store.MessageHeader{
			FolderID: int(inbox), UID: m.uid, Subject: m.subj, Sender: m.sender,
			SenderAddr: "x@" + m.sender, Date: "2026-11-17T01:0%d:00Z", Unread: true,
		}, &store.MessageBody{PlainText: "isi " + m.subj}); err != nil {
			t.Fatal(err)
		}
	}
	svc := app.New(db)
	t.Cleanup(func() { svc.Close() })
	return svc
}

// TestViewBuilds memastikan view 3-pane terbangun tanpa GUI (headless Tester):
// sidebar folder, daftar pesan, dan reading pane menampilkan isi pesan.
func TestViewBuilds(t *testing.T) {
	st := &state{svc: testService(t)}
	st.reloadFolders()
	tt := ui.NewTester(st.view, 1100, 720)
	if !tt.HasText("Karta") {
		t.Fatal("judul Karta tidak tampil")
	}
	if !tt.HasText("INBOX") {
		t.Fatal("folder INBOX tidak tampil di sidebar")
	}
	if !tt.HasText("Salam pembuka") {
		t.Fatal("subjek pesan tidak tampil di daftar")
	}
	// Reading pane: pesan terbaru terpilih otomatis (urutan date DESC).
	if !tt.HasText("isi Laporan mingguan") {
		t.Fatal("reading pane tidak menampilkan isi pesan")
	}
}

// TestAccountFormGoogle memastikan tombol Login via Google tampil, semua
// field manual tetap ada, dan status OAuth inline tampil.
func TestAccountFormGoogle(t *testing.T) {
	st := &state{svc: testService(t)}
	st.showAccount = true
	tt := ui.NewTester(st.view, 1100, 720)
	if !tt.HasText("Login via Google") {
		t.Fatal("tombol Login via Google tidak tampil")
	}
	if !tt.HasText("IMAP host") {
		t.Fatal("field IMAP host tidak tampil")
	}
	// Status OAuth inline.
	st.oauthMsg, st.oauthFail = "Menunggu authorize di browser…", false
	tt.Frame()
	if !tt.HasText("Menunggu authorize di browser") {
		t.Fatal("status OAuth tidak tampil")
	}
}

// TestViewSearch memastikan query pencarian memfilter daftar (FTS5).
func TestViewSearch(t *testing.T) {
	st := &state{svc: testService(t)}
	st.reloadFolders()
	tt := ui.NewTester(st.view, 1100, 720)
	if !tt.HasText("Salam pembuka") {
		t.Fatal("pesan lain tidak tampil sebelum filter")
	}
	st.query = "lapor"
	st.reloadMessages()
	tt.Frame()
	if tt.HasText("Salam pembuka") {
		t.Fatal("filter FTS5 tidak menyaring pesan lain")
	}
	if !tt.HasText("Laporan mingguan") {
		t.Fatal("hasil cari tidak tampil")
	}
}
