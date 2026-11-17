package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// seedBenchDB membuat DB file dengan n pesan bergaya email nyata
// (subject pendek, body ~1KB), lalu mengembalikan DB-nya.
func seedBenchDB(b *testing.B, n int) *DB {
	b.Helper()
	db, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	accID, err := db.AddAccount("bench@contoh.id", "imap", 993, true, "smtp", 465, true)
	if err != nil {
		b.Fatal(err)
	}
	inbox, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		b.Fatal(err)
	}
	words := []string{"laporan", "penawaran", "rapat", "kwitansi", "kontrak",
		"proyek", "jadwal", "penawaran", "gaji", "invoice", "survei", "umpan balik"}
	for i := 1; i <= n; i++ {
		// Realistis: tiap pesan hanya memuat subset kata + satu token unik
		// (kosakata email nyata bervariasi per pesan).
		var body strings.Builder
		for w := 0; w < 120; w++ {
			body.WriteString(words[(i*7+w*3)%len(words)])
			body.WriteByte(' ')
		}
		fmt.Fprintf(&body, "token%d referensi%d", i, i)
		subj := fmt.Sprintf("%s %d", words[i%len(words)], i)
		m := MessageHeader{
			FolderID: int(inbox), UID: i, MessageID: fmt.Sprintf("<%d@bench>", i),
			Subject: subj, Sender: fmt.Sprintf("Pengirim %d <p%d@contoh.id>", i%50, i%50),
			SenderAddr: fmt.Sprintf("p%d@contoh.id", i%50),
			Recipients: "bench@contoh.id",
			Date:       fmt.Sprintf("2026-11-%02dT%02d:00:00Z", (i%28)+1, i%24),
			Unread:     i%3 != 0, HasAttachment: i%7 == 0, Size: 1024 + i,
		}
		if _, err := db.UpsertMessage(&m, &MessageBody{PlainText: body.String()}); err != nil {
			b.Fatal(err)
		}
	}
	return db
}

// BenchmarkListMessages10k mengukur daftar penuh folder 10.000 pesan.
func BenchmarkListMessages10k(b *testing.B) {
	db := seedBenchDB(b, 10000)
	var folderID int64
	if err := db.QueryRow(`SELECT id FROM folders WHERE role='inbox'`).Scan(&folderID); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgs, err := db.ListMessages(folderID, "")
		if err != nil {
			b.Fatal(err)
		}
		if len(msgs) != 10000 {
			b.Fatalf("pesan %d", len(msgs))
		}
	}
}

// BenchmarkSearch10k mengukur pencarian FTS5 di folder 10.000 pesan.
func BenchmarkSearch10k(b *testing.B) {
	db := seedBenchDB(b, 10000)
	var folderID int64
	if err := db.QueryRow(`SELECT id FROM folders WHERE role='inbox'`).Scan(&folderID); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgs, err := db.ListMessages(folderID, "laporan kwitansi")
		if err != nil {
			b.Fatal(err)
		}
		if len(msgs) == 0 {
			b.Fatal("hasil kosong")
		}
	}
}

// BenchmarkSearch10kSingle mengukur pencarian satu token.
func BenchmarkSearch10kSingle(b *testing.B) {
	db := seedBenchDB(b, 10000)
	var folderID int64
	if err := db.QueryRow(`SELECT id FROM folders WHERE role='inbox'`).Scan(&folderID); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.ListMessages(folderID, "kontrak"); err != nil {
			b.Fatal(err)
		}
	}
}
