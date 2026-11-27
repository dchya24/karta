# AGENTS.md — Panduan Pengembangan Proyek Karta

Dokumen ini berisi aturan arsitektur, konvensi kode, dan batasan teknis bagi *coding agents* dan kontributor yang bekerja pada repositori **Karta**.

---

## 1. Gambaran Umum Proyek
- **Nama Proyek:** Karta
- **Bahasa:** Go (Golang)
- **GUI Framework:** `github.com/egoist/mygo` dalam **mode Native UI** (`mygo/ui` — toolkit UI yang digambar mygo sendiri via GPU, tanpa HTML/JS/webview) — **DILARANG** memakai mode web (`Window.Page()`, `LoadHTML`, IPC `mygo.Bind`), WebView, Electron, Wails, atau aset HTML/CSS/JS frontend.
- **Database:** SQLite (`modernc.org/sqlite` untuk pure Go tanpa CGO jika memungkinkan, atau sesuai konfigurasi `go.mod`).

---

## 2. Struktur Direktori Wajib

```text
karta/
├── cmd/karta/main.go        # Entry point aplikasi & wiring dependency
├── docs/                    # Dokumen desain & keputusan teknis (DESIGN.md)
├── internal/
│   ├── ui/                  # Komponen GUI native mygo/ui (Window, Split, Sidebar, List, Editor/richtext, Composer)
│   ├── app/                 # Application service layer (orkestrasi UI <-> Mail <-> Store)
│   ├── mail/                # Protokol IMAP, SMTP, dan MIME parser
│   ├── store/               # SQLite repository, migrasi skema, dan query FTS5
│   ├── sync/                # Background goroutine untuk sinkronisasi email

```

Pemetaan widget: layout 3-pane = `ui.Split` bersarang; sidebar = `ui.Sidebar`; daftar pesan = `ui.List` (virtualisasi bawaan); reading pane = `ui.Editor`/`richtext` dengan `ui.Link`; dialog lampiran = DialogModule (`Save`/`Open`); shortcut = `Context.OnWindowShortcut`; dispatch ke main thread = `ui.RunOnMain` / `postMain`. Lihat `docs/DESIGN.md` untuk detail.

---

## 3. Aturan Arsitektur & Batasan Ketat

### A. Pemisahan Layer (Strict Layering)
1. `internal/mail` dan `internal/store` **TIDAK BOLEH** mengimpor `internal/ui` atau `github.com/egoist/mygo`.
2. `internal/ui` **TIDAK BOLEH** menjalankan query SQL atau koneksi jaringan IMAP/SMTP secara langsung. Semua aksi user harus memanggil method pada `internal/app.Service`.
3. `internal/app` bertindak sebagai *facade* utama. Desain method dan struct DTO pada `internal/app` harus bersih dan serializable (JSON-friendly), siap diekspos lewat IPC ke Takka (Rust) di masa mendatang.

### B. Thread Safety & GUI Main Thread (`mygo`)
- Operasi GUI native **wajib berjalan di Main Thread**.
- Background worker (`internal/sync`) dan *goroutine* jaringan (`internal/mail`) **tidak boleh** memodifikasi properti widget `mygo` secara langsung.
- Selalu gunakan mekanisme *dispatch/callback* ke main thread yang disediakan `mygo` (`ui.RunOnMain` untuk blocking, `postMain` untuk async) saat memperbarui daftar email, status bar, atau badge folder setelah proses sinkronisasi selesai.
- Kode native `mygo/ui` dapat diuji tanpa GUI aktif melalui headless `Tester` bawaan mygo; gunakan untuk unit test komponen UI yang tidak menyentuh protokol/jaringan.

### C. Strategi Parsing & Rendering Email
- Saat mem-parsing pesan MIME di `internal/mail/parser.go`, selalu ekstrak dan simpan dua representasi (jika tersedia):
  1. `plain_text`: Teks bersih untuk ditampilkan di widget teks native dan untuk konsumsi konteks agent di masa depan.
  2. `raw_html`: Disimpan apa adanya di SQLite hanya untuk fitur fallback *"Open in Browser"*.
- Jika email masuk hanya memiliki bagian `text/html`, konversikan menjadi teks bersih yang rapi (mempertahankan jeda paragraf dan URL) sebelum disimpan ke kolom `plain_text`.

### D. Keamanan & Penyimpanan Kredensial
- **JANGAN PERNAH** menyimpan password atau token akun di file konfigurasi JSON/TOML/YAML maupun di database SQLite.
- Gunakan OS Keyring (`github.com/zalando/go-keyring`) untuk menyimpan dan mengambil kredensial akun.

---

## 4. Konvensi Kode Go
- Gunakan *error wrapping* standar Go (`fmt.Errorf("konteks operasi: %w", err)`).
- Pastikan semua operasi I/O jaringan (IMAP/SMTP) dan database menerima `context.Context` untuk mendukung *cancellation* dan *timeout*.
- Hindari dependensi eksternal yang berat. Prioritaskan standard library Go atau library kecil yang teruji. Dependensi inti yang disepakati: `modernc.org/sqlite` (pure Go, FTS5), `emersion/go-imap/v2` (perhatikan: masih beta — pin versi persis), `emersion/go-message`, `zalando/go-keyring`, `golang.org/x/net/html` (untuk HTML→Text). Konversi HTML→Text ditulis sendiri di `internal/mail` (walk DOM `x/net/html`), tanpa dependensi converter berat.
- Build harus lolos dengan `CGO_ENABLED=0` (pure Go, kompatibel no-cgo mygo).
- Tulis unit test untuk `internal/mail/parser.go` dan `internal/store` (terutama query SQLite FTS5) tanpa memerlukan lingkungan GUI aktif.

---

## 5. Checklist Sebelum Menyelesaikan Tugas (Definition of Done)
1. Kode terkompilasi bersih (`go build ./cmd/karta`).
2. Tidak ada pemanggilan widget UI di luar *main thread*.
3. Tidak ada kebocoran dependensi dari `internal/ui` ke dalam `internal/app`, `internal/mail`, atau `internal/store`.
4. Unit test pada layer non-UI lulus (`go test ./internal/mail/... ./internal/store/...`).
5. `go vet ./...` lulus tanpa temuan.
6. `go test -race ./internal/sync/... ./internal/app/...` lulus — nol race condition (PRD §5).