# Karta

[![Release](https://img.shields.io/github/v/release/dchya24/karta?label=rilis&logo=github)](https://github.com/dchya24/karta/releases/latest)
[![CI Release](https://github.com/dchya24/karta/actions/workflows/release.yml/badge.svg)](https://github.com/dchya24/karta/actions/workflows/release.yml)

**Karta** adalah email client desktop *pure native* — digambar langsung ke layar lewat GPU (`mygo/ui`), **tanpa Electron, tanpa WebView, tanpa HTML/CSS/JS**. Dirancang untuk kecepatan ekstrem, konsumsi memori minimal (< 30 MB RAM), navigasi *keyboard-first*, dan kerja *offline-first*.

Inti layanannya (IMAP/SMTP, parser MIME, SQLite FTS5) terpisah bersih dari lapisan UI, sehingga di masa depan bisa terhubung ke **Takka** (agent harness berbasis Rust) lewat IPC lokal.

## Fitur

- **3-pane layout native** — sidebar folder multi-akun, daftar pesan (virtualisasi bawaan), reading pane `richtext` dengan tautan klikable (`https://` & `mailto:`).
- **Pencarian instan** berbasis SQLite FTS5 atas subjek, pengirim, dan isi pesan — hasil di bawah 50 ms pada 10.000 email.
- **Aksi cepat pesan** — Arsip, Hapus (ke Trash), Tandai Baca/Belum, Balas, Balas Semua, Teruskan, simpan lampiran, *Open in Browser* fallback HTML.
- **Sinkronisasi realtime** — IMAP IDLE push dengan fallback polling 5 menit, backoff eksponensial.
- **Login Google tanpa konfigurasi** — cukup satu klik, tanpa membuat OAuth client di Google Console.
- **OAuth Google (Gmail)** — token disimpan aman di OS keyring (`go-keyring`), bukan di file/database; deteksi token kedaluwarsa/izin hilang dengan tombol *Login ulang*.
- **Tema terang/gelap** dengan warna brand sendiri, preferensi disimpan di `prefs.json`.
- **Multi-akun** — tambah/hapus akun IMAP (password atau OAuth2) dari dalam aplikasi.

### Cara kerja login Google (OAuth relay)

Karta tidak menyimpan `client_secret` Google di dalam aplikasi. Secret itu ada di layanan relay kecil stateless ([`server/`](server/README.md), open source di repo ini juga) yang dipasang di `https://karta.devchya.id`:

1. Karta minta `client_id` dari relay (`GET /oauth/config`).
2. Browser membuka halaman consent Google dan kembali ke callback lokal `http://127.0.0.1:8699` (PKCE, sesuai RFC 8252 — aplikasi desktop memang wajib redirect ke loopback).
3. Karta menukar `code` menjadi token lewat relay (`POST /oauth/exchange`) — relay menambahkan `client_secret` milik server, mem-proxy ke Google, dan **tidak menyimpan apa pun**.
4. Access & refresh token hanya disimpan di **OS keyring** komputermu. Server tidak menyimpan token, code, atau state apa pun.

Untuk development, mode lokal tetap didukung: set `KARTA_OAUTH_CLIENT_ID`/`KARTA_OAUTH_CLIENT_SECRET` (env) atau `~/.config/karta/oauth.json`, maka Karta bertukar token langsung dengan Google tanpa relay.

## Unduh & Instalasi

Semua aset tersedia di halaman [**Releases**](https://github.com/dchya24/karta/releases/latest).

### Linux

| Paket | Perintah |
|---|---|
| `.deb` (Debian/Ubuntu) | `sudo dpkg -i karta-linux-amd64-<versi>.deb` |
| `.rpm` (Fedora/openSUSE) | `sudo dnf install ./karta-linux-amd64-<versi>.rpm` |
| `.tar.gz` (portabel) | `tar -xzf karta-linux-amd64-<versi>.tar.gz && install -m755 dist/karta ~/.local/bin/` |

Paket `.deb`/`.rpm` memasang `karta` ke `/usr/bin`, entri menu `karta.desktop`, dan ikon ke tema `hicolor`. Ganti `amd64` menjadi `arm64` sesuai arsitektur mesin.

### macOS

1. Buka `Karta-macos-arm64-<versi>.dmg` (Apple Silicon) atau `Karta-macos-amd64-<versi>.dmg` (Intel).
2. Seret **Karta.app** ke folder *Applications*.

Aplikasi saat ini **belum ditandatangani** (unsigned). Saat macOS menolak membuka, klik kanan **Karta.app → Open → Open**, atau jalankan `xattr -cr /Applications/Karta.app`.

### Windows

1. Jalankan `Karta-Setup-windows-amd64.exe` (installer, dengan ikon desktop & uninstaller), atau
2. Pakai `karta-windows-amd64-portable.exe` jika ingin tanpa instalasi.

## Shortcut Keyboard

| Tombol | Aksi |
|---|---|
| `J` / `Down` | Pesan berikutnya |
| `K` / `Up` | Pesan sebelumnya |
| `C` | Tulis email baru |
| `R` / `Shift+R` | Balas / Balas Semua |
| `F` | Teruskan |
| `E` | Arsip |
| `U` | Baca / Belum dibaca |
| `Delete` | Hapus |
| `/` | Fokus ke kolom pencarian |

## Build dari Sumber

Syarat: [Go 1.27](https://go.dev) (toolchain 1.27.4). Build pure Go — **tidak butuh CGO**.

```sh
git clone https://github.com/dchya24/karta.git
cd karta
CGO_ENABLED=0 go build ./cmd/karta
go test ./internal/...
```

## Rilis Otomatis

Tag `v*` yang di-push memicu workflow [release.yml](.github/workflows/release.yml): binary `CGO_ENABLED=0` untuk Linux/macOS/Windows, lalu dikemas menjadi `.tar.gz`, `.deb`, `.rpm` (Linux), `.dmg` (macOS), dan installer `.exe` (Windows, Inno Setup), semuanya otomatis diterbitkan di halaman Releases.

## Dokumen Desain

- [PRD.md](PRD.md) — kebutuhan produk & ruang lingkup.
- [docs/DESIGN.md](docs/DESIGN.md) — keputusan teknis & arsitektur UI.
- [AGENTS.md](AGENTS.md) — aturan kontribusi untuk coding agents.

## Lisensi

Karta dirilis di bawah [Lisensi MIT](LICENSE).
