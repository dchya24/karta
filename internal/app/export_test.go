package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karta/internal/store"
)

// seedExportDB menyiapkan satu akun dengan dua folder: INBOX berisi pesan
// ber-body + lampiran dan pesan fetched=0; Archive berisi satu pesan fetched=0.
func seedExportDB(t *testing.T, svc *Service) (inboxID, archiveID int64) {
	t.Helper()
	ctx := context.Background()
	id, err := svc.AddAccount(ctx, AccountConfig{
		Email: "aku@contoh.id", Password: "rahasia",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inboxID, err = svc.db.EnsureFolder(int64(id), "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	archiveID, err = svc.db.EnsureFolder(int64(id), "Archive", "archive")
	if err != nil {
		t.Fatal(err)
	}
	// Pesan INBOX lengkap: body + baris amplop palsu + lampiran berisi BLOB.
	mid, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(inboxID), UID: 7, MessageID: "m1@contoh.id",
		Subject: "Salam dari Karta", Sender: "Pengirim", SenderAddr: "pengirim@contoh.id",
		Recipients: "aku@contoh.id", Cc: "teman@contoh.id",
		Date: "2026-02-05T09:30:00Z", HasAttachment: true, Size: 123,
	}, &store.MessageBody{PlainText: "Halo,\nFrom aku\nIni isi pesan.", RawHTML: "<p>Halo</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.db.InsertAttachment(mid, "laporan.txt", "text/plain", []byte("isi lampiran")); err != nil {
		t.Fatal(err)
	}
	// Pesan INBOX kedua: header saja (fetched=0, body belum di-cache).
	if _, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(inboxID), UID: 8, MessageID: "m2@contoh.id",
		Subject: "Belum di-fetch", SenderAddr: "orang@contoh.id",
		Recipients: "aku@contoh.id", Date: "2026-02-06T10:00:00Z",
	}, nil); err != nil {
		t.Fatal(err)
	}
	// Pesan Archive: header saja, subjek tak beraturan untuk slug.
	if _, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(archiveID), UID: 3, MessageID: "m3@contoh.id",
		Subject: "Arsip: Laporan Q1!", SenderAddr: "orang@contoh.id",
		Recipients: "aku@contoh.id", Date: "2026-02-07T08:00:00Z",
	}, nil); err != nil {
		t.Fatal(err)
	}
	return inboxID, archiveID
}

