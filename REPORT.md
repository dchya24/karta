# Laporan Implementasi — Batch Paralel (2026-01)

Dua fitur dikerjakan paralel di dua worktree (branch terpisah), lalu di-merge ke master.

## 1. Notifikasi OS pesan baru (feat/desktop-notify)
- Status: DONE
- Library: API native mygo (`NewNotification` + `ShowContext`) — nol dependensi baru, build `CGO_ENABLED=0` tetap lolos.
- Desain: hook di `tryRefreshAccount` setelah header baru tersimpan; backfill awal sesi hening; dedupe per sesi (Message-ID / akun+folder+UID); hanya folder inbox; error hanya di-log.
- Test: 8 test baru (fake Notifier): backfill suppression, dedupe, konkurensi, inbox-only, bentuk opsi. `go test -race` sync+app lulus, `go vet` bersih.
- Catatan (ditindaklanjuti): pengiriman notifikasi kini asinkron via satu goroutine antrean FIFO (siklus sync tidak menunggu); error notifikasi di macOS diberi hint "hanya tampil saat app ter-package sebagai .app". Limitasi macOS dev tetap ada (sifat sistem notifikasi macOS), tapi kini ter-log jelas.

## 2. Thread grouping message list (feat/thread-grouping)
- Status: DONE
- Desain: identitas thread dari `References`/`In-Reply-To` (normalisasi Message-ID), fallback subject dinormalisasi, per-folder; singleton untuk pesan tanpa identitas.
- Store: migrasi v4 (`thread_id`, refs, index) + backfill + `RegroupThreads` (union-find deterministik) + `ListThreadSummaries`; regroup pasca-sync.
- UI: 1 baris per thread + badge count, virtualisasi `ui.List` tidak diubah.
- Test: normalisasi, rantai References, order-independence, per-folder, migrasi+backfill, regroup, singleton, headless UI. `go test -race` lulus.

## Merge
- Konflik di `internal/app/service.go` diselesaikan: `RegroupThreads` dulu, lalu `notifyNewMessages`.
- DoD penuh dijalankan ulang setelah merge (build no-cgo, vet, test race).


---


# Laporan: Sinkronisasi Folder Dinamis (feat/dynamic-folders)

## Status
**DONE** — seluruh poin brief terimplementasi, semua DoD lulus.

## Keputusan Implementasi
1. **Tempat pengurutan folder**: dipilih **satu tempat** — `app.Service.ListFolders`
   (`internal/app/service.go`), karena UI (`internal/ui/app.go`) langsung mengonsumsi
   `ListFolders` dan urutan yang sama dibutuhkan oleh loop sync. Fungsi kunci
   `syncOrder(role, name)` bersama `roleRank` (inbox=0, drafts=1, sent=2,
   archive=3, trash=4, junk=5; non-core = 6) dipakai dua-duanya, jadi sidebar dan
   urutan sinkronisasi **dijamin konsisten dari satu sumber**.
2. **`tryRefreshAccount`**: semua mailbox hasil `ListMailboxes` di-`EnsureFolder`
   lalu disinkron. Loop sebelumnya memakai `map[role]folderID` (hanya core, dan
   menimpa folder kedua dengan role sama) — sekarang slice folder yang diurutkan
   (`sort.Slice` dengan `syncOrder`), sehingga beberapa folder non-core ikut sync.
   Junk kini ikut disinkron tiap siklus penuh (sebelumnya tidak, karena tidak ada
   di `coreRoles`).
3. **`coreRoles` tidak dihapus**: masih dipakai `idleFolders` (`internal/app/idle.go`)
   untuk pemilihan folder IDLE watch (push realtime). Brief tidak meminta perubahan
   IDLE ("tidak ada polling terpisah untuk folder non-core"), jadi perilaku IDLE
   dipertahankan — hanya komentarnya diperbarui agar tidak menyesatkan.
4. **Regroup thread & notifikasi tidak berubah**: `RegroupThreads` tetap dipanggil
   per folder setelah sync; `notifyNewMessages` tetap hanya saat `role == "inbox"`.
