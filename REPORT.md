# REPORT: Notifikasi OS untuk Pesan Baru (Karta)

**Status: DONE**

Branch: `feat/desktop-notify` · Worktree: `/Works/Development/self-project/karta.worktrees/notify`

## Keputusan Library + Alasan

**Dipakai: API notifikasi native `github.com/egoist/mygo` v0.9.2 (sudah ada di go.mod). Tidak ada dependensi baru; `go.mod`/`go.sum` tidak berubah.**

Langkah 1 brief dijalankan lebih dulu: grep `notif` pada source mygo di module cache (`$GOMODCACHE/github.com/egoist/mygo@v0.9.2`) menemukan API notifikasi OS native yang lengkap:

- `mygo.NewNotification(mygo.NotificationOptions{...}) *Notification`
- `(*Notification).ShowContext(ctx)` — otomatis mendispatch ke main thread GUI via `postMain`, jadi **aman dipanggil dari goroutine sinkronisasi** (diverifikasi di source: path non-main-thread tidak pernah menyentuh widget; `Refresh` di codebase hanya pernah dipanggil dari goroutine — `internal/ui/app.go` `startSync` dan `refreshNow`)
- `mygo.ErrNotificationsDenied` (macOS), dukungan Linux (dbus) / Windows (toast) / macOS bawaan backend

Karena brief mensyaratkan "jika ada, pakai itu — jangan tambah dependensi", `beeep` **tidak** ditambahkan; risiko no-cgo dbus pun tidak muncul karena tidak ada library eksternal baru dan build tetap `CGO_ENABLED=0`.

## File Berubah

| File | Perubahan |
|---|---|
| `internal/app/notify.go` | **Baru.** Interface `Notifier`, DTO `NewMessage` (JSON-friendly), `sessionNotifier` (gate backfill + dedupe), `mygoNotifier` (impl produksi), `SetNotifierForTesting`, hook `notifyNewMessages` |
| `internal/app/notify_test.go` | **Baru.** 8 test dengan fake Notifier (tanpa OS asli) |
| `internal/app/service.go` | Wiring minimal: field `notify` + init di `New` (+2 baris), hook 3 baris di `tryRefreshAccount` |

Tidak disentuh (sesuai batasan brief): `internal/ui`, `internal/store`, `internal/mail/*`, `cmd/karta`, `internal/sync/*` (alur sync existing tidak perlu diubah — hook dipasang di titik payload header baru berada, yaitu `tryRefreshAccount`; worker hanya memicu `Refresh` seperti sebelumnya).

## Desain Implementasi

1. **Hook tanpa rombak alur sync.** `tryRefreshAccount` sudah mengiterasi header baru hasil `SyncFolder`; setelah upsert + `UpdateFolderSync`, folder ber-role **inbox** meneruskan headers ke `s.notifyNewMessages(...)`. IDLE (`internal/mail/idle.go`) dan worker (`internal/sync/worker.go`) tidak diubah — push IDLE tetap memicu siklus `Refresh` yang sama.
2. **Backfill suppression.** Gate `sessionNotifier` menandai "armed" per folder ID: sync **pertama** tiap folder dalam sesi tidak dinotifikasi (initial backfill hening). Akun/folder yang muncul di tengah sesi otomatis ikut aturan ini (diuji `TestGateFolderArmedIndependently`).
3. **Dedupe per sesi.** Set kunci: `Message-ID` bila ada, fallback `akun/folder/UID` untuk pesan tanpa Message-ID. Header yang dikirim ulang server (mis. reconnect IDLE) tidak memicu notifikasi kedua (diuji).
4. **Kondisi folder.** Hanya inbox yang dinotifikasi: pesan yang muncul di sent/drafts/trash/archive bukan pesan "diterima" (sender di sent = user sendiri; UID pesan pindahan bisa terlihat seperti pesan baru). Keputusan ini didokumentasikan di kode dan diuji (`TestRefreshNotificationInboxOnly`).
5. **Thread safety & context.** Gate dilindungi mutex; `mygoNotifier` membungkus ctx dengan `context.WithoutCancel` + timeout 10 s — cancellation sinkronisasi tidak membatalkan notifikasi dan goroutine tidak bisa menggantung bila main loop tidak memproses. Error notifikasi hanya di-log (best-effort), tidak pernah menggagalkan sync. Error wrapping `fmt.Errorf("kirim notifikasi desktop: %w", err)`.
6. **Bentuk notifikasi.** Title = "Karta" (app name), Body = `pengirim: subjek`, tanpa aksi klik, tanpa ID/image/sender khusus.

## Daftar Commit

| Commit | Isi |
|---|---|
| `3cd223b` | `feat(app): notifikasi OS pesan baru via API native mygo` — notify.go + notify_test.go + wiring service.go |

(`REPORT.md` di-commit terpisah setelah file ini dibuat.)

## Hasil Test (Definition of Done)

| DoD | Hasil |
|---|---|
| 1. `CGO_ENABLED=0 go build ./cmd/karta` | ✅ BUILD_OK (juga `CGO_ENABLED=0 go build ./...` OK) |
| 2. `go test ./internal/sync/... ./internal/app/...` | ✅ ok kedua paket |
| 2b. `go test -race` kedua paket | ✅ ok, nol race |
| 3. `go vet ./...` | ✅ bersih |
| 4. Dedupe/keputusan kirim diunit-test dengan fake Notifier | ✅ 8 test baru: `TestDedupeKey`, `TestGateBackfillThenNotify`, `TestGateFolderArmedIndependently`, `TestGateNotifyErrorLogged`, `TestGateConcurrentObserve`, `TestRefreshNotificationFlow`, `TestRefreshNotificationInboxOnly`, `TestNotificationOptions` |
| 5. Commit kecil di `feat/desktop-notify` | ✅ 1 commit fitur |

`go test ./...` penuh (mail, store, ui, sync, app): semua ok.

## Self-Review

- **Kelengkapan:** seluruh poin brief tercakup — cek API mygo dulu (langkah 1), pemanggilan library di app layer, interface kecil untuk fake, kondisi backfill + dedupe, body = sender + subject, app name "Karta", tanpa aksi klik.
- **YAGNI:** tidak ada fitur di luar brief (tanpa preferensi notifikasi, tanpa aksi klik, tanpa state persisten antar sesi — brief eksplisit dedupe hanya per sesi).
- **Layering (AGENTS.md):** `internal/sync` dan `internal/mail` tidak menyentuh mygo/ui; satu-satunya impor mygo baru ada di `internal/app` (diizinkan; hanya `mail`/`store` yang dilarang). Tidak ada query SQL/jaringan di layer UI.
- **Kekhawatiran minor (tidak memblokir):**
  1. Di macOS, notifikasi butuh app ter-package/sign (`mygo build`); saat dev tanpa package, `ShowContext` bisa mengembalikan error yang hanya ter-log. Perilaku ini bawaan mygo, terdokumentasi di doc-nya (`NotificationsSupported`).
  2. Notifikasi dikirim sinkron dalam siklus sync dengan timeout 10 s/pesan; bila puluhan pesan baru datang sekaligus, siklus bisa tertunda maksimal beberapa detik — trade-off sadar demi deterministik (tanpa goroutine latar) dan tetap dibatasi timeout.
  3. Pengiriman notifikasi mengikuti siklus `Refresh`, jadi jalur polling (fallback folder tanpa IDLE) otomatis ikut dinotifikasi dengan gate yang sama — dianggap perilaku yang benar sesuai brief ("background sync / IMAP IDLE").
