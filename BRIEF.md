# Brief: CLI Export mbox/EML (Karta)

Worktree: `/Works/Development/self-project/karta.worktrees/export` (branch `feat/cli-export`).

## Tujuan
Subcommand `karta export` mengekspor pesan lokal dari SQLite ke mbox (default) atau EML, tanpa GUI.

## Desain (ikuti, jangan berinovasi)
### CLI (`cmd/karta/main.go`)
- `karta export [direktori]` — flag: `--format mbox|eml` (default mbox), `--account <email>` (filter satu akun; kosong = semua), `--folder <role>` (filter role; kosong = semua folder).
- `os.Args[1] == "export"` → jalankan export lalu keluar; argumen lain/perilaku lama → GUI seperti sekarang. Pakai stdlib `flag` (NewFlagSet dengan nama subcommand), tanpa dependensi baru.
- Output: mbox → satu file `<email>--<role>.mbox` per folder di direktori tujuan (default: direktori kerja saat ini). eml → satu file `<role>/<UID>-<slug-subjek>.eml`. Direktori dibuat `os.MkdirAll` 0o700. Nama file di-sanitize (tanpa `/`, karakter kontrol).
- Print ringkasan akhir: jumlah akun, folder, pesan, path. Error per folder tidak menghentikan folder lain.

### Layering (AGENTS.md)
- **Pembentuk MIME di `internal/mail/export.go`** (file baru): fungsi murni tanpa akses DB — input struct kecil (From, To, Cc, Subject, Date, Message-ID, PlainText, RawHTML, attachments BLOB) → output bytes pesan RFC822. `mbox` = envelope `From ` line + escaping body (`>From `) + pesan; `eml` = satu pesan. Body: `text/plain` dari `plain_text`; bila ada attachments dengan BLOB terisi, pakai `mime/multipart/mixed` (stdlib — go-message sudah ada tapi stdlib cukup). `Content-Type`/`Content-Disposition` attachment dari kolom yang ada.
- **Orkestrasi di `internal/app/export.go`** (file baru): method `Service.Export(ctx, opts ExportOptions) ([]ExportResult, error)` — JSON-friendly DTO; loop akun/folder dari `s.db`, baca pesan via query yang sudah ada, panggil pembentuk MIME dari `internal/mail`, tulis file (atau kembalikan path per folder agar CLI menulis file — pilih: Service menulis file, CLI hanya parsing argumen + print; konsisten dengan pola facade). Chat/DTO: definisikan struct yang diserialisasi jelas.
- **Store**: tambahkan query yang dibutuhkan bila belum ada (mis. daftar pesan folder lengkap beserta body + attachments) di `internal/store/export_query.go` atau tambahan di `queries.go` — ikuti pola query yang sudah ada. Pesan dengan `fetched=0` (body belum di-cache): tetap ekspor dari metadata, body kosong + kolom body kosong; jangan trigger IMAP.

## Batasan (AGENTS.md, wajib)
- `internal/mail` tidak boleh mengimpor `internal/store`/`internal/app`/mygo.
- `internal/app` tidak boleh mengimpor `internal/ui`/mygo.
- Tanpa keyring/GUI — export murni dari DB lokal.
- `context.Context` untuk operasi DB; error wrapping `%w`.

## File yang BOLEH diubah
`cmd/karta/main.go`, `internal/app/export.go` (+test), `internal/mail/export.go` (+test), `internal/store/queries.go` bila perlu (+test).
**Jangan** sentuh `internal/ui`, `internal/sync`.

## Definition of Done
1. `CGO_ENABLED=0 go build ./cmd/karta` lolos.
2. `go test ./internal/mail/... ./internal/app/... ./internal/store/...` + `-race` lulus; `go vet ./...` bersih.
3. Test: (a) MIME builder: mbox escaping `>From `, eml multipart dengan attachment, header lengkap; (b) end-to-end headless: DB in-memory dengan pesan 2 folder → export → file terbentuk, isi terverifikasi; (c) pesan fetched=0 diekspor tanpa body tanpa error.
4. Commit kecil yang jelas di branch `feat/cli-export`.

## Laporan
Tulis ke `REPORT.md` di root worktree: status, keputusan, commit, file berubah, hasil test, self-review, kekhawatiran.