5. **Dedupe guard**: tidak ada perubahan di `internal/mail` — `SyncFolder(ctx,
   mailbox, lastUID)` sudah generic per mailbox (SELECT, UID SEARCH UID>last,
   fetch batch). Tidak ada asumsi khusus core role yang ditemukan, jadi
   `internal/mail/imap.go` tidak disentuh (bug `mailboxRole` tidak ditemukan).
6. **Sidebar & label**: `internal/ui/app.go` tidak perlu diubah. `folderLabel`
   sudah memangkas prefix `[Gmail]/`, `[Google Mail]/`, `INBOX.`, `INBOX/` untuk
   folder non-core (terverifikasi test headless `TestViewBuilds`: label
   "Important" tampil, "[Gmail]" tidak). Seleksi folder default di `reloadFolders`
   memilih folder pertama — kini otomatis Inbox karena rank inbox paling awal.
7. **Dependensi**: nol dependensi baru. Hanya `sort` (stdlib) ditambahkan.

## Commit
- `489ab64` — `app: sinkronisasi semua folder IMAP, bukan hanya folder inti`

## File Berubah
- `internal/app/service.go` — loop sync semua folder + urutan; `ListFolders`
  diurutkan; helper `roleRank`/`syncOrder`; komentar `coreRoles` diperbarui.
- `internal/app/dynfolders_test.go` (baru) — 4 test (rincian di bawah).

## Test
Baru (`internal/app/dynfolders_test.go`, stub `folderedIMAP` per-mailbox dengan
catatan urutan pemanggilan):
1. `TestRefreshSyncsNonCoreFolders` — folder non-core ikut di-sync: header masuk
   store, `last_seen_uid` maju ke 7, **unread terhitung** (DoD a & d).
2. `TestRefreshSyncOrder` — urutan sync `INBOX, Drafts, Sent, Archive, Trash,
   Junk, Alfa, Zulu` (core dulu sesuai rank, non-core alfabetis) (DoD b).
3. `TestRefreshSyncDedupeGuard` — dua siklus: siklus kedua menerima
   `lastUID = max siklus pertama` per folder (INBOX=3, Label/Klien=9), tidak ada
   duplikasi pesan, unread tetap benar (DoD a + guard dedupe).
4. `TestListFoldersSidebarOrder` — urutan folder untuk sidebar konsisten dengan
   urutan sync (DoD c).

Hasil lengkap (DoD):
- `CGO_ENABLED=0 go build ./cmd/karta` ✅
- `go vet ./...` bersih ✅
- `go test ./internal/...` semua paket `ok` (app, mail, store, sync, ui) ✅
- `go test -race ./internal/app/... ./internal/store/...` ✅

## Self-Review
- Batasan layer: hanya `internal/app` yang diubah; tidak ada impor `internal/ui`
  ke `mail`/`store`; tidak ada SQL di `internal/ui`; error wrapping `%w`;
  konteks tetap mengalir. ✅
- YAGNI: tidak ada fitur di luar brief (IDLE/polling non-core tidak ditambahkan).
- Dua iterasi perbaikan test sendiri: (1) asersi dedupe awal salah membandingkan
  lastUID siklus pertama vs kedua; (2) asersi unread tidak memperhitungkan pesan
  non-core yang sudah dibaca. Keduanya bug test, bukan bug implementasi.
- Catatan gofmt: `internal/app/types.go` dan `internal/store/thread.go` tidak
  gofmt-rapi **pra-eksisting** (tidak saya sentuh, di luar lingkup brief).

## Kekhawatiran
1. **Folder non-core berpotensi memperlambat siklus penuh** pada akun dengan
   banyak folder besar (semua di-sync sekali per siklus `refreshAccount`, sesuai
   desain brief — batas waktu `perAccountTimeout` 2 menit per akun berlaku).
   Tidak mitigasi di sini karena desain mengikuti brief.
2. **Junk kini ikut sync** tiap siklus (bagian dari urutan core di brief) —
   pada server dengan folder spam besar ini menambah beban siklus; perilaku
   diminta eksplisit oleh brief.
3. IDLE hanya memantau folder inti; perubahan di folder non-core baru terlihat
   pada siklus polling berikutnya — sesuai desain, dicatat agar tidak salah paham.
