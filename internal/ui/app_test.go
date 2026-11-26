package ui

import (
	"fmt"
	"strings"
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
	if _, err := db.EnsureFolder(accID, "[Gmail]/All Mail", "archive"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureFolder(accID, "[Gmail]/Trash", "trash"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureFolder(accID, "[Gmail]/Important", "other"); err != nil {
		t.Fatal(err)
	}
	ms := []struct {
		uid    int
		subj   string
		sender string
		addr   string
		to     string
		cc     string
		body   string
	}{
		{1, "Salam pembuka", "Budi <budi@contoh.id>", "budi@contoh.id", "a@contoh.id", "", "isi Salam pembuka"},
		{2, "Laporan mingguan", "Cici <cici@contoh.id>", "cici@contoh.id", "a@contoh.id, dodi@contoh.id", "eko@contoh.id", "isi Laporan mingguan — lihat https://karta.dev/docs."},
	}
	for _, m := range ms {
		if _, err := db.UpsertMessage(&store.MessageHeader{
			FolderID: int(inbox), UID: m.uid, Subject: m.subj, Sender: m.sender,
			SenderAddr: m.addr, Recipients: m.to, Cc: m.cc,
			Date: fmt.Sprintf("2026-11-17T01:0%d:00Z", m.uid), Unread: true,
		}, &store.MessageBody{PlainText: m.body}); err != nil {
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
	if !tt.HasText("Inbox") {
		t.Fatal("folder Inbox tidak tampil di sidebar")
	}
	if !tt.HasText("Salam pembuka") {
		t.Fatal("subjek pesan tidak tampil di daftar")
	}
	// Reading pane: pesan terbaru terpilih otomatis (urutan date DESC) lewat richtext read-only.
	if st.readerDoc == nil || !strings.Contains(st.readerDoc.Text().PlainText(), "isi Laporan mingguan") {
		t.Fatal("reading pane tidak menampilkan isi pesan")
	}
	if !tt.HasText("Balas Semua") || !tt.HasText("Arsip") || !tt.HasText("Hapus") {
		t.Fatal("toolbar aksi reading pane tidak lengkap")
	}
	if !tt.HasText("Archive") || !tt.HasText("Trash") || !tt.HasText("Important") || tt.HasText("[Gmail]") {
		t.Fatal("nama folder [Gmail]/ harus digeneralisasi di sidebar")
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

func TestLinkifyAndMailto(t *testing.T) {
	txt := linkifyText("Cek https://karta.dev/docs. atau mailto:tim@karta.dev?subject=Halo!")
	var links []string
	for _, run := range txt.Runs(0, txt.Len()) {
		if run.Attributes.Link != "" {
			links = append(links, run.Attributes.Link)
		}
	}
	if len(links) != 2 || links[0] != "https://karta.dev/docs" || links[1] != "mailto:tim@karta.dev?subject=Halo" {
		t.Fatalf("links=%v", links)
	}
	to, subj := parseMailto("mailto:tim@karta.dev?subject=Halo%20Karta")
	if to != "tim@karta.dev" || subj != "Halo Karta" {
		t.Fatalf("mailto to=%q subj=%q", to, subj)
	}
}

func TestReadingPaneHeadersReplyAllAndShortcuts(t *testing.T) {
	st := &state{svc: testService(t)}
	st.reloadFolders()
	tt := ui.NewTester(st.view, 1100, 720)
	if !tt.HasText("Cc: eko@contoh.id") {
		t.Fatal("header Cc tidak tampil di reading pane")
	}
	// Shift+R → Balas Semua: To=pengirim, Cc=penerima lain tanpa akun sendiri.
	tt.Key(ui.Shift, ui.KeyR)
	if !st.composerOpen || st.composerMode != "reply_all" {
		t.Fatalf("Shift+R gagal: open=%v mode=%q", st.composerOpen, st.composerMode)
	}
	if st.dTo != "cici@contoh.id" || st.dCc != "dodi@contoh.id, eko@contoh.id" {
		t.Fatalf("reply_all to=%q cc=%q", st.dTo, st.dCc)
	}
	st.closeComposer()
	tt.Frame()

	// E → Arsipkan pesan terpilih (2 → 1 di INBOX) + tampilkan toast status.
	tt.Key(0, ui.KeyE)
	tt.Frame()
	if len(st.msgs) != 1 || st.msgs[0].Subject != "Salam pembuka" {
		t.Fatalf("shortcut E gagal arsip: %+v", st.msgs)
	}
	if !tt.HasText("Pesan diarsipkan") && !tt.HasText("Diarsipkan lokal") {
		t.Fatal("toast arsip tidak tampil")
	}

	// / → Fokus ke kolom pencarian.
	tt.Key(0, ui.KeySlash)
	if !tt.Focused("Cari") {
		t.Fatal("shortcut / tidak memfokuskan kolom pencarian")
	}
}

func TestOAuthReloginStopsInfiniteLazyFetch(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	accID, _ := db.AddAccount("uji-oauth-expired@contoh.id", "imap.gmail.com", 993, true, "smtp.gmail.com", 465, true, "oauth2")
	inbox, _ := db.EnsureFolder(accID, "INBOX", "inbox")
	// Pesan belum ter-fetch (body nil → fetched=0).
	msgID, err := db.UpsertMessage(&store.MessageHeader{
		FolderID: int(inbox), UID: 47, Subject: "Perlu fetch",
		Sender: "Google <no-reply@google.com>", SenderAddr: "no-reply@google.com",
		Date: "2026-11-25T14:51:00Z", Unread: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(db)
	svc.SetKeyringForTesting(app.NewFakeKeyring())
	t.Cleanup(func() { svc.Close() })

	st := &state{svc: svc, fetching: map[int]bool{}, fetchErr: map[int]string{}}
	st.reloadFolders()

	// Frame 1 memicu fetchBodyLazy sekali → gagal dengan OAuthReloginError;
	// Frame 2 menampilkan tombol Login ulang Google & Coba lagi tanpa retry loop.
	tt := ui.NewTester(st.view, 1100, 720)
	tt.Frame()
	if st.reloginEmail != "uji-oauth-expired@contoh.id" {
		t.Fatalf("reloginEmail=%q", st.reloginEmail)
	}
	if st.fetchErr[int(msgID)] == "" {
		t.Fatal("fetchErr harus tercatat agar tidak loop")
	}
	if !tt.HasText("Login ulang Google") || !tt.HasText("Coba lagi") {
		t.Fatal("tombol Login ulang Google / Coba lagi tidak tampil saat sesi kedaluwarsa")
	}
	if st.fetching[int(msgID)] {
		t.Fatal("fetchBodyLazy tidak boleh dipanggil berulang setelah gagal")
	}
}

// TestDeleteAccountModal memastikan konfirmasi hapus akun pakai modal native
// mygo/ui (bukan dialog OS) dan eksekusi hapus bekerja.
func TestDeleteAccountModal(t *testing.T) {
	svc := testService(t)
	st := &state{svc: svc, fetching: map[int]bool{}}
	st.reloadFolders()
	acc := st.sections[0].acc
	tt := ui.NewTester(st.view, 1100, 720)
	tt.Frame()

	st.deleteAccount(acc)
	tt.Frame()
	if !tt.HasText("Hapus akun " + acc.Email + "?") {
		t.Fatal("modal konfirmasi hapus tidak tampil")
	}
	if err := tt.Click("Hapus akun " + acc.Email); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.delAccOpen {
		t.Fatal("modal harus tertutup setelah konfirmasi")
	}
	accs, err := svc.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 0 {
		t.Fatalf("akun tersisa %d, mau 0", len(accs))
	}
}
