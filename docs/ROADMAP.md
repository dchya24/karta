# ROADMAP — Karta v0.2

Status: semua fitur MVP v0.1 (PRD §3) sudah terimplementasi, termasuk IMAP IDLE
yang semula dijadwalkan v0.2 dan OAuth XOAUTH2 yang semula non-goal v0.1.
Rilis terakhir: v0.4.0.

## Prioritas v0.2

### 1. Bridge IPC untuk Takka (AI agent)
Server local socket (Unix socket / TCP localhost) di repo utama yang mengekspos
DTO JSON-friendly `internal/app` (ListThreads, ListMessages, GetBody, SendDraft,
Search, dst.) dengan protokol NDJSON request/response. Klien pertama: Takka
(Rust). Fondasi: batas abstraksi layering sudah rapi (app bebas ui/mygo).

### 2. Edit draft dari folder Drafts
Saat ini `SaveDraft` meng-APPEND ke folder Drafts IMAP, tapi draft yang tersimpan
belum bisa dibuka ulang di composer untuk dilanjutkan/dikirim. Perlu: deteksi
pesan draft di list (flag `\Draft`), tombol/konteks "Edit", load body + lampiran
ke composer, hapus draf lama saat kirim/replace.

### 3. Search per-scope
FTS5 sudah ada; tambahkan filter scope (folder aktif / akun aktif / semua) di
search bar, plus highlight cuplikan hasil (snippet FTS5).

### 4. Bintang / flag pesan
`Flag \Flagged` IMAP + kolom bintang di list + shortcut `S`. Cache flag di SQLite.

### 5. Multi-aksi lampiran
"Save All" untuk semua lampiran satu pesan, dan drag-and-drop keluar jika
didukung toolkit mygo.

## Ditunda / dipertimbangkan

- **Identity/signature per akun** — kecil, bisa masuk v0.2 bila ada permintaan.
- **Filter/sieve rule sisi klien** — berat, evaluasi ulang di v0.3.
- **Kontak autolengkap** (alamat dari sent) — murah, kandidat v0.2 bila composer
  butuh autocomplete.
