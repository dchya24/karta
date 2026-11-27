package store

import (
	"bytes"
	"testing"
)

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
		"smtp.contoh.id", 465, true, "")
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
	if _, err := db.AddAccount("saya@contoh.id", "x", 1, false, "x", 1, false, ""); err == nil {
		t.Fatal("email ganda harus gagal")
	}
}

func TestFoldersAndMessages(t *testing.T) {
	db := openTestDB(t)
	accID, _ := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true, "")
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
	accID, _ := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true, "")
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

func TestCcMoveAndDeleteMessage(t *testing.T) {
	db := openTestDB(t)
	accID, _ := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	inbox, _ := db.EnsureFolder(accID, "INBOX", "inbox")
	archive, _ := db.EnsureFolder(accID, "Archive", "archive")

	// Seed pesan lain di Archive dengan UID sama (7) untuk menguji anti-tabrakan UID.
	if _, err := db.UpsertMessage(&MessageHeader{FolderID: int(archive), UID: 7,
		Subject: "lama", Date: "2026-11-16T00:00:00Z"}, &MessageBody{PlainText: "lama"}); err != nil {
		t.Fatal(err)
	}
	id, err := db.UpsertMessage(&MessageHeader{
		FolderID: int(inbox), UID: 7, Subject: "rapat",
		Recipients: "aku@contoh.id", Cc: "tim@contoh.id", Date: "2026-11-17T00:00:00Z",
	}, &MessageBody{PlainText: "agenda"})
	if err != nil {
		t.Fatal(err)
	}
	// Cc ikut terindeks FTS5.
	hits, err := db.ListMessages(inbox, "tim")
	if err != nil || len(hits) != 1 || hits[0].Cc != "tim@contoh.id" {
		t.Fatalf("cari Cc: %+v err=%v", hits, err)
	}
	f, err := db.FolderByRole(accID, "archive")
	if err != nil || int64(f.ID) != archive {
		t.Fatalf("FolderByRole: %+v err=%v", f, err)
	}
	if err := db.MoveMessage(id, archive, 0); err != nil {
		t.Fatal(err)
	}
	if inMsgs, _ := db.ListMessages(inbox, ""); len(inMsgs) != 0 {
		t.Fatalf("inbox harus kosong: %+v", inMsgs)
	}
	if arMsgs, _ := db.ListMessages(archive, ""); len(arMsgs) != 2 {
		t.Fatalf("archive harus berisi 2 pesan: %+v", arMsgs)
	}
	if err := db.DeleteMessage(id); err != nil {
		t.Fatal(err)
	}
	if hits, _ := db.ListMessages(archive, "agenda"); len(hits) != 0 {
		t.Fatalf("FTS harus terhapus: %+v", hits)
	}
}

func TestListAttachmentBlobs(t *testing.T) {
	db := openTestDB(t)
	accID, _ := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	folder, _ := db.EnsureFolder(accID, "INBOX", "inbox")
	id, err := db.UpsertMessage(&MessageHeader{
		FolderID: int(folder), UID: 1, Subject: "lampiran", SenderAddr: "b@x.id",
	}, &MessageBody{PlainText: "isi"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAttachment(id, "biner.bin", "application/octet-stream", []byte{0, 1, 2, 255}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAttachment(id, "kosong.txt", "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	atts, err := db.ListAttachmentBlobs(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 {
		t.Fatalf("jumlah lampiran = %d, mau 2", len(atts))
	}
	if atts[0].Filename != "biner.bin" || atts[0].ContentType != "application/octet-stream" {
		t.Fatalf("lampiran 1: %+v", atts[0])
	}
	if !bytes.Equal(atts[0].Data, []byte{0, 1, 2, 255}) {
		t.Fatalf("isi blob: %v", atts[0].Data)
	}
	if atts[1].Filename != "kosong.txt" || atts[1].Data != nil {
		t.Fatalf("lampiran tanpa blob: %+v", atts[1])
	}
	if atts, err := db.ListAttachmentBlobs(id + 999); err != nil || len(atts) != 0 {
		t.Fatalf("pesan tanpa lampiran: %+v err=%v", atts, err)
	}
}
