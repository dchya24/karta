package mail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"strings"
	"time"
)

// ExportAttachment adalah satu lampiran pesan untuk ekspor (isi BLOB dari
// store; Data boleh nil bila blob tidak tersimpan).
type ExportAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ExportMessage adalah pesan minimal untuk pembentukan RFC822: input murni
// tanpa akses DB/jaringan, sehingga dapat diuji dan dipakai lewat
// internal/app tanpa membuat dependensi ke store. Date zero → header Date
// dihilangkan. RawHTML disimpan apa adanya (fallback "Open in Browser"),
// body ekspor selalu text/plain dari PlainText.
type ExportMessage struct {
	FromName    string
	FromAddr    string
	To          string
	Cc          string
	Subject     string
	Date        time.Time
	MessageID   string
	PlainText   string
	RawHTML     string
	Attachments []ExportAttachment
}

// envelopeTimeFormat adalah format baris amplop mbox klasik
// ("From sender Mon Jan 02 15:04:05 2006").
const envelopeTimeFormat = "Mon Jan 02 15:04:05 2006"

// BuildEML membentuk satu pesan RFC822 (format .eml): header lengkap +
// body text/plain dari PlainText, atau multipart/mixed bila ada lampiran
// dengan BLOB terisi. Baris dibungkus CRLF sesuai RFC 5322.
func BuildEML(m *ExportMessage) ([]byte, error) {
	b, err := buildMessage(m)
	if err != nil {
		return nil, fmt.Errorf("bentuk pesan eml: %w", err)
	}
	return b, nil
}

// BuildMbox membentuk satu entri mbox: baris amplop "From ", isi pesan
// (CRLF dinormalkan ke LF, konvensi mbox Unix) dengan escaping "From "
// menjadi ">From ", dan baris kosong pemisah di akhir.
func BuildMbox(m *ExportMessage) ([]byte, error) {
	b, err := buildMessage(m)
	if err != nil {
		return nil, fmt.Errorf("bentuk pesan mbox: %w", err)
	}
	from := m.FromAddr
	if from == "" {
		from = "MAILER-DAEMON"
	}
	t := m.Date
	if t.IsZero() {
		t = time.Now()
	}
	var out bytes.Buffer
	out.WriteString("From " + from + " " + t.Format(envelopeTimeFormat) + "\n")
	// Normalisasi CRLF ke LF (konvensi mbox Unix) lalu escape baris amplop
	// palsu di isi pesan; pastikan entri berakhir baris kosong pemisah.
	norm := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasSuffix(norm, "\n") {
		norm += "\n"
	}
	out.WriteString(escapeFrom(norm))
	out.WriteString("\n")
	return out.Bytes(), nil
}

// buildMessage membentuk pesan RFC822 utuh (header + body, CRLF).
func buildMessage(m *ExportMessage) ([]byte, error) {
	var w bytes.Buffer
	for _, line := range m.headers() {
		w.WriteString(line + "\r\n")
	}
	atts := attachmentsWithData(m)
	if len(atts) > 0 {
		mp := multipart.NewWriter(&w)
		w.WriteString("MIME-Version: 1.0\r\n")
		w.WriteString("Content-Type: multipart/mixed; boundary=" + mp.Boundary() + "\r\n")
		w.WriteString("\r\n")
		// Part pertama: teks pesan (quoted-printable, UTF-8).
		ph := make(map[string][]string)
		ph["Content-Type"] = []string{`text/plain; charset="utf-8"`}
		ph["Content-Transfer-Encoding"] = []string{"quoted-printable"}
		pw, err := mp.CreatePart(ph)
		if err != nil {
			return nil, fmt.Errorf("buat part teks: %w", err)
		}
		if err := writeQuotedPrintable(pw, m.PlainText); err != nil {
			return nil, fmt.Errorf("tulis body teks: %w", err)
		}
		// Part lampiran: base64 agar aman terhadap data biner.
		for _, att := range atts {
			ct := att.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			ah := make(map[string][]string)
			ah["Content-Type"] = []string{ct}
			if disp := mime.FormatMediaType("attachment",
				map[string]string{"filename": att.Filename}); disp != "" {
				ah["Content-Disposition"] = []string{disp}
			} else {
				ah["Content-Disposition"] = []string{"attachment"}
			}
			ah["Content-Transfer-Encoding"] = []string{"base64"}
			apw, err := mp.CreatePart(ah)
			if err != nil {
				return nil, fmt.Errorf("buat part lampiran %q: %w", att.Filename, err)
			}
			if err := writeBase64(apw, att.Data); err != nil {
				return nil, fmt.Errorf("tulis lampiran %q: %w", att.Filename, err)
			}
		}
		if err := mp.Close(); err != nil {
			return nil, fmt.Errorf("tutup multipart: %w", err)
		}
		return w.Bytes(), nil
	}
	// Body tunggal: text/plain (quoted-printable) — juga dipakai pesan
	// fetched=0 (body kosong).
	w.WriteString("MIME-Version: 1.0\r\n")
	w.WriteString(`Content-Type: text/plain; charset="utf-8"` + "\r\n")
	w.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	w.WriteString("\r\n")
	if err := writeQuotedPrintable(&w, m.PlainText); err != nil {
		return nil, fmt.Errorf("tulis body teks: %w", err)
	}
	return w.Bytes(), nil
}

