# Karta — Design & Technical Decisions (hasil riset v0.1)

## 1. Fakta mygo (riset modul v0.9.2, 2026-11-17)

mygo BUKAN "pure native OS bindings" sebagaimana kata AGENTS.md. Ia dual-mode:

1. **Web mode** — halaman HTML di webview sistem (WKWebView/WebKitGTK/WebView2). Dilarang untuk Karta.
2. **Native UI mode** — package `mygo/ui`: toolkit UI yang digambar mygo sendiri via GPU, **tanpa HTML/JS/webview**. Ini yang dipakai Karta.

Kami hanya akan memakai `mygo.App` + `mygo/ui`; tidak akan memakai `Window.Page()`, `LoadHTML`, `Bind` (IPC web), maupun `ui.Webview`.

### Pemetaan API mygo → kebutuhan PRD

| PRD | mygo API |
|---|---|
| Window utama | `mygo.NewWindow(WindowOptions{...})` + `WindowOptions.Content` (native UI builder) |
| 3-pane layout | `ui.Split` (nested: sidebar/list, list/reading) |
| Sidebar akun+folder+unread | `ui.Sidebar` |
| Message list 10k email | `ui.List` — virtualisasi bawaan (hanya render baris di view) |
| Reading pane text | `ui.Editor` / `richtext` (link clickable ada: `ui.Link`, richtext mendukung link) |
| Composer | `ui.Textarea`, `ui.Textinput`, window dialog |
| Attachment save/pick | `mygo` DialogModule: `Save(SaveDialogOptions)`, Open dialog |
| Keyboard shortcut (J/K/C/R/E//) | `Context.OnWindowShortcut(mods, key, fn)` |
| Main-thread dispatch | `ui.RunOnMain(fn)` (blocking) / `postMain` (async) di `loop.go` |
| Test tanpa GUI | headless renderer + `Tester` (`ui/headless.go`) |
| No cgo | purego-based; `CGO_ENABLED=0` aman → kompatibel `modernc.org/sqlite` |

Note: `ui` juga bisa jalan di browser (WASM) — tidak relevan untuk Karta, abaikan.

## 2. Keputusan dependensi

| Kebutuhan | Pilihan | Status |
|---|---|---|
| SQLite | `modernc.org/sqlite` v1.62.0 | FTS5 tersedia (virtual table + shadow tables didukung driver). Pure Go. `go 1.26.0` |
| IMAP | `github.com/emersion/go-imap/v2` | Terbaru = `v2.0.0-beta.9` (Nov 2026) — masih beta, pin versi persis, siap API churn |
| MIME | `github.com/emersion/go-message` v0.18.2 | Stabil |
| Keyring | `github.com/zalando/go-keyring` v0.2.8 | Stabil |
| HTML→Text | `golang.org/x/net/html` (parser stdlib-extended, sudah jadi dep langsung mygo) + converter manual di `internal/mail/parser.go` | Tanpa dependensi berat baru |

## 3. Strategi sync (keputusan)

- **Polling berkala** untuk v0.1: interval default 5 menit per akun, tombol manual refresh, `context.Context` untuk cancel.
- IMAP IDLE ditunda ke v0.2 (kompleksitas reconnection; polling cukup untuk MVP).
- Batching: fetch header batch (`UID SEARCH` + `FETCH` batch 500), body lazy — body di-fetch saat email dibuka pertama kali (disimpan cache SQLite).

## 4. Skema SQLite

```sql
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE accounts (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,      -- password TIDAK di sini (keyring)
  imap_host TEXT NOT NULL,
  imap_port INTEGER NOT NULL,
  imap_tls INTEGER NOT NULL DEFAULT 1,
  smtp_host TEXT NOT NULL,
  smtp_port INTEGER NOT NULL,
  smtp_tls INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);

CREATE TABLE folders (
  id INTEGER PRIMARY KEY,
  account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name TEXT NOT NULL,              -- nama folder IMAP
  role TEXT NOT NULL,              -- inbox|sent|drafts|trash|archive|other
  last_seen_uid INTEGER NOT NULL DEFAULT 0,
  UNIQUE(account_id, name)
);

CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  uid INTEGER NOT NULL,
  message_id TEXT,
  in_reply_to TEXT,
  subject TEXT,
  sender TEXT,                     -- "Name <addr>" asli
  sender_addr TEXT,                -- addr murni utk search
  recipients TEXT,                 -- To+Cc
  date TEXT,
  unread INTEGER NOT NULL DEFAULT 1,
  has_attachment INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL DEFAULT 0,
  plain_text TEXT,                 -- dilihat lazy; diisi saat fetch body
  raw_html TEXT,                   -- asli, fallback "Open in Browser" saja
  fetched INTEGER NOT NULL DEFAULT 0, -- body sudah di-fetch?
  UNIQUE(folder_id, uid)
);
CREATE INDEX idx_messages_folder ON messages(folder_id, date DESC);

CREATE TABLE attachments (
  id INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  filename TEXT NOT NULL,
  content_type TEXT,
  size INTEGER NOT NULL,
  data BLOB                        -- simpan inline; email biasanya kecil
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
  subject, sender_addr, recipients, plain_text,
  content=''  -- external content manual; index diisi via trigger/insert eksplisit
);
```

Migrasi: tabel `schema_migrations(version INTEGER PRIMARY KEY)`; tiap migrasi satu fungsi berurut.

## 5. DTO `internal/app` (JSON-friendly, siap bridge)

```go
type Account struct{ ID int; Email string }
type Folder struct{ ID int; AccountID int; Name string; Role string; Unread int }
type MessageHeader struct{ // ringan utk list, tanpa body
  ID, FolderID int; UID int
  Subject, Sender, Date string
  Unread, HasAttachment bool
}
type Message struct { MessageHeader; To, Cc string; PlainText, RawHTML string
  Attachments []Attachment }
type Attachment struct{ ID int; MessageID int; Filename, ContentType string; Size int }
type Draft struct{ To, Cc, Subject, Body string; Attachments []string }
```

Service interface utama: `AddAccount`, `ListAccounts`, `RemoveAccount`, `ListFolders`,
`ListMessages(folderID, q string)`, `GetMessage(id)`, `MarkRead(id, bool)`,
`Archive(id)`, `Delete(id)`, `SaveDraft(d)`, `Send(d, ctx)`, `Refresh(ctx)`.

## 6. HTML→text converter (internal/mail/htmltotext.go)

Walk `x/net/html`: node `p`, `div`, `br`, `blockquote`, `li` → newline;
`a[href]` → `text (url)` hanya jika href ≠ text; strip `script/style/head`;
collapse ≥2 newline kosong. Unit test wajib di AGENTS.md.

## 7. Prasyarat lingkungan (bloker)

**Go toolchain belum terpasang di mesin ini.** mygo butuh `go >= 1.27.3`
(modernc sqlite butuh >= 1.26.0). Perlu install user-local (mis.
`~/.local/go-1.27.x`) sebelum langkah 5 (implementasi).

## 8. Risiko terbuka

1. RAM < 35 MB: FTS5 index + modernc binary + toolkit GPU — perlu benchmark awal sebelum fitur bertumpuk.
2. go-imap v2 beta: pin `v2.0.0-beta.9`, duga API berubah di v0.2.
3. `ui.Link` — verifikasi interaktifnya di window nyata saat scaffold pertama (riset statis cukup, uji runtime menyusul).

## 9. Brand & logo

Produk: Karta — Pure Native Desktop Email Client. Ekosistem: kompatibel dengan Takka (Agent Harness). Estetika: minimalis, geometris, native OS friendly (macOS squircle, Windows 11, Linux).

### 9.1 Filosofi & konsep visual

Logo Karta menggabungkan tiga elemen visual utama dalam satu bentuk geometris yang sangat sederhana sehingga tetap tajam bahkan pada ukuran system tray atau favicon `16x16px`:

1. Huruf "K" (Karta): batang vertikal di sisi kiri yang menjadi jangkar struktur (melambangkan stabilitas dan kecepatan Go).
2. Lipatan amplop / surat (Mail): dua garis diagonal yang bertemu di tengah membentuk siluet lipatan amplop sekaligus bagian kanan dari huruf K.
3. Arah panah / kursor (Speed & Action): sudut lancip yang mengarah ke dalam/kanan melambangkan pengiriman pesan yang instan dan kesiapan terhubung dengan agent (Takka).

### 9.2 Sistem warna (color palette)

Palet dirancang agar kontras tinggi di Light Mode maupun Dark Mode pada dock atau taskbar OS.

| Peran Warna | Nama | Kode Hex | Penggunaan |
| :--- | :--- | :--- | :--- |
| Primary Background | Obsidian Slate | `#111827` | Latar belakang ikon aplikasi (Dark/Default) |
| Secondary Gradient | Deep Cobalt | `#1E293B` | Gradasi halus atas-ke-bawah pada squircle |
| Primary Accent | Signal Cyan / Go Blue | `#00ADD8` | Garis lipatan amplop / aksen kecepatan |
| Foreground / Stroke | Crisp Alabaster | `#F8FAFC` | Batang utama huruf "K" & elemen utama |
| Light Mode Surface | Pure Paper | `#FFFFFF` | Varian ikon untuk latar terang / monokrom |

### 9.3 Konstruksi grid & proporsi

- Canvas dasar: `512 x 512 px` (rasio `1:1`).
- Safe area (padding): `64 px` di setiap sisi (ikon utama di dalam kotak `384 x 384 px`).
- Stroke weight: `44 px` (konsisten agar tidak pecah saat downscale ke `32x32` atau `16x16`).
- Corner radius:
  - Container app icon (macOS style): `112 px` (~22% dari ukuran kanvas).
  - Ujung garis (stroke-linecap & stroke-linejoin): `round` agar terasa modern dan tidak terlalu kaku.

### 9.4 Aset vektor (SVG siap pakai)

#### 9.4.1 App icon utama (full color — dark squircle)

Cocok untuk ikon aplikasi utama di macOS, Windows, dan Linux desktop entry (`.desktop`).

```svg
<svg width="512" height="512" viewBox="0 0 512 512" fill="none" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="bg-grad" x1="0" y1="0" x2="512" y2="512" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#1E293B"/>
      <stop offset="100%" stop-color="#0F172A"/>
    </linearGradient>
    <linearGradient id="accent-grad" x1="210" y1="140" x2="390" y2="372" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#38BDF8"/>
      <stop offset="100%" stop-color="#0284C7"/>
    </linearGradient>
  </defs>

  <!-- Container Squircle -->
  <rect x="24" y="24" width="464" height="464" rx="108" fill="url(#bg-grad)" stroke="#334155" stroke-width="4"/>

  <!-- Batang Vertikal Huruf K (Kiri) -->
  <path d="M156 144V368" stroke="#F8FAFC" stroke-width="46" stroke-linecap="round"/>

  <!-- Lipatan Amplop / Sayap Kanan Huruf K -->
  <path d="M356 144L224 256L356 368" stroke="url(#accent-grad)" stroke-width="46" stroke-linecap="round" stroke-linejoin="round"/>

  <!-- Titik Aksen / Node (Simbol Koneksi ke Takka) -->
  <circle cx="356" cy="256" r="22" fill="#F8FAFC"/>
</svg>
```

#### 9.4.2 Varian monokrom / system tray icon (`16px` – `24px`)

Digunakan untuk menu bar macOS (`Template Image`) atau system tray Windows/Linux yang menyesuaikan tema OS secara otomatis.

```svg
<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
  <path d="M6 4V20" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"/>
  <path d="M18 4L10.5 12L18 20" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>
  <circle cx="18" cy="12" r="1.5" fill="currentColor"/>
</svg>
```

### 9.5 Panduan penggunaan & ekspor aset

Untuk kebutuhan build lintas platform di Go (`mygo`), simpan aset hasil ekspor di dalam direktori `assets/icons/`:

```text
assets/
└── icons/
    ├── karta.svg          # Master vector
    ├── karta.icns         # macOS App Bundle Icon (16px s/d 1024px)
    ├── karta.ico          # Windows Executable Icon (16, 32, 48, 256px)
    ├── karta_512.png      # Linux / Freedesktop icon
    └── tray_template.png  # Monochrome tray icon (22x22 & 44x44 @2x)
```

Larangan desain (don'ts):

- Jangan menambahkan bayangan (drop shadow) berlapis di dalam gambar SVG (biarkan OS yang merender bayangan alami pada ikon aplikasi).
- Jangan menggunakan lebih dari 2 warna utama pada simbol agar tetap terbaca pada ukuran kecil.
- Jangan mengubah proporsi jarak antara batang vertikal dan sudut lipatan amplop karena akan menghilangkan keterbacaan huruf "K".
