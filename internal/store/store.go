package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB membungkus koneksi SQLite tunggal untuk seluruh aplikasi.
type DB struct {
	*sql.DB
}

// Open membuka (atau membuat) database di path, menjalankan migrasi, dan
// mengaktifkan WAL + foreign_keys. Gunakan path ":memory:" untuk test.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("buka sqlite %s: %w", path, err)
	}
	// Satu koneksi: modernc/sqlite aman konkuren via antrian, dan WAL
	// hanya memberi manfaat dengan lebih dari satu koneksi.
	sqlDB.SetMaxOpenConns(1)
	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}
