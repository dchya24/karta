# REPORT: Thread/Conversation Grouping di Message List (Karta)

**Status: DONE**

Branch: `feat/thread-grouping` — implementasi penuh sesuai BRIEF.md: identitas thread dari header reply (fallback subject), migrasi + backfill di store, agregasi per-thread pada query list, DTO app, dan rendering 1-baris-per-thread + badge di UI. Test lulus (termasuk `-race`), build & vet bersih, `CGO_ENABLED=0`.

---

## 1. Skema `thread_id`

Migrasi v4 (`internal/store/migrate.go`), hanya ditambahkan (tidak mengubah migrasi lama):

```sql
ALTER TABLE messages ADD COLUMN thread_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN refs TEXT NOT NULL DEFAULT '';   -- header References mentah
CREATE INDEX idx_messages_thread ON messages(folder_id, thread_id, date DESC, uid DESC);
```

- `thread_id` = kunci grup **per-folder** (hasil `ThreadSeedKey`/`RegroupThreads`), bukan global.
- `refs` menyimpan header `References` mentah; `in_reply_to` sudah ada sejak v1, kini ikut terisi dari sync.
- Backfill: setelah migrasi v4 diterapkan, `backfillThreadIDs()` menjalankan `RegroupThreads` untuk semua folder dalam satu transaksi (atomic, gagal = rollback).

## 2. Keputusan desain

### Identitas thread (`internal/store/thread.go`)
- **Sumber utama:** rantai `References` + `In-Reply-To` → Message-ID dinormalisasi (trim `<>`, lowercase, case-fold). `References` dipisah per token; `In-Reply-To` hanya dipakai bila `References` kosong; self-reference & duplikat dibuang.
- **Fallback:** subject dinormalisasi (`NormalizeThreadSubject`: strip `Re:`/`Fwd:`/`Fw:` berulang termasuk `Re[2]:`, case-insensitive, lipat spasi) + folder — **hanya** untuk pesan tanpa header reply sama sekali. Prefix hanya di-strip bila diikuti `:` (`restart: x` tidak ikut).
- **Per-folder:** kunci fallback berformat `s<folderID>:<subject>`; kunci Message-ID tidak perlu diberi folder karena regroup selalu dijalankan per folder (query memfilter `folder_id`).
- **Regroup = union-find** atas seluruh kunci thread folder. Pesan terhubung bila saling merujuk. `thread_id` komponen = Message-ID normalized terkecil di komponen (atau kunci fallback terkecil) → **deterministik terhadap urutan masuk** (reply yang tiba sebelum root tetap bergabung) dan **idempoten**.
- **Singleton:** pesan tanpa identitas apa pun (tanpa Message-ID/refs/subject) diberi kunci unik `u<id>`/`u<folder>:<uid>` sejak insert — tidak pernah tergrup dengan pesan kosong lain.

### Penyimpanan & query (`internal/store/queries.go`)
- `UpsertMessage`: `thread_id` dihitung saat insert (`ThreadSeedKey`); upsert ulang **tidak menimpa** `thread_id` yang sudah ada, dan header reply lama dipertahankan bila sync datang tanpa refs.
- `ListThreadSummaries(folderID, query)`: tanpa query → **1 baris per thread** = pesan terbaru thread (`IsThreadLatest=true`) + `ThreadCount` anggota, lewat subquery `GROUP BY thread_id` + correlated subquery `LIMIT 1` yang keduanya terjawab oleh `idx_messages_thread` (tanpa full scan di luar folder). Dengan query FTS5 → delegasi ke `ListMessages` (hasil pencarian tetap flat per pesan).
- `SetMessageThreadHeaders(id, ...)`: dipanggil saat body di-fetch lazy — mengisi `message_id`/`in_reply_to`/`refs` yang masih kosong dari hasil parsing raw, lalu **regroup folder** dalam satu transaksi (header baru bisa menyatukan/memecah thread).
- `scanHeaderRow` disatukan untuk semua query header; query thread menambah 2 kolom agregasi (`t.cnt`, `1`) di belakang `headerCols`.

### App DTO (`internal/app/types.go`, `service.go`)
- `MessageHeader` DTO + `ThreadID`, `ThreadCount`, `IsThreadLatest` (JSON-friendly, `omitempty`).
- `ListThreads(folderID, query)` = facade baru untuk UI; `ListMessages` tetap flat (tidak dirusak untuk pemakai lain).
- `refreshAccount`: menyimpan `InReplyTo` + `Refs` dari sync header, dan **regroup tiap folder setelah sync batch** (test: reply tiba sebelum root → tetap bergabung).
- `fetchAndStoreBody`: setelah parse raw, panggil `SetMessageThreadHeaders` (References tidak selalu ada di sync header; terisi saat lazy fetch).

