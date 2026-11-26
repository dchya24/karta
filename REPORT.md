# REPORT.md — Onboarding First-Run + Empty State (Karta)

## Status
**DONE** — semua poin brief terpenuhi, semua test lulus.

## Ringkasan Perubahan
Hanya menyentuh `internal/ui` (sesuai batasan AGENTS.md/brief):

1. **Welcome screen** (`internal/ui/app.go`):
   - `state.view`: bila `len(s.sections) == 0` (0 akun) dan form akun tidak terbuka → render `s.welcome(c)` layar penuh, menggantikan 3-pane.
   - `welcome()` baru: logo `kartaLogo` (72px), nama app "Karta" (bold 28), tagline "Email client native. Cepat, tenang, teks-first." (`t.TextMuted`), tombol utama **"Tambah akun"** → `s.showAccount = true` (form akun layar penuh yang sudah ada, tidak ada service call baru). Layout `ui.Column(c).Fill().Center().Gap(14)`, warna mengikuti tema aktif (`t.TextMuted` dari `c.Theme()`).
   - Tidak ada flag onboarding yang dipersist — kondisi 0 akun deterministik dari `reloadFolders()`.
2. **Empty state daftar pesan**: placeholder `"Tidak ada pesan"` (`t.TextMuted`, padding 16) di dalam `ui.List` sudah ada di kode sebelumnya; perilaku ini kini dikunci oleh test `TestEmptyFolderPlaceholder`. Reading pane tidak disentuh.
3. **Shortcut guard** (samping, kecil): `s.shortcuts` sekarang return awal bila `len(s.sections) == 0` — mencegah shortcut (mis. `c` membuka composer) menyala di layar welcome tanpa akun.

## File Berubah
- `internal/ui/app.go` — welcome screen + guard shortcut (+25/-1 baris).
- `internal/ui/welcome_test.go` — test baru (3 test).
- `REPORT.md` — laporan ini.

## Keputusan Desain
- Welcome digambar sebagai cabang penuh di `view` (mirip pola `showAccount`/`composerOpen`), bukan overlay — sesuai desain brief "state welcome layar penuh menggantikan 3-pane".
- Tombol memakai teks "Tambah akun" dengan Label aksesibilitas "Tambah akun pertama" agar bisa diklik deterministik di headless `Tester` (`tt.Click`).
- `eval_screenshot_test.go` tidak terdampak (semua scenario memakai `testService` yang punya 1 akun); tidak ada snapshot yang perlu diperbarui. Belum ditambahkan screenshot welcome (di luar scope brief).

## Test (pola headless `ui.NewTester`)
- `TestWelcomeScreenFirstRun` — 0 akun → welcome tampil (logo text, tagline, tombol), dan 3-pane **tidak** tampil (placeholder/toolbar absah).
- `TestWelcomeButtonOpensAccountForm` — klik "Tambah akun pertama" → `s.showAccount = true`, form akun tampil, welcome hilang.
- `TestEmptyFolderPlaceholder` — folder terpilih (Trash kosong) dengan 0 pesan → "Tidak ada pesan" tampil.

## Hasil Verifikasi (Go 1.27.4)
- `CGO_ENABLED=0 go build ./cmd/karta` ✅
- `go test ./internal/ui/...` ✅
- `go test -race ./internal/ui/...` ✅
- `go test -race ./internal/sync/... ./internal/app/...` ✅ (DoD AGENTS.md §5.6)
- `go test ./...` (semua paket) ✅
- `go vet ./...` ✅ bersih

## Self-Review
- Batasan: hanya `internal/ui` berubah; 0 dependensi baru; native mygo/ui (tanpa HTML/web); tidak ada perubahan di `internal/app`/`store`/`mail`/`sync` — ✅.
- YAGNI: tidak menambah flag persist, tidak menambah service method, tidak menyentuh reading pane — ✅.
- Self-review diff: minimal, mengikuti pola `view` yang ada (branch early-return + helper), komentar sesuai gaya bahasa repo.

## Kekhawatiran
- Tidak ada yang blocking. Catatan kecil: tampilan visual welcome (ukuran logo 72px, font size 28, gap 14) adalah pilihan selera yang bisa disetel lewat screenshot eval manual (`KARTA_UI_EVAL=1`) — tidak diverifikasi visual di lingkungan bermonitor.
