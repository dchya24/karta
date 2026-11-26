# Laporan Implementasi — Batch Paralel (2026-01)

Dua fitur dikerjakan paralel di dua worktree (branch terpisah), lalu di-merge ke master.

## 1. Notifikasi OS pesan baru (feat/desktop-notify)
- Status: DONE
- Library: API native mygo (`NewNotification` + `ShowContext`) — nol dependensi baru, build `CGO_ENABLED=0` tetap lolos.
- Desain: hook di `tryRefreshAccount` setelah header baru tersimpan; backfill awal sesi hening; dedupe per sesi (Message-ID / akun+folder+UID); hanya folder inbox; error hanya di-log.
- Test: 8 test baru (fake Notifier): backfill suppression, dedupe, konkurensi, inbox-only, bentuk opsi. `go test -race` sync+app lulus, `go vet` bersih.
- Catatan: macOS butuh app ter-package untuk notifikasi; pengiriman sinkron dengan timeout 10s.

## 2. Thread grouping message list (feat/thread-grouping)
- Status: DONE
- Desain: identitas thread dari `References`/`In-Reply-To` (normalisasi Message-ID), fallback subject dinormalisasi, per-folder; singleton untuk pesan tanpa identitas.
- Store: migrasi v4 (`thread_id`, refs, index) + backfill + `RegroupThreads` (union-find deterministik) + `ListThreadSummaries`; regroup pasca-sync.
- UI: 1 baris per thread + badge count, virtualisasi `ui.List` tidak diubah.
- Test: normalisasi, rantai References, order-independence, per-folder, migrasi+backfill, regroup, singleton, headless UI. `go test -race` lulus.

## Merge
- Konflik di `internal/app/service.go` diselesaikan: `RegroupThreads` dulu, lalu `notifyNewMessages`.
- DoD penuh dijalankan ulang setelah merge (build no-cgo, vet, test race).
