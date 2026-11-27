package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"karta/internal/mail"
	"karta/internal/store"
)

// ExportOptions adalah opsi ekspor CLI (JSON-friendly). Format: "mbox"
// (default, satu file per folder) atau "eml" (satu file per pesan).
// Directory kosong berarti direktori kerja saat ini. AccountEmail dan
// FolderRole kosong berarti tanpa filter.
type ExportOptions struct {
	Format       string `json:"format"`
	Directory    string `json:"directory"`
	AccountEmail string `json:"accountEmail,omitempty"`
	FolderRole   string `json:"folderRole,omitempty"`
}

// ExportResult adalah hasil ekspor satu folder (JSON-friendly). Path adalah
// file mbox yang dihasilkan, atau direktori .eml untuk format eml. Error
// tidak kosong berarti folder itu gagal; folder lain tetap diekspor.
type ExportResult struct {
	AccountEmail string `json:"accountEmail"`
	FolderName   string `json:"folderName"`
	FolderRole   string `json:"folderRole"`
	Path         string `json:"path"`
	Count        int    `json:"count"`
	Error        string `json:"error,omitempty"`
}

// Export mengekspor pesan lokal dari SQLite ke mbox/EML tanpa jaringan/GUI.
// Pesan fetched=0 (body belum di-cache) diekspor dari metadata dengan body
// kosong; tidak ada fetch IMAP yang dipicu.
func (s *Service) Export(ctx context.Context, opts ExportOptions) ([]ExportResult, error) {
	format := opts.Format
	if format == "" {
		format = "mbox"
	}
	if format != "mbox" && format != "eml" {
		return nil, fmt.Errorf("format tidak dikenal %q: gunakan mbox atau eml", opts.Format)
	}
	dir := opts.Directory
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("buat direktori tujuan %s: %w", dir, err)
	}
	accs, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	var results []ExportResult
	for _, acc := range accs {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if opts.AccountEmail != "" && acc.Email != opts.AccountEmail {
			continue
		}
		folders, err := s.db.ListFolders(int64(acc.ID))
		if err != nil {
			results = append(results, ExportResult{AccountEmail: acc.Email,
				Error: fmt.Sprintf("daftar folder: %v", err)})
			continue
		}
		for _, f := range folders {
			if opts.FolderRole != "" && f.Role != opts.FolderRole {
				continue
			}
			res := ExportResult{AccountEmail: acc.Email, FolderName: f.Name, FolderRole: f.Role}
			path, n, ferr := s.exportFolder(ctx, format, dir, acc.Email, f)
			res.Path, res.Count = path, n
			if ferr != nil {
				res.Error = ferr.Error()
			}
			results = append(results, res)
		}
	}
	return results, nil
}

// exportFolder mengekspor seluruh pesan satu folder: satu file mbox
// ("<email>--<role>.mbox") atau satu file .eml per pesan
// ("<role>/<UID>-<slug-subjek>.eml"). Error menghentikan folder itu saja.
func (s *Service) exportFolder(ctx context.Context, format, dir, accEmail string, f store.Folder) (string, int, error) {
	headers, err := s.db.ListMessages(int64(f.ID), "")
	if err != nil {
		return "", 0, fmt.Errorf("daftar pesan folder %s: %w", f.Name, err)
	}
	count := 0
	switch format {
	case "mbox":
		var buf bytes.Buffer
		for _, h := range headers {
			if err := ctx.Err(); err != nil {
				return "", count, err
			}
			msg, err := s.exportMessage(int64(h.ID))
			if err != nil {
				return "", count, err
			}
			entry, err := mail.BuildMbox(msg)
			if err != nil {
				return "", count, err
			}
			buf.Write(entry)
			count++
		}
		name := sanitizeFilename(accEmail) + "--" + sanitizeFilename(f.Role) + ".mbox"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			return "", count, fmt.Errorf("tulis %s: %w", path, err)
		}
		return path, count, nil
	default: // "eml"
		sub := filepath.Join(dir, sanitizeFilename(f.Role))
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return "", count, fmt.Errorf("buat direktori %s: %w", sub, err)
		}
		for _, h := range headers {
			if err := ctx.Err(); err != nil {
				return "", count, err
			}
			msg, err := s.exportMessage(int64(h.ID))
			if err != nil {
				return "", count, err
			}
			data, err := mail.BuildEML(msg)
			if err != nil {
				return "", count, err
			}
			path := filepath.Join(sub, fmt.Sprintf("%d-%s.eml", h.UID, slugSubject(h.Subject)))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				return "", count, fmt.Errorf("tulis %s: %w", path, err)
			}
			count++
		}
		return sub, count, nil
	}
}

// exportMessage mengumpulkan data satu pesan dari store menjadi input
// pembentukan MIME. Body hanya diisi bila fetched=1; tidak ada fetch IMAP.
func (s *Service) exportMessage(id int64) (*mail.ExportMessage, error) {
	h, b, err := s.db.GetMessage(id)
	if err != nil {
		return nil, fmt.Errorf("baca pesan %d: %w", id, err)
	}
	m := &mail.ExportMessage{
		FromName:  h.Sender,
		FromAddr:  h.SenderAddr,
		To:        h.Recipients,
		Cc:        h.Cc,
		Subject:   h.Subject,
		MessageID: h.MessageID,
	}
	if t, perr := time.Parse(time.RFC3339, h.Date); perr == nil {
		m.Date = t
	}
	if b.Fetched {
		m.PlainText = b.PlainText
	}
	atts, err := s.db.ListAttachmentBlobs(id)
	if err != nil {
		return nil, fmt.Errorf("lampiran pesan %d: %w", id, err)
	}
	for _, a := range atts {
		m.Attachments = append(m.Attachments, mail.ExportAttachment{
			Filename: a.Filename, ContentType: a.ContentType, Data: a.Data})
	}
	return m, nil
}

// sanitizeFilename mengganti pemisah path dan karakter kontrol dengan '_'
// agar nama file aman untuk filesystem.
func sanitizeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, s)
}

// slugSubject mengubah subjek menjadi slug nama file: huruf/angka dipertahankan,
// sisanya '-', dipangkas, dibatasi 60 karakter; kosong menjadi "tanpa-subjek".
func slugSubject(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= 60 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "tanpa-subjek"
	}
	return out
}
