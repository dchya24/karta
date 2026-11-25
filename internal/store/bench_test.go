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
	accID, err := db.AddAccount("bench@contoh.id", "imap", 993, true, "smtp", 465, true, "")
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

// seedRealisticBenchDB membuat 10.000 email dengan distribusi kosakata luas
// (~200 kata unik lintas topik kerja, tagihan, deploy, dukungan, pribadi)
// sehingga hasil FTS5 mencerminkan selektivitas korpus email nyata.
func seedRealisticBenchDB(b *testing.B, n int) (*DB, int64) {
	b.Helper()
	db, err := Open(filepath.Join(b.TempDir(), "bench-real.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	accID, err := db.AddAccount("dev@karta.id", "imap", 993, true, "smtp", 465, true, "")
	if err != nil {
		b.Fatal(err)
	}
	inbox, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		b.Fatal(err)
	}
	topics := [][]string{
		{"deploy", "kubernetes", "produksi", "rollback", "latensi", "prometheus", "container", "pipeline", "staging", "manifest", "docker", "cluster"},
		{"invoice", "pembayaran", "pajak", "faktur", "transfer", "rekening", "jatuh", "tempo", "nominal", "kwitansi", "pelunasan", "bendahara"},
		{"sprint", "retrospective", "backlog", "roadmap", "estimasi", "blocker", "milestone", "deliverable", "desain", "review", "arsitektur", "rfc"},
		{"keamanan", "sertifikat", "tls", "rotasi", "kredensial", "oauth", "token", "audit", "kerentanan", "patch", "firewall", "enkripsi"},
		{"rekrutmen", "kandidat", "wawancara", "portofolio", "penawaran", "onboarding", "kontrak", "evaluasi", "referensi", "posisi", "engineer", "tim"},
		{"pelanggan", "tiket", "eskalasi", "insiden", "gangguan", "pemulihan", "sla", "laporan", "verifikasi", "notifikasi", "dukungan", "keluhan"},
		{"perjalanan", "penerbangan", "reservasi", "hotel", "boarding", "konfirmasi", "jadwal", "bandara", "bagasi", "itinerary", "reimburse", "biaya"},
		{"newsletter", "rilis", "golang", "sqlite", "performa", "benchmark", "memori", "kompilasi", "runtime", "optimasi", "artikel", "ringkasan"},
	}
	common := []string{"halo", "terima", "kasih", "mohon", "bantuan", "terlampir", "update", "hari", "minggu", "depan", "silakan", "cek", "detail", "berikut"}
	for i := 1; i <= n; i++ {
		t1 := topics[i%len(topics)]
		t2 := topics[(i/len(topics))%len(topics)]
		var body strings.Builder
		for p := 0; p < 5; p++ {
			for w := 0; w < 18; w++ {
				switch w % 4 {
				case 0:
					body.WriteString(common[(i+p*3+w)%len(common)])
				case 1, 2:
					body.WriteString(t1[(i*5+p*7+w)%len(t1)])
				default:
					body.WriteString(t2[(i*3+p*5+w)%len(t2)])
				}
				body.WriteByte(' ')
			}
			body.WriteString("\n\n")
		}
		// Sisipkan kata kunci jarang pada 1% email (~100 hasil di 10k).
		if i%100 == 0 {
			body.WriteString("rekonsiliasi migrasi-kuartal ")
		}
		subj := fmt.Sprintf("[%s] %s #%d", t1[0], t1[(i/3)%len(t1)], i)
		m := MessageHeader{
			FolderID: int(inbox), UID: i, MessageID: fmt.Sprintf("<r%d@bench>", i),
			Subject: subj, Sender: fmt.Sprintf("Rekan %d <rekan%d@karta.id>", i%120, i%120),
			SenderAddr: fmt.Sprintf("rekan%d@karta.id", i%120),
			Recipients: "dev@karta.id",
			Cc:         fmt.Sprintf("tim%d@karta.id", i%20),
			Date:       fmt.Sprintf("2026-11-%02dT%02d:%02d:00Z", (i%28)+1, i%24, i%60),
			Unread:     i%5 == 0, HasAttachment: i%9 == 0, Size: 2048 + i%4096,
		}
		if _, err := db.UpsertMessage(&m, &MessageBody{PlainText: body.String()}); err != nil {
			b.Fatal(err)
		}
	}
	return db, inbox
}

// BenchmarkSearch10kRealistic mengukur kueri FTS5 selektif (~1% hasil) dan
// kueri topik (~12% hasil) pada 10.000 email berkorpus realistis.
func BenchmarkSearch10kRealistic(b *testing.B) {
	db, inbox := seedRealisticBenchDB(b, 10000)
	b.Run("selective_2tok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			msgs, err := db.ListMessages(inbox, "rekonsiliasi migrasi")
			if err != nil {
				b.Fatal(err)
			}
			if len(msgs) != 100 {
				b.Fatalf("hasil=%d, mau 100", len(msgs))
			}
		}
	})
	b.Run("topic_2tok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			msgs, err := db.ListMessages(inbox, "deploy kubernetes")
			if err != nil {
				b.Fatal(err)
			}
			if len(msgs) == 0 {
				b.Fatal("hasil kosong")
			}
		}
	})
}