func TestExportMbox(t *testing.T) {
	svc := newTestService(t)
	seedExportDB(t, svc)
	dir := t.TempDir()
	results, err := svc.Export(context.Background(), ExportOptions{Format: "mbox", Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("hasil = %+v, mau 2 folder", results)
	}
	paths := map[string]ExportResult{}
	for _, r := range results {
		if r.Error != "" {
			t.Fatalf("folder %s gagal: %s", r.FolderName, r.Error)
		}
		paths[r.FolderRole] = r
	}
	// Nama file mbox: <email>--<role>.mbox.
	inboxPath := filepath.Join(dir, "aku@contoh.id--inbox.mbox")
	if paths["inbox"].Path != inboxPath {
		t.Fatalf("path inbox = %q", paths["inbox"].Path)
	}
	data, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// Dua entri (baris amplop muncul 2x); urutan date DESC, jadi entri
	// pertama bisa pesan mana pun.
	if n := strings.Count(s, "\nFrom "); n != 1 {
		t.Fatalf("baris amplop = %d (di luar entri pertama), isi:\n%s", n, s)
	}
	if !strings.HasPrefix(s, "From ") {
		t.Fatalf("amplop pertama salah:\n%s", s)
	}
	for _, want := range []string{"Subject: Salam dari Karta", "Subject: Belum di-fetch",
		">From aku", "Content-Disposition: attachment", "filename=laporan.txt"} {
		if !strings.Contains(s, want) {
			t.Fatalf("%q tidak ada di mbox:\n%s", want, s)
		}
	}
	if paths["inbox"].Count != 2 {
		t.Fatalf("hitungan inbox = %d, mau 2", paths["inbox"].Count)
	}
	// Pesan fetched=0 diekspor tanpa body: tidak ada body teks miliknya.
	if !strings.Contains(s, "Subject: Belum di-fetch") {
		t.Fatal("pesan fetched=0 tidak diekspor")
	}
	// Folder archive: satu pesan fetched=0.
	archData, err := os.ReadFile(filepath.Join(dir, "aku@contoh.id--archive.mbox"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archData), "Subject: Arsip: Laporan Q1!") {
		t.Fatalf("isi archive:\n%s", archData)
	}
	if paths["archive"].Count != 1 {
		t.Fatalf("hitungan archive = %d, mau 1", paths["archive"].Count)
	}
}

func TestExportEML(t *testing.T) {
	svc := newTestService(t)
	seedExportDB(t, svc)
	dir := t.TempDir()
	results, err := svc.Export(context.Background(), ExportOptions{Format: "eml", Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("hasil = %+v, mau 2 folder", results)
	}
	// INBOX: dua file; Archive: satu file dengan slug subjek.
	inboxDir := filepath.Join(dir, "inbox")
	ents, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range ents {
		names[e.Name()] = true
	}
	if !names["7-salam-dari-karta.eml"] || !names["8-belum-di-fetch.eml"] {
		t.Fatalf("file eml inbox: %v", names)
	}
	eml, err := os.ReadFile(filepath.Join(inboxDir, "7-salam-dari-karta.eml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"From: Pengirim <pengirim@contoh.id>",
		"Subject: Salam dari Karta", "Message-ID: <m1@contoh.id>",
		"Content-Type: multipart/mixed", "Content-Disposition: attachment; filename=laporan.txt"} {
		if !bytes.Contains(eml, []byte(want)) {
			t.Fatalf("%q tidak ada di eml:\n%s", want, eml)
		}
	}
	// Pesan fetched=0: file eml ada, body teks kosong.
	plain, err := os.ReadFile(filepath.Join(inboxDir, "8-belum-di-fetch.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain, []byte("Subject: Belum di-fetch")) {
		t.Fatalf("eml fetched=0:\n%s", plain)
	}
	if bytes.Count(plain, []byte("\r\n\r\n")) != 1 {
		t.Fatalf("eml fetched=0 punya body:\n%s", plain)
	}
	// Archive: direktori role + slug subjek dibersihkan.
	arch, err := os.ReadFile(filepath.Join(dir, "archive", "3-arsip-laporan-q1.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(arch, []byte("Subject: Arsip: Laporan Q1!")) {
		t.Fatalf("isi archive eml:\n%s", arch)
	}
}

func TestExportFilters(t *testing.T) {
	svc := newTestService(t)
	seedExportDB(t, svc)
	// Akun kedua untuk filter.
	id, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "kedua@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.db.EnsureFolder(int64(id), "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(other), UID: 1, Subject: "akun lain", SenderAddr: "x@y.id",
	}, nil); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// Filter akun: hanya akun kedua.
	results, err := svc.Export(context.Background(), ExportOptions{
		Directory: dir, AccountEmail: "kedua@contoh.id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].AccountEmail != "kedua@contoh.id" {
		t.Fatalf("filter akun: %+v", results)
	}
	// Filter role: hanya archive.
	results, err = svc.Export(context.Background(), ExportOptions{
		Directory: dir, FolderRole: "archive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].FolderRole != "archive" || results[0].Count != 1 {
		t.Fatalf("filter role: %+v", results)
	}
	// Tidak ada file mbox akun pertama--inbox karena hanya archive diekspor.
	if _, err := os.Stat(filepath.Join(dir, "aku@contoh.id--inbox.mbox")); err == nil {
		t.Fatal("folder terfilter ikut diekspor")
	}
}

func TestExportDefaultFormatAndDir(t *testing.T) {
	svc := newTestService(t)
	seedExportDB(t, svc)
	// Format kosong = mbox; tanpa panggilan MkdirAll terpisah di cwd test:
	// pakai subdirektori temp agar tidak menulis ke cwd.
	root := t.TempDir()
	results, err := svc.Export(context.Background(), ExportOptions{Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Error != "" || !strings.HasSuffix(r.Path, ".mbox") {
			t.Fatalf("default format: %+v", r)
		}
	}
}

func TestExportInvalidFormat(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Export(context.Background(), ExportOptions{Format: "json"}); err == nil {
		t.Fatal("format tidak dikenal harus error")
	}
}
