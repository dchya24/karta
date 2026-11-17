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
}

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
	}
	return nil
}
