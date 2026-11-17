package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AttachmentFile mengembalikan isi lampiran pesan untuk disimpan ke disk.
func (s *Service) AttachmentData(messageID int, filename string) ([]byte, string, error) {
	f, err := s.db.GetAttachmentData(int64(messageID), filename)
	if err != nil {
		return nil, "", err
	}
	return f.Data, f.ContentType, nil
}

// SaveHTMLForBrowser menyimpan raw_html pesan ke file sementara dan
// mengembalikan path-nya. UI layer yang membukanya di browser (mygo Shell),
// supaya layer app tetap bebas dependensi GUI (siap bridge).
func (s *Service) SaveHTMLForBrowser(ctx context.Context, messageID int) (string, error) {
	h, b, err := s.db.GetMessage(int64(messageID))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(b.RawHTML) == "" {
		return "", fmt.Errorf("pesan tidak punya HTML mentah")
	}
	_ = ctx // file lokal, tanpa jaringan
	dir := os.TempDir()
	name := fmt.Sprintf("karta-%d-%s.html", messageID, sanitizeName(h.Subject))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.RawHTML), 0o600); err != nil {
		return "", fmt.Errorf("tulis HTML sementara: %w", err)
	}
	// Bersihkan berkas lama (>1 jam) agar temp tidak menumpuk.
	go cleanOldTempHTML(dir)
	return path, nil
}

// sanitizeName membuat nama file aman dari subjek.
func sanitizeName(subject string) string {
	var b strings.Builder
	for _, r := range subject {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "pesan"
	}
	return out
}

// cleanOldTempHTML menghapus file karta-*.html berumur > 1 jam. Error diabaikan.
func cleanOldTempHTML(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "karta-") || !strings.HasSuffix(name, ".html") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > time.Hour {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
