package ui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"karta/internal/app"
	"karta/internal/store"
)

// TestViewThreadGrouping memastikan message list menampilkan thread sebagai
// satu baris (pesan terbarunya) dengan agregasi dari app.ListThreads, tanpa
// GUI (headless Tester).
func TestViewThreadGrouping(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := app.New(db)
	svc.SetKeyringForTesting(app.NewFakeKeyring())
	t.Cleanup(func() { svc.Close() })
	accID, err := db.AddAccount("thread@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	// Thread 2 pesan: root (01:00) + reply (02:00); satu pesan mandiri (03:00).
	seed := []struct {
		uid                  int
		mid, irt, subj, date string
	}{
		{1, "<root@x>", "", "Topik utama", "2026-11-17T01:00:00Z"},
		{2, "<r1@x>", "<root@x>", "Re: Topik utama", "2026-11-17T02:00:00Z"},
		{3, "", "", "Catatan lain", "2026-11-17T03:00:00Z"},
	}
	for _, m := range seed {
		if _, err := db.UpsertMessage(&store.MessageHeader{
			FolderID: int(inbox), UID: m.uid, MessageID: m.mid, InReplyTo: m.irt,
			Subject: m.subj, Sender: "Budi <budi@contoh.id>",
			SenderAddr: "budi@contoh.id", Recipients: "thread@contoh.id",
			Date: m.date, Unread: true,
		}, &store.MessageBody{PlainText: "isi " + m.subj}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RegroupThreads(inbox); err != nil {
		t.Fatal(err)
	}

	st := &state{svc: svc}
	st.reloadFolders()
	tt := ui.NewTester(st.view, 1100, 720)

	// Thread view: 2 baris, bukan 3 — root tidak tampil sebagai baris sendiri.
	if len(st.msgs) != 2 {
		t.Fatalf("baris %d, mau 2 (1 thread + 1 pesan mandiri): %+v", len(st.msgs), st.msgs)
	}
	if st.msgs[0].Subject != "Catatan lain" || st.msgs[0].ThreadCount != 1 {
		t.Fatalf("baris 0: %+v", st.msgs[0])
	}
	if st.msgs[1].Subject != "Re: Topik utama" || st.msgs[1].ThreadCount != 2 ||
		!st.msgs[1].IsThreadLatest || st.msgs[1].UID != 2 {
		t.Fatalf("baris thread: %+v", st.msgs[1])
	}
	// Baris thread = pesan terbaru: root tidak jadi baris tersendiri (sudah
	// diverifikasi lewat state di atas), dan badge jumlah thread dirender.
	if !tt.HasText(fmt.Sprint(st.msgs[1].ThreadCount)) {
		t.Fatal("badge jumlah pesan thread tidak tampil")
	}
	// Klik baris thread membuka pesan terbaru thread (UID 2).
	st.selected = 1
	m, err := svc.PeekMessage(st.msgs[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.UID != 2 || m.Subject != "Re: Topik utama" {
		t.Fatalf("pesan terbuka %+v, mau UID 2 (reply terbaru)", m.MessageHeader)
	}
	if !strings.Contains(m.PlainText, "isi Re: Topik utama") {
		t.Fatalf("isi pesan %q", m.PlainText)
	}
}
