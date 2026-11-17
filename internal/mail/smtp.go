package mail

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gomail "github.com/emersion/go-message/mail"
)

// SMTPConfig adalah parameter koneksi SMTP satu akun. TLS=true berarti
// implicit TLS (biasanya port 465); false = koneksi plain (MVP).
type SMTPConfig struct {
	Host string
	Port int
	TLS  bool
	User string
	// Pass berisi password (AuthPassword) ATAU access token (AuthOAuth2).
	Pass string
	Auth AuthMethod
}

// OutgoingMessage adalah pesan keluar sebelum dibungkus MIME.
// AttachmentPaths berisi path file lokal.
type OutgoingMessage struct {
	From            string
	To              []string
	Cc              []string
	Subject         string
	Body            string
	AttachmentPaths []string
}

// smtpTimeout membatasi durasi sesi SMTP.
const smtpTimeout = 60 * time.Second

// ComposeMessage membangun pesan RFC822 lengkap: text/plain + lampiran
// dalam multipart/mixed bila ada lampiran.
func ComposeMessage(msg OutgoingMessage) ([]byte, error) {
	if len(msg.To) == 0 {
		return nil, fmt.Errorf("penerima (To) kosong")
	}
	var buf strings.Builder
	h := gomail.Header{}
	if err := h.GenerateMessageID(); err != nil {
		return nil, fmt.Errorf("buat Message-ID: %w", err)
	}
	h.SetDate(time.Now())
	h.SetSubject(msg.Subject)
	h.SetAddressList("From", []*gomail.Address{{Address: msg.From}})
	if err := setAddressList(&h, "To", msg.To); err != nil {
		return nil, err
	}
	if err := setAddressList(&h, "Cc", msg.Cc); err != nil {
		return nil, err
	}

	w, err := gomail.CreateWriter(&buf, h)
	if err != nil {
		return nil, fmt.Errorf("buat MIME writer: %w", err)
	}
	if len(msg.AttachmentPaths) == 0 {
		h := gomail.InlineHeader{}
		h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		part, err := w.CreateSingleInline(h)
		if err != nil {
			return nil, fmt.Errorf("buat text part: %w", err)
		}
		if err := writePart(part, msg.Body); err != nil {
			return nil, err
		}
	} else {
		inline, err := w.CreateInline()
		if err != nil {
			return nil, fmt.Errorf("buat inline: %w", err)
		}
		h := gomail.InlineHeader{}
		h.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		part, err := inline.CreatePart(h)
		if err != nil {
			return nil, fmt.Errorf("buat text part: %w", err)
		}
		if err := writePart(part, msg.Body); err != nil {
			return nil, err
		}
		if err := inline.Close(); err != nil {
			return nil, fmt.Errorf("tutup inline: %w", err)
		}
		for _, p := range msg.AttachmentPaths {
			if err := writeAttachment(w, p); err != nil {
				return nil, err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("tutup MIME: %w", err)
	}
	return []byte(buf.String()), nil
}

func writePlainPart(inline interface {
	CreateSingleInline(gomail.InlineHeader) (interface {
		Write([]byte) (int, error)
		Close() error
	}, error)
}, body string) error {
	panic("unused placeholder")
}

// setAddressList mengisi header alamat dari daftar string "a@b" / "Nama <a@b>".
func setAddressList(h *gomail.Header, key string, addrs []string) error {
	if len(addrs) == 0 {
		return nil
	}
	list := make([]*gomail.Address, 0, len(addrs))
	for _, raw := range addrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if name, addr, ok := splitAddress(raw); ok {
			list = append(list, &gomail.Address{Name: name, Address: addr})
		} else {
			list = append(list, &gomail.Address{Address: raw})
		}
	}
	if len(list) == 0 {
		return nil
	}
	h.SetAddressList(key, list)
	return nil
}

// splitAddress memecah "Nama <a@b>" menjadi nama dan alamat.
func splitAddress(raw string) (name, addr string, ok bool) {
	i := strings.LastIndexByte(raw, '<')
	j := strings.IndexByte(raw, '>')
	if i >= 0 && j > i {
		return strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1 : j]), true
	}
	return "", "", false
}

// writePart menulis isi part lalu menutupnya.
func writePart(part io.WriteCloser, body string) error {
	if _, err := part.Write([]byte(body)); err != nil {
		return fmt.Errorf("tulis body: %w", err)
	}
	if err := part.Close(); err != nil {
		return fmt.Errorf("tutup text part: %w", err)
	}
	return nil
}

// writeAttachment menambahkan satu lampiran ke writer.
func writeAttachment(w *gomail.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("baca lampiran %s: %w", path, err)
	}
	ah := gomail.AttachmentHeader{}
	ah.SetContentType("application/octet-stream", nil)
	ah.SetFilename(filepath.Base(path))
	part, err := w.CreateAttachment(ah)
	if err != nil {
		return fmt.Errorf("buat attachment part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("tulis lampiran %s: %w", path, err)
	}
	if err := part.Close(); err != nil {
		return fmt.Errorf("tutup lampiran %s: %w", path, err)
	}
	return nil
}

// SendMail mengirim pesan via SMTP. TLS=true memakai implicit TLS.
func SendMail(cfg SMTPConfig, msg OutgoingMessage) error {
	if msg.From == "" {
		return fmt.Errorf("pengirim (From) kosong")
	}
	data, err := ComposeMessage(msg)
	if err != nil {
		return err
	}
	rcpts := dedup(append(append([]string{}, msg.To...), msg.Cc...))
	if len(rcpts) == 0 {
		return fmt.Errorf("penerima kosong")
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var conn net.Conn
	if cfg.TLS {
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("koneksi SMTP %s: %w", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(smtpTimeout))

	var c *smtp.Client
	c, err = smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("sambungan SMTP: %w", err)
	}
	defer c.Close()
	if cfg.User != "" {
		auth, err := SMTPAuth(cfg.Auth, cfg.Host, cfg.User, cfg.Pass)
		if err != nil {
			return err
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth SMTP %s: %w", cfg.Host, err)
		}
	}
	if err := c.Mail(msg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("tulis pesan: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("akhir DATA: %w", err)
	}
	return c.Quit()
}

// dedup menghapus alamat ganda, mempertahankan urutan.
func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
