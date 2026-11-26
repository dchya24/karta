# REPORT.md — Batch Paralel: Folder Dinamis + Onboarding (Karta)

Dua fitur dikerjakan paralel di dua worktree, lalu di-merge ke master.

## 1. Sinkronisasi folder dinamis (feat/dynamic-folders)
- Status: DONE
- `tryRefreshAccount` kini menyinkronkan semua mailbox hasil `ListMailboxes`: folder inti dulu (inbox→drafts→sent→archive→trash→junk), lalu non-core ("other") alfabetis. Satu helper `syncOrder`/`roleRank` dipakai untuk urutan sync dan `app.ListFolders` (sidebar) — konsisten dari satu sumber.
- `folderLabel` sudah benar memangkas prefix `[Gmail]/` dsb.; `internal/mail` dan `internal/sync` tidak disentuh; regroup thread & notifikasi inbox-only tidak berubah.
- Test baru (4): non-core ikut sync + unread, urutan core-first, sidebar konsisten, dedupe lintas siklus.
- Catatan desain: siklus penuh membebankan semua folder non-core (timeout 2 menit/akun berlaku); IDLE tetap hanya folder inti — perubahan non-core terlihat di siklus polling berikutnya.

## 2. Onboarding first-run + empty state (feat/onboarding)
- Status: DONE — hanya `internal/ui` berubah.
- Welcome screen: `len(s.sections) == 0` (0 akun) → layar penuh `s.welcome()` — logo, "Karta", tagline "Email client native. Cepat, tenang, teks-first.", tombol "Tambah akun" → `s.showAccount = true`. Tanpa flag persist (deterministik dari jumlah akun).
- Shortcut guard: shortcut (mis. `c` composer) tidak menyala di layar welcome.
- Empty state "Tidak ada pesan" di daftar pesan dikunci test baru `TestEmptyFolderPlaceholder`.
- Test headless: `TestWelcomeScreenFirstRun`, `TestWelcomeButtonOpensAccountForm`, `TestEmptyFolderPlaceholder` — semua lulus (`-race` juga).
- Catatan: ukuran visual welcome (logo 72px, font 28) bisa disetel via screenshot eval `KARTA_UI_EVAL=1`.

## Riwayat merge sebelumnya (untuk konteks)
- `49e950b` — notifikasi OS + thread grouping (konflik `internal/app/service.go`: `RegroupThreads` dulu, lalu `notifyNewMessages`).
- `6706854` — pengiriman notifikasi asinkron FIFO + hint error darwin.

## DoD pasca-merge
Build no-cgo, `go vet`, test penuh, dan `-race` dijalankan ulang di master setelah merge ini.
