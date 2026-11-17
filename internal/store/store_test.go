package store

import "testing"

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateAndAccountCRUD(t *testing.T) {
	db := openTestDB(t)
	id, err := db.AddAccount("saya@contoh.id", "imap.contoh.id", 993, true,
		"smtp.contoh.id", 465, true)
	if err != nil {
		t.Fatal(err)
	}
	accs, err := db.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].Email != "saya@contoh.id" || accs[0].ID != int(id) {
		t.Fatalf("akun %+v", accs)
	}
	// Email unik.
	if _, err := db.AddAccount("saya@contoh.id", "x", 1, false, "x", 1, false); err == nil {
		t.Fatal("email ganda harus gagal")
	}
}

func TestFoldersAndMessages(t *testing.T) {
	db := openTestDB(t)
	accID, _ := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true)
	inbox, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	if inbox2, _ := db.EnsureFolder(accID, "INBOX", "inbox"); inbox2 != inbox {
		t.Fatalf("folder id tidak stabil: %d vs %d", inbox, inbox2)
	}

	m1 := MessageHeader{FolderID: int(inbox), UID: 1, MessageID: "<1@x>",
		Subject: "Laporan penjualan", Sender: "Budi <budi@contoh.id>",
		SenderAddr: "budi@contoh.id", Date: "2026-11-17T01:00:00Z", Unread: true}
	m2 := MessageHeader{FolderID: int(inbox), UID: 2, MessageID: "<2@x>",
		Subject: "Kata sandi sementara", Sender: "Noreply <no@svc.id>",
		SenderAddr: "no@svc.id", Date: "2026-11-17T02:00:00Z", Unread: true}
	for _, m := range []MessageHeader{m1, m2} {
		if _, err := db.UpsertMessage(&m, &MessageBody{
			PlainText: "isi " + m.Subject + " dengan kata kunci penawaran"}); err != nil {
			t.Fatal(err)
		}
	}

	folders, err := db.ListFolders(accID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Unread != 2 {
		t.Fatalf("folder %+v", folders)
	}

	msgs, err := db.ListMessages(inbox, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].UID != 2 {
		t.Fatalf("urutan pesan salah: %+v", msgs)
	}

	// Pencarian FTS5: prefix match, skor tidak penting di sini.
	hit, err := db.ListMessages(inbox, "lapor")
	if err != nil {
		t.Fatal(err)
	}
	if len(hit) != 1 || hit[0].Subject != "Laporan penjualan" {
		t.Fatalf("hasil cari %+v", hit)
	}
	hit, err = db.ListMessages(inbox, "penawaran")
	if err != nil {
		t.Fatal(err)
	}
	if len(hit) != 2 {
		t.Fatalf("cari body: %+v", hit)
	}
	if got, _ := db.ListMessages(inbox, "tidak-ada)(&"); len(got) != 0 {
		t.Fatalf("query liar harus aman: %+v", got)
	}
}

func TestGetMessageMarkReadAndBody(t *testing.T) {
	db := openTestDB(t)
	accID, _ := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true)
	inbox, _ := db.EnsureFolder(accID, "INBOX", "inbox")
	id, err := db.UpsertMessage(&MessageHeader{FolderID: int(inbox), UID: 7,
		Subject: "s", Date: "2026-11-17T00:00:00Z", Unread: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, b, err := db.GetMessage(id)
	if err != nil {
		t.Fatal(err)
	}
	if b.Fetched || b.PlainText != "" {
		t.Fatalf("body harus belum di-fetch: %+v", b)
	}
	if err := db.SetMessageBody(id, &MessageBody{PlainText: "teks bersih",
		RawHTML: "<p>teks bersih</p>"}); err != nil {
		t.Fatal(err)
	}
	_, b, err = db.GetMessage(id)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Fetched || b.PlainText != "teks bersih" {
		t.Fatalf("body %+v", b)
	}
	// Pencarian menemukan body lazy.
	hits, err := db.ListMessages(inbox, "teks")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits %+v", hits)
	}
	if err := db.MarkRead(id, true); err != nil {
		t.Fatal(err)
	}
	h, _, _ = db.GetMessage(id)
	if h.Unread {
		t.Fatal("harus sudah dibaca")
	}
	folders, _ := db.ListFolders(accID)
	if folders[0].Unread != 0 {
		t.Fatalf("unread %d", folders[0].Unread)
	}
}