// headers membentuk baris-baris header pesan (tanpa Content-Type/MIME-Version;
// itu tergantung bentuk body).
func (m *ExportMessage) headers() []string {
	var h []string
	switch {
	case m.FromAddr != "" && m.FromName != "":
		h = append(h, "From: "+encodeWord(m.FromName)+" <"+m.FromAddr+">")
	case m.FromAddr != "":
		h = append(h, "From: <"+m.FromAddr+">")
	case m.FromName != "":
		h = append(h, "From: "+encodeWord(m.FromName))
	}
	if m.To != "" {
		h = append(h, "To: "+m.To)
	}
	if m.Cc != "" {
		h = append(h, "Cc: "+m.Cc)
	}
	if m.Subject != "" {
		h = append(h, "Subject: "+encodeWord(m.Subject))
	}
	if !m.Date.IsZero() {
		h = append(h, "Date: "+m.Date.Format(time.RFC1123Z))
	}
	if m.MessageID != "" {
		mid := m.MessageID
		if !strings.HasPrefix(mid, "<") {
			mid = "<" + mid + ">"
		}
		h = append(h, "Message-ID: "+mid)
	}
	return h
}

// encodeWord meng-encode nilai header berisi non-ASCII (RFC 2047 Q-encoding);
// nilai ASCII dibiarkan apa adanya.
func encodeWord(s string) string {
	if isASCII(s) {
		return s
	}
	return mime.QEncoding.Encode("utf-8", s)
}

// isASCII true bila semua byte ASCII.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// attachmentsWithData mengembalikan lampiran yang BLOB-nya terisi.
func attachmentsWithData(m *ExportMessage) []ExportAttachment {
	out := make([]ExportAttachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		if len(a.Data) > 0 {
			out = append(out, a)
		}
	}
	return out
}

// writeQuotedPrintable menulis teks sebagai quoted-printable.
func writeQuotedPrintable(w io.Writer, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(s)); err != nil {
		return err
	}
	return qp.Close()
}

// writeBase64 menulis data biner base64 terbungkus 76 kolom (CRLF).
func writeBase64(w io.Writer, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		if _, err := io.WriteString(w, enc[:76]+"\r\n"); err != nil {
			return err
		}
		enc = enc[76:]
	}
	_, err := io.WriteString(w, enc+"\r\n")
	return err
}

// escapeFrom mengganti setiap baris isi yang diawali "From " menjadi
// ">From " (konvensi mbox agar baris amplop palsu tidak muncul di body).
// Header tidak pernah diawali "From " (baris header pertama adalah
// "From:" dengan titik dua), jadi cukup ganti setelah newline.
func escapeFrom(s string) string {
	return strings.ReplaceAll(s, "\nFrom ", "\n>From ")
}
