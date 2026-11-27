# REPORT: Bulk Action Pesan (Karta)

## Status
**DONE**

## Ringkasan
Multi-pilih pesan di daftar pesan lalu Archive/Delete sekaligus, persis
mengikuti desain brief tanpa tambahan fitur.

## Keputusan Implementasi
- **Service (`internal/app/service.go`)**: `ArchiveMany(ctx, []int)` dan
  `DeleteMany(ctx, []int)` diimplementasi lewat helper `forEachID` — loop
  berurutan memanggil `Archive`/`Delete` yang sudah ada (urutan input =
  urutan eksekusi, tanpa parallel karena IMAP satu koneksi per akun).
  Error per-ID dibungkus `fmt.Errorf("pesan %d: %w", ...)` lalu diagregasi
  ke `*MultiError` yang sudah ada; `nil` bila semua sukses. DTO tidak berubah.
- **UI (`internal/ui/app.go`)**:
  - Set pilihan: `bulkSel map[int]bool` (ID pesan, bukan thread — pesan
    tersembunyi thread tidak ikut kecuali tampil sebagai baris terbarunya).
  - Tombol `x` toggle pesan terpilih; indikator visual termurah sesuai
    widget yang ada: prefix `✔` pada teks sender + teks baris berwarna
    `t.Accent` (menang atas warna seleksi baris `AccentText`).
  - `Escape`, pindah folder, dan reload daftar mengosongkan set —
    diimplementasi di awal `reloadMessages()` (dipanggil oleh semua jalur
    tersebut), plus `Escape` di `shortcuts()`.
  - Saat set tidak kosong, baris aksi tampil di toolbar daftar pesan:
    `N terpilih: [A]rsip  [D]elete  [x]Batal` (pola baris existing).
  - Shortcut: `Shift+A` (= huruf besar A) → `Service.ArchiveMany`;
    `Shift+D` → modal konfirmasi `ui.Modal` (pola `deleteAccountModal`)
    yang konfirmasinya memanggil `Service.DeleteMany`; `x` toggle;
    `Escape` kosongkan. Tidak ada bentrok dengan shortcut existing
    (`c`, `r`, `e`, `/`, `j/k`, `u`, `f`, Delete) sehingga navigasi `j/k`
    tetap hidup saat memilih — bisa multi-select via keyboard.
  - Eksekusi `runBulk` mengumpulkan ID dalam urutan tampil daftar, lalu
    memanggil method bulk Service di goroutine (langsung sinkron saat
    headless `win == nil`, pola `runAction` yang ada). Hasil → toast
    ringkasan (`N diarsipkan` / `N dihapus`; bila `MultiError`: gagal yang
    semuanya `SyncWarning` ditampilkan sebagai peringatan "aksi lokal
    berhasil, sync server gagal", selain itu `S berhasil, F gagal`) +
    `reloadFolders()` (memuat ulang daftar pesan dan badge unread sidebar).
- **Layering**: `internal/ui` tidak menyentuh SQL/jaringan — semua via
  `app.Service`. Tidak ada dependensi baru. `internal/store`, `internal/mail`,
  `internal/sync`, `cmd/karta`, dan `internal/app/types.go` tidak disentuh.

## Commit (branch `feat/bulk-actions`)
1. `3f99adf` — app: ArchiveMany/DeleteMany — loop berurutan per-ID, error agregat MultiError
2. `1d8f476` — ui: mode pilih bulk pesan (x toggle, Shift+A arsip, Shift+D hapus via modal) + test headless

## File Berubah
- `internal/app/service.go` (+29): `ArchiveMany`, `DeleteMany`, `forEachID`.
- `internal/app/bulk_test.go` (baru): 3 test service.
- `internal/ui/app.go` (+~170): state bulk, toggle/clear, baris aksi,
  indikator baris, shortcut, modal, eksekusi + toast.
- `internal/ui/bulk_test.go` (baru): 3 test UI headless.

## Hasil Test (Definition of Done)
| Check | Hasil |
|---|---|
| `CGO_ENABLED=0 go build ./cmd/karta` | ✅ lolos |
| `go vet ./...` | ✅ bersih |
| `go test ./internal/app/... ./internal/ui/...` | ✅ ok |
| `go test -race` (kedua paket) | ✅ ok, nol race |
| `go test ./...` (seluruh repo) | ✅ ok (mail, store, sync ikut lulus) |

Detail test:
- Service (stub IMAP headless, pola `actionStubIMAP` existing): urutan MOVE
  mengikuti urutan input acak `[id3,id1,id2]`; error satu ID tidak
  menghentikan ID lain; agregat `*MultiError` berisi konteks `pesan 999`;
  daftar kosong = tanpa panggilan IMAP; DeleteMany di Trash = hapus
  permanen + expunge per-ID.
- UI headless (`ui.NewTester`): toggle `x` (add & remove), hint
  `N terpilih: [A]rsip [D]elete [x]Batal` sesuai jumlah, Escape kosongkan,
  reload daftar (simulasi pindah folder) kosongkan, `Shift+A` memindahkan
  2 pesan ke folder Archive via Service + toast "2 diarsipkan" + set
  kosong, `Shift+D` membuka modal "Hapus 1 pesan?" yang konfirmasinya
  memindahkan pesan ke Trash + toast "1 dihapus".

## Self-Review
- Kelengkapan: semua poin desain brief terpenuhi (x, Escape, pindah folder,
  reload, baris aksi, A/D/x, modal, toast ringkasan, reload unread,
  pilihan per-pesan, Service bulk bukan loop UI).
- Kualitas: error wrapping `%w` konsisten; komentar bahasa Indonesia
  mengikuti gaya repo; helper `forEachID` dipakai bersama dua method.
- YAGNI: satu wrapper unused (`runBulkDelete`) sempat terbentuk lalu
  dihapus sebelum commit; tidak ada fitur di luar brief (tanpa select-all,
  tanpa drag-select, tanpa klik pada baris aksi).

## Kekhawatiran
- Toast gagal parsial menghitung `SyncWarning` sebagai "gagal" bila
  digabung dengan kegagalan lokal sesungguhnya; kasus semua-`SyncWarning`
  sudah ditangani sebagai peringatan tersendiri. Perilaku ini mengikuti
  kontrak `MultiError` yang ada.
- `gofmt` menandai `internal/app/types.go` dan `internal/store/thread.go`
  sebagai belum terformat — kondisi pra-eksisting di HEAD, di luar cakupan
  file yang boleh diubah brief ini, jadi tidak disentuh.
