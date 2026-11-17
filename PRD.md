# Product Requirements Document (PRD): Karta

**Versi:** 0.1.0 (MVP)  
**Status:** Draft / In-Development  
**Tech Stack:** Go, `github.com/egoist/mygo` (Pure Native GUI), SQLite (FTS5), IMAP/SMTP  

---

## 1. Ringkasan Eksekutif & Visi Produk

**Karta** adalah aplikasi *email client* desktop *pure native* yang dirancang untuk kecepatan ekstrem, konsumsi memori minimal (< 30 MB RAM), dan pengalaman pengguna bebas *bloat* (tanpa Electron maupun WebView). 

Pada rilis pertama (v0.1), Karta berfokus menjadi *email client* mandiri yang andal, *offline-first*, dan responsif. Secara arsitektural, inti layanan Karta dirancang terpisah dari lapisan UI agar di masa depan dapat terhubung dengan **Takka** (agent harness berbasis Rust) melalui protokol IPC lokal yang ringan.

### Target Pengguna
- Developer, *power user*, dan pekerja teknis yang menginginkan *email client* cepat dengan navigasi *keyboard-first*.
- Pengguna yang menghindari aplikasi berbasis Chromium/WebView demi efisiensi baterai dan memori.

---

## 2. Prinsip Desain & Batasan Arsitektur

1. **Zero WebView / Pure Native GUI:** Seluruh antarmuka dibangun menggunakan **mode Native UI** dari `mygo` (package `mygo/ui` — toolkit yang digambar mygo sendiri via GPU, tanpa HTML/JS/webview). Mode web mygo (`Window.Page()`, `LoadHTML`, IPC `mygo.Bind`) tidak dipakai.
2. **Local-First & Instant UX:** Semua pesan yang telah disinkronisasi disimpan di SQLite lokal (WAL mode). Perpindahan folder, membuka email, dan pencarian harus berjalan instan (< 50ms).
3. **Text-First Rendering:** Mengutamakan keterbacaan konten teks bersih (`text/plain` atau konversi HTML-to-Text terstruktur) untuk menghindari beban rendering HTML berat dan *tracking pixel*.
4. **Decoupled Core (Agent-Ready):** Logika protokol email (`internal/mail`) dan penyimpanan (`internal/store`) tidak boleh bergantung pada paket UI (`internal/ui`).

---

## 3. Ruang Lingkup Fitur MVP (v0.1)

### 3.1. Manajemen Akun & Keamanan
- Mendukung konfigurasi 1–2 akun email via standar **IMAP** dan **SMTP** (termasuk dukungan App Password untuk Gmail/iCloud/Fastmail).
- Kredensial kata sandi wajib disimpan di **OS Keyring/Keychain** (bukan *plaintext* di file konfigurasi).

### 3.2. Sinkronisasi & Penyimpanan Lokal
- **Background Sync Worker:** Sinkronisasi berkala di latar belakang menggunakan *goroutine* (polling, default interval 5 menit per akun; IMAP IDLE ditunda ke v0.2) + tombol *manual refresh*. Fetch header per-batch; body di-fetch lazy saat pesan dibuka lalu di-cache di SQLite.
- **Folder Inti:** Sinkronisasi otomatis untuk `Inbox`, `Sent`, `Drafts`, `Trash`, dan `Archive`.
- **SQLite Storage + FTS5:** Penyimpanan metadata header, body teks bersih, HTML mentah (opsional untuk fallback), dan indeks pencarian teks penuh. Driver: `modernc.org/sqlite` (pure Go, FTS5 terverifikasi didukung). Detail skema di `docs/DESIGN.md`.

### 3.3. Antarmuka Utama (3-Pane Native Layout)
- **Pane 1 — Sidebar:** Daftar akun dan folder beserta indikator jumlah pesan belum dibaca (*unread badge*).
- **Pane 2 — Message List:** Daftar email (Pengirim, Subjek, Waktu, Status Read/Unread, Indikator Lampiran).
- **Pane 3 — Reading Pane:**
  - Header detail (`From`, `To`, `Cc`, `Date`, `Subject`).
  - Daftar lampiran (*attachments*) yang dapat disimpan ke disk lokal melalui *Native Save Dialog*.
  - Area baca isi pesan dengan dukungan klik tautan (*clickable links*).
  - Opsi fallback **"Open in Browser"** (menyimpan HTML ke file sementara dan membukanya di browser default OS jika user perlu melihat layout visual).

### 3.4. Composer & Aksi Pesan
- **Window/Dialog Composer Native:** Mendukung `Compose` baru, `Reply`, `Reply All`, dan `Forward`. Draft yang disimpan di-*sync* ke folder Drafts IMAP akun terkait.
- **Lampiran Keluar:** Memilih file lokal via *Native File Picker* untuk dikirim via SMTP.
- **Aksi Cepat:** Mark as Read/Unread, Archive, Delete (pindah ke Trash).

### 3.5. Navigasi & Pencarian Cepat
- **Search Bar Native:** Pencarian lokal instan berbasis SQLite FTS5 berdasarkan subjek, pengirim, dan isi pesan.
- **Keyboard Shortcuts:**
  - `J` / `Down`: Email berikutnya
  - `K` / `Up`: Email sebelumnya
  - `C`: Compose email baru
  - `R`: Reply email terpilih
  - `E`: Archive email terpilih
  - `/`: Fokus ke kolom pencarian

---

## 4. Non-Goals (Di Luar Ruang Lingkup v0.1)
- **Rendering native:** isi pesan dirender sebagai teks/richtext native; link `http(s)` dan `mailto:` clickable. Tidak ada engine HTML di dalam aplikasi.
- **Rendering HTML/CSS penuh di dalam aplikasi** (tidak menggunakan engine browser); tampilan visual HTML hanya via fallback **"Open in Browser"**.
- OAuth2 interaktif berbasis browser embedded (v0.1 cukup mendukung IMAP/SMTP + App Password).
- Kalender, Kontak terpisah, atau RSS reader.
- Integrasi aktif dengan **Takka** (hanya menyiapkan batas abstraksi `internal/app` dan `internal/bridge` tanpa fitur AI aktif di v0.1).

---

## 5. Metrik Keberhasilan (v0.1)
- **Cold Startup Time:** < 300 ms hingga window utama tampil.
- **Memory Footprint (Idle):** < 35 MB RAM.
- **Pencarian Lokal (10.000 email):** < 50 ms menggunakan SQLite FTS5.
- **Stabilitas Concurrency:** Nol *race condition* atau *UI freeze* saat sinkronisasi IMAP berjalan di latar belakang (dicek dengan `go test -race` pada `internal/sync` dan `internal/app`, lihat AGENTS.md §5).
- **Arsitektur:** Tidak ada impor `internal/ui`/`mygo` dari `internal/mail`, `internal/store`, dan `internal/sync` (sesuai AGENTS.md §3.A).