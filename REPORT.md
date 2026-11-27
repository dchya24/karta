# REPORT.md — CLI Export mbox/EML (Karta)

## Status: DONE

Brief `BRIEF.md` terimplementasi penuh di branch `feat/cli-export` (commit `3c2d5e3`).

## Ringkasan Implementasi

### 1. Pembentuk MIME — `internal/mail/export.go` (file baru)
Fungsi murni tanpa akses DB/jaringan, mengikuti batasan AGENTS.md (hanya stdlib):
- `ExportMessage` (input): FromName, FromAddr, To, Cc, Subject, Date, Message-ID, PlainText, RawHTML, Attachments (BLOB). RawHTML disimpan apa adanya; body ekspor selalu `text/plain` dari `PlainText` sesuai brief.
- `BuildEML`: pesan RFC822 utuh (CRLF). Body tunggal `text/plain; charset=utf-8` quoted-printable; bila ada lampiran dengan BLOB terisi → `multipart/mixed` (part teks quoted-printable + part lampiran base64, `Content-Disposition`/`Content-Type` dari kolom store; lampiran tanpa BLOB dilewati).
- `BuildMbox`: baris amplop `From <addr> <Mon Jan 02 15:04:05 2006>` (fallback `MAILER-DAEMON`), isi pesan dinormalkan CRLF→LF (konvensi mbox Unix), escaping setiap baris diawali `From ` → `>From `, diakhiri baris kosong pemisah.
- Header non-ASCII di-encode RFC 2047 (`mime.QEncoding`); Date zero → header Date dihilangkan; Message-ID dibungkus `<>` bila belum.

### 2. Query store — `internal/store/queries.go`
- `AttachmentBlob` + `ListAttachmentBlobs(messageID)`: lampiran beserta isi BLOB (data NULL tetap dikembalikan dengan `Data` nil). Daftar pesan folder memakai `ListMessages`/`GetMessage` yang sudah ada (pola N+1 sederhana, cukup untuk CLI export).

### 3. Orkestrasi — `internal/app/export.go` (file baru)
- `ExportOptions{Format, Directory, AccountEmail, FolderRole}` dan `ExportResult{AccountEmail, FolderName, FolderRole, Path, Count, Error}` — JSON-friendly.
- `Service.Export(ctx, opts) ([]ExportResult, error)`: validasi format (default mbox), `os.MkdirAll(dir, 0o700)`, loop akun → folder (dengan filter), per folder: baca pesan via store, bentuk MIME via `internal/mail`, tulis file. Service yang menulis file; CLI hanya parsing argumen + print (pola facade).
- Nama file: mbox `<email>--<role>.mbox` (mode 0600); eml `<role>/<UID>-<slug-subjek>.eml` (direktori role 0700). `sanitizeFilename` mengganti `/`, `\`, dan karakter kontrol dengan `_`; `slugSubject` slug 60 karakter, kosong → `tanpa-subjek`.
- Pesan `fetched=0`: diekspor dari metadata dengan body kosong, **tanpa** memicu fetch IMAP.
- Error per folder tidak menghentikan folder lain (laporan di `ExportResult.Error`, khusus kegagalan daftar folder akun tetap lanjut ke akun berikutnya). `ctx.Err()` dicek antar pesan/folder.

### 4. CLI — `cmd/karta/main.go`
- `os.Args[1] == "export"` → `runExport()`, lalu keluar; jalur lain = GUI seperti semula (bridge + UI tidak disentuh di jalur export).
- Stdlib `flag.NewFlagSet("export", flag.ExitOnError)`: `--format mbox|eml` (default mbox), `--account <email>`, `--folder <role>`, satu argumen posisional `[direktori]` (default `.`), `--help` berisi panduan.
- Ringkasan akhir: daftar per-folder (jumlah pesan + path) dan total akun/folder/pesan; kegagalan per-folder ke stderr, exit code tetap 0; error fatal (DB, format tidak dikenal) exit non-zero via `log.Fatal`.

## Keputusan Desain
- **Normalisasi LF di mbox**: entri mbox dinormalisasi CRLF→LF agar konsisten dengan konvensi mbox Unix dan escaping `\nFrom ` andal; .eml tetap CRLF RFC-compliant.
- **Escaping mbox**: semua baris body diawali `From ` di-escape (termasuk `From  dua spasi`) — konvensi mboxrd klasik.
- **Query store**: hanya `ListAttachmentBlobs` yang ditambahkan; sisanya memakai query yang ada agar tidak duplikasi.
- **Dependensi**: nol dependensi baru (semua stdlib: mime, mime/multipart, quotedprintable, base64).

## File Berubah
- `cmd/karta/main.go` — dispatch subcommand export + `runExport()`
- `internal/mail/export.go`, `internal/mail/export_test.go` — baru
- `internal/app/export.go`, `internal/app/export_test.go` — baru
- `internal/store/queries.go`, `internal/store/store_test.go` — `ListAttachmentBlobs` + test
- `BRIEF.md` — ikut di-commit sebagai rekam brief

## Commit
- `3c2d5e3` — `export: subcommand CLI karta export mbox/EML tanpa GUI`

## Hasil Test
- `CGO_ENABLED=0 go build ./cmd/karta` — lolos.
- `go test ./internal/mail/... ./internal/app/... ./internal/store/...` — ok ketiganya.
- `go test -race` pada ketiga paket — ok, nol race.
- `go vet ./...` — bersih.
- Smoke CLI (binary hasil build, HOME/XDG terisolasi): `--help`, export DB kosong (ringkasan 0/0/0, exit 0), `--format json` ditolak dengan pesan jelas.
- Cakupan test sesuai DoD: (a) MIME builder — escaping `>From `, eml multipart dengan attachment (verifikasi via `mail.ReadMessage` + `multipart.NextRawPart`, decode QP/base64), header lengkap + RFC 2047 + parse balik; (b) end-to-end headless — DB in-memory 2 folder, mbox & eml, file terbentuk dan isi terverifikasi; (c) pesan fetched=0 diekspor tanpa body tanpa error (mbox + eml); plus test filter akun/role, format tidak valid, dan `ListAttachmentBlobs`.

## Self-Review
- Batasan layering diverifikasi: `internal/mail/export.go` tidak mengimpor store/app/mygo; `internal/app/export.go` tidak mengimpor ui/mygo; tidak ada sentuhan ke `internal/ui`/`internal/sync`; tidak ada keyring/jaringan di jalur export.
- File yang diubah sesuai daftar "BOLEH diubah" di brief.
- YAGNI: tidak ada fitur di luar brief (tanpa progress bar, tanpa format lain, tanpa flag tambahan).

## Kekhawatiran
- Tidak ada. Catatan kecil: pesan tanpa alamat pengirim memakai amplop `MAILER-DAEMON` fallback, dan lampiran tanpa BLOB dilewati dari body (metadata hilang di file ekspor) — keduanya konsisten dengan ketentuan brief ("attachments dengan BLOB terisi").
