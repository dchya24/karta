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
