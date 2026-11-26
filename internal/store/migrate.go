package store

import "fmt"

// migrations berisi satu entri per versi skema. Entri baru hanya boleh
// ditambahkan, tidak boleh diubah.
var migrations = []string{
	// v1: skema awal (lihat docs/DESIGN.md §4).
	`CREATE TABLE accounts (
		id         INTEGER PRIMARY KEY,
		email      TEXT NOT NULL UNIQUE,
		imap_host  TEXT NOT NULL,
		imap_port  INTEGER NOT NULL,
		imap_tls   INTEGER NOT NULL DEFAULT 1,
		smtp_host  TEXT NOT NULL,
		smtp_port  INTEGER NOT NULL,
		smtp_tls   INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`,
	`CREATE TABLE folders (
		id            INTEGER PRIMARY KEY,
		account_id    INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		name          TEXT NOT NULL,
		role          TEXT NOT NULL,
		last_seen_uid INTEGER NOT NULL DEFAULT 0,
		UNIQUE(account_id, name)
	)`,
	`CREATE TABLE messages (
		id             INTEGER PRIMARY KEY,
		folder_id      INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
		uid            INTEGER NOT NULL,
		message_id     TEXT,
		in_reply_to    TEXT,
		subject        TEXT,
		sender         TEXT,
		sender_addr    TEXT,
		recipients     TEXT,
		date           TEXT,
		unread         INTEGER NOT NULL DEFAULT 1,
		has_attachment INTEGER NOT NULL DEFAULT 0,
		size           INTEGER NOT NULL DEFAULT 0,
		plain_text     TEXT,
		raw_html       TEXT,
		fetched        INTEGER NOT NULL DEFAULT 0,
		UNIQUE(folder_id, uid)
	)`,
	`CREATE INDEX idx_messages_folder ON messages(folder_id, date DESC)`,
	`CREATE TABLE attachments (
		id           INTEGER PRIMARY KEY,
		message_id   INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
		filename     TEXT NOT NULL,
		content_type TEXT,
		size         INTEGER NOT NULL,
		data         BLOB
	)`,
	// FTS5 tanpa content=: menyimpan salinan teks yang dicari; rowid = messages.id.
	`CREATE VIRTUAL TABLE messages_fts USING fts5(
		subject, sender_addr, recipients, plain_text
	)`,
	// v2: kolom metode autentikasi akun (password | oauth2).
	`ALTER TABLE accounts ADD COLUMN auth TEXT NOT NULL DEFAULT 'password'`,
	// v3: kolom Cc terpisah agar reading pane dan Reply All menampilkan Cc akurat.
	`ALTER TABLE messages ADD COLUMN cc TEXT`,
	// v4: identitas thread percakapan. thread_id = kunci grup per-folder
	// (hasil ThreadSeedKey/RegroupThreads); refs menyimpan header References
	// mentah (in_reply_to sudah ada sejak v1, kini ikut terisi). Index
	// (folder, thread, date, uid) mendukung query pesan-terbaru-per-thread
	// tanpa full scan tabel.
	`ALTER TABLE messages ADD COLUMN thread_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN refs TEXT NOT NULL DEFAULT '';
	CREATE INDEX idx_messages_thread ON messages(folder_id, thread_id, date DESC, uid DESC)`,
}

// threadSchemaVersion adalah versi migrasi yang menambah kolom thread.
// Setelah migrasi ini diterapkan, backfillThreadIDs dijalankan sekali untuk
// mengisi thread_id pesan lama.
const threadSchemaVersion = 4

// migrate menjalankan migrasi yang belum diterapkan, sekaligus memverifikasi
// dukungan FTS5 sejak awal.
func (db *DB) migrate() error {
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return fmt.Errorf("aktifkan WAL: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		return fmt.Errorf("buat schema_migrations: %w", err)
	}

	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("baca versi skema: %w", err)
	}
	threadJustApplied := false
	for i, stmt := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("mulai tx migrasi v%d: %w", v, err)
		}
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrasi v%d: %w", v, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, v); err != nil {
			tx.Rollback()
			return fmt.Errorf("catat migrasi v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migrasi v%d: %w", v, err)
		}
		if v == threadSchemaVersion {
			threadJustApplied = true
		}
	}
	if threadJustApplied {
		// Backfill thread_id pesan lama sekali saja, di satu transaksi:
		// gagal di tengah = rollback penuh, thread_id tetap kosong.
		if err := db.backfillThreadIDs(); err != nil {
			return fmt.Errorf("backfill thread_id: %w", err)
		}
	}
	return nil
}

// backfillThreadIDs menghitung ulang thread_id semua folder (pesanan lama
// dari sebelum kolom thread_id ada).
func (db *DB) backfillThreadIDs() error {
	rows, err := db.Query(`SELECT id FROM folders`)
	if err != nil {
		return fmt.Errorf("daftar folder backfill: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("baca folder backfill: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterasi folder backfill: %w", err)
	}
	rows.Close()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("mulai tx backfill: %w", err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		if err := regroupThreadsTx(tx, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backfill: %w", err)
	}
	return nil
}
