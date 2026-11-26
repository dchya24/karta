package mail

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

// ParsedAttachment adalah lampiran hasil ekstraksi MIME.
type ParsedAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message adalah hasil parsing satu pesan MIME. PlainText selalu terisi
// (dikonversi dari HTML bila pesan tidak punya text/plain); RawHTML disimpan
// apa adanya hanya untuk fallback "Open in Browser". Header identitas thread
// (MessageID/InReplyTo/References) dipakai pengelompokan percakapan.
type Message struct {
	Subject     string
	From        string
	To          string
	Cc          string
	Date        time.Time
	MessageID   string
	InReplyTo   string
	References  string
	PlainText   string
	RawHTML     string
	Attachments []ParsedAttachment
}

// maxAttachment membatasi ukuran lampiran yang diekstrak ke memori (10 MB).
const maxAttachment = 10 << 20

// ParseMessage mem-parse raw bytes pesan MIME (header + body).
func ParseMessage(raw []byte) (*Message, error) {
	r, err := mail.CreateReader(strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("buka pesan MIME: %w", err)
	}
	msg := &Message{}
	if s, err := r.Header.Subject(); err == nil {
		msg.Subject = s
	}
	msg.From = formatAddressList(&r.Header, "From")
	msg.To = formatAddressList(&r.Header, "To")
	msg.Cc = formatAddressList(&r.Header, "Cc")
	if d, err := r.Header.Date(); err == nil {
		msg.Date = d
	}
	// Header identitas thread (Get case-insensitive; nilai mentah dipertahankan,
	// normalisasi dilakukan di internal/store).
	msg.MessageID = r.Header.Get("Message-Id")
	msg.InReplyTo = r.Header.Get("In-Reply-To")
	msg.References = r.Header.Get("References")

	var plain, htmlBody string
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("baca part MIME: %w", err)
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			body, readErr := io.ReadAll(io.LimitReader(p.Body, maxAttachment))
			if readErr != nil {
				return nil, fmt.Errorf("baca body inline: %w", readErr)
			}
			if strings.HasPrefix(ct, "text/plain") && plain == "" {
				plain = strings.TrimRight(string(body), "\r\n")
			} else if strings.HasPrefix(ct, "text/html") && htmlBody == "" {
				htmlBody = string(body)
			}
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			data, readErr := io.ReadAll(io.LimitReader(p.Body, maxAttachment))
			if readErr != nil {
				return nil, fmt.Errorf("baca lampiran %q: %w", filename, readErr)
			}
			ct, _, _ := h.ContentType()
			msg.Attachments = append(msg.Attachments, ParsedAttachment{
				Filename:    filename,
				ContentType: ct,
				Data:        data,
			})
		}
	}

	msg.PlainText = plain
	if msg.PlainText == "" && htmlBody != "" {
		msg.PlainText = HTMLToText(htmlBody)
	}
	msg.RawHTML = htmlBody
	return msg, nil
}

// formatAddressList mengembalikan daftar alamat sebagai "Nama <a@b>, C <d@e>".
func formatAddressList(h *mail.Header, key string) string {
	addrs, err := h.AddressList(key)
	if err != nil || len(addrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.Name, a.Address))
		} else {
			parts = append(parts, a.Address)
		}
	}
	return strings.Join(parts, ", ")
}
