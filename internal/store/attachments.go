package store

import "fmt"

// AttachmentFile adalah metadata + isi satu lampiran.
type AttachmentFile struct {
	Filename    string
	ContentType string
	Data        []byte
}

// GetAttachmentData mengembalikan isi lampiran berdasarkan pesan + filename.
func (db *DB) GetAttachmentData(messageID int64, filename string) (*AttachmentFile, error) {
	f := &AttachmentFile{Filename: filename}
	err := db.QueryRow(`SELECT content_type, data FROM attachments
		WHERE message_id=? AND filename=? LIMIT 1`, messageID, filename).
		Scan(&f.ContentType, &f.Data)
	if err != nil {
		return nil, fmt.Errorf("baca lampiran %q pesan %d: %w", filename, messageID, err)
	}
	return f, nil
}

// HasRawHTML mengembalikan true bila pesan punya HTML mentah tersimpan.
func (db *DB) HasRawHTML(messageID int64) (bool, error) {
	var rawHTML []byte
	err := db.QueryRow(`SELECT raw_html FROM messages WHERE id=?`, messageID).Scan(&rawHTML)
	if err != nil {
		return false, fmt.Errorf("baca raw_html pesan %d: %w", messageID, err)
	}
	return len(rawHTML) > 0, nil
}
