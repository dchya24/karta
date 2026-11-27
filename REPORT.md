# REPORT.md — Batch Paralel: Bulk Action + CLI Export (Karta)

Dua fitur dikerjakan paralel di dua worktree, lalu di-merge ke master.

## 1. Bulk action pesan (feat/bulk-actions)
- Status: DONE
- Service: `ArchiveMany`/`DeleteMany` via helper `forEachID` — loop berurutan memanggil `Archive`/`Delete` existing (urutan input = urutan eksekusi, tanpa parallel karena IMAP satu koneksi per akun). Error per-ID dibungkus `pesan %d: %w` ke `*MultiError` yang sudah ada.
- UI: set pilihan `bulkSel map[int]bool` (per ID pesan, bukan thread); toggle `x` dengan indikator ✔ + warna accent; baris aksi "N terpilih: [A]rsip [D]elete [x]Batal" (`Shift+A`/`Shift+D`); Delete via modal `ui.Modal` pola yang ada; Escape/pindah folder/reload → set kosong. Eksekusi via Service di goroutine + `Window.Update`, toast ringkasan + reload unread.
- Test: 6 baru (3 service stub-IMAP: urutan, agregat error, no-op kosong; 3 UI headless: toggle, clear, arsip via Service, modal hapus).
- Catatan minor: toast parsial menghitung `SyncWarning` sebagai gagal bila bercampur kegagalan lokal (mengikuti kontrak MultiError existing).

## 2. CLI export mbox/EML (feat/cli-export)
- Status: DONE
- `karta export [dir] --format mbox|eml --account <email> --folder <role>`; default mbox, argumen `export` di-detach dari jalur GUI; stdlib `flag.NewFlagSet`, nol dependensi baru.
- `internal/mail/export.go`: pembentuk MIME murni — `BuildEML` (text/plain quoted-printable, atau multipart/mixed dengan lampiran base64) dan `BuildMbox` (amplop `From `, escaping `>From `, normalisasi LF).
- `internal/app/export.go`: `Service.Export(ctx, ExportOptions)` JSON-friendly — loop akun/folder dari SQLite, tulis file `<email>--<role>.mbox` / `<role>/<UID>-<slug>.eml` mode 0600/0700, filename di-sanitize, error per folder tidak menghentikan folder lain.
- `internal/store/queries.go`: tambahan `ListAttachmentBlobs`.
- Pesan `fetched=0` diekspor dari metadata tanpa body, tanpa trigger IMAP.
- Layering terverifikasi: mail tanpa store/app/mygo; app tanpa ui/mygo; tidak menyentuh ui/sync.

## DoD pasca-merge
Build no-cgo, `go vet`, test penuh, dan `-race` dijalankan ulang di master setelah merge ini.

## Riwayat merge sebelumnya (konteks)
- `d446857` — folder dinamis + onboarding (paralel).
- `49e950b` — notifikasi OS + thread grouping (paralel).
- `6706854` — notifikasi asinkron FIFO + hint error darwin.