### IMAP fetch (`internal/mail/imap.go`)
- `SyncFolder` kini ikut `FETCH BODY.PEEK[HEADER.FIELDS (References)]` per batch (500 msg) — References tidak ada di Envelope. Header diparse via `go-message/textproto` (case-insensitive), tanpa dependensi baru.
- `parser.go`: `Message.MessageID/InReplyTo/References` diekstrak dari header mentah (normalisasi tetap di `internal/store`).

### UI (`internal/ui/app.go`)
- `reloadMessages` memakai `svc.ListThreads`: tanpa query = 1 baris per thread (barisnya pesan terbaru → tanggal baris otomatis pesan terbaru); dengan query = hasil pencarian flat. Virtualisasi `ui.List` tidak diubah (jumlah item berkurang, iterasi tetap sama).
- `row`: `ui.Badge` (widget yang sama dengan badge unread sidebar) menampilkan jumlah anggota bila thread >1 pesan; klik baris membuka pesan terbaru thread (baris memang ID pesan terbaru — tidak ada perubahan jalur klik).
- Tidak ada query SQL di `internal/ui`; semua via `internal/app.Service`.

## 3. Daftar commit (branch `feat/thread-grouping`)

| Commit | Isi |
|---|---|
| `24220bf` | store: identitas thread + migrasi/backfill + query agregasi per-thread |
| `61c41df` | mail: ekstrak header identitas thread (References/In-Reply-To/Message-ID) |
| `5165000` | app: DTO field thread + ListThreads + lengkapi header saat lazy fetch |
| `7d870d1` | ui: message list tampil sebagai thread (1 baris per percakapan + badge) |
| `2bcd9ca` | ui: perbaiki import strings pada test thread |
| `f68c638` | app: regroup thread setelah tiap sync folder |
| `57c3744` | store: pesan tanpa identitas jadi singleton sejak insert |

## 4. File berubah

- `internal/store/migrate.go`, `queries.go`, `thread.go` (baru), `thread_test.go` (baru)
- `internal/mail/parser.go`, `parser_test.go`, `imap.go`, `imap_test.go`
- `internal/app/types.go`, `service.go`, `thread_test.go` (baru)
- `internal/ui/app.go`, `thread_test.go` (baru)
- `go.mod`/`go.sum`: **tidak berubah** (tanpa dependensi baru). `internal/sync` tidak disentuh.

## 5. Hasil test

```
go build ./cmd/karta                  (CGO_ENABLED=0)  → OK
go vet ./...                                           → bersih
go test ./internal/mail/... ./internal/store/... ./internal/app/... ./internal/ui/... → ok (semua)
go test -race ./internal/app/... ./internal/store/...  → ok (tanpa race)
go test -race ./internal/mail/... ./internal/ui/... ./internal/sync/... → ok
```

Cakupan test baru (semua tanpa GUI): normalisasi Message-ID & subject (tabel kasus), `ThreadSeedKey` (root rantai, In-Reply-To, fallback subject, per-folder), regroup rantai References (3-baris → 1 thread, idempoten), order-independence (4 urutan insert), per-folder (subject sama di folder beda tidak menyatu), pencarian FTS tetap flat, `SetMessageThreadHeaders` (isi kosong + tidak menimpa + regroup + error pesan hilang), migrasi v4 + backfill (db skema v3 dibangun manual lalu dibuka lewat `Open`), parser header thread, `headerFromFetch` References, DTO `ListThreads` (urutan, count, UID terbaru, JSON valid), regroup pasca-sync (reply dulu, root menyusul), singleton tanpa identitas, dan test headless `ui.Tester` untuk 1-baris-per-thread + badge + klik-membuka-pesan-terbaru.

## 6. Self-review

- **Kekhawatiran kecil (non-blocking):**
  1. `SetMessageThreadHeaders` menjalankan regroup full-folder tiap kali body di-fetch lazy. Pada folder sangat besar (puluhan ribu pesan) ini adalah satu pass index-scan per pesan yang dibuka pertama kali; masih hitungan ms–puluhan ms dan hanya terjadi sekali per pesan (setelah `fetched`, kolom terisi). Bila jadi beban, regroup bisa diganti incremental-union.
  2. Badge thread tidak menandai *jumlah unread* dalam thread (baris = pesan terbaru, dot unread mengikuti pesan terbaru). Di luar scope brief — brief hanya minta badge jumlah pesan.
- **Kepatuhan batasan:** tidak ada impor `mygo`/`internal/ui` di `internal/mail`/`internal/store`; tidak ada SQL di `internal/ui`; DTO app JSON-friendly (siap diexpose ke bridge); tanpa dependensi baru; `internal/sync` tidak disentuh.
- **Catatan proses:** sebagian kode store (thread.go, migrasi, query) sudah ada sebagai perubahan un-committed di worktree saat task dimulai; diperbaiki (bug test migrasi, kolom ambigu pada query join, perilaku lazy-header yang tidak menyetukan rantai) dulu, diuji, baru di-commit sebagai basis.
