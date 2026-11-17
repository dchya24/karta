package mail

import (
	"context"
	"fmt"
"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// IMAPConfig adalah parameter koneksi IMAP satu akun.
type IMAPConfig struct {
	Host string
	Port int
	TLS  bool
	User string
	Pass string
}

// MailboxInfo adalah satu folder IMAP beserta peran khususnya bila
// diumumkan server (special-use RFC 6154) atau dikenali dari nama.
type MailboxInfo struct {
	Name string
	Role string // inbox|sent|drafts|trash|archive|junk|other
}

// HeaderInfo adalah metadata satu pesan hasil sync header.
type HeaderInfo struct {
	UID           uint32
	MessageID     string
	InReplyTo     string
	Subject       string
	Sender        string
	SenderAddr    string
	Recipients    string
	Date          time.Time
	Unread        bool
	HasAttachment bool
	Size          int64
}

// Client membungkus satu koneksi IMAP. Tidak thread-safe: pakai satu
// Client per goroutine, tutup dengan Close.
type Client struct {
	c *imapclient.Client
}

// DialIMAP membuka koneksi TLS (atau StartTLS bila cfg.TLS false) dan login.
func DialIMAP(cfg IMAPConfig) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	var c *imapclient.Client
	var err error
	if cfg.TLS {
		c, err = imapclient.DialTLS(addr, nil)
	} else {
		c, err = imapclient.DialStartTLS(addr, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("koneksi IMAP %s: %w", addr, err)
	}
	cl := &Client{c: c}
	if err := c.Login(cfg.User, cfg.Pass).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("login IMAP %s: %w", cfg.User, err)
	}
	return cl, nil
}

// Close mengakhiri sesi IMAP dengan rapi.
func (cl *Client) Close() error {
	cl.c.Logout().Wait()
	return cl.c.Close()
}

// ListMailboxes mengembalikan folder yang bisa dipilih beserta perannya.
func (cl *Client) ListMailboxes(ctx context.Context) ([]MailboxInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := cl.c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("LIST folder: %w", err)
	}
	out := make([]MailboxInfo, 0, len(data))
	for _, m := range data {
		if hasAttr(m.Attrs, imap.MailboxAttrNoSelect) || hasAttr(m.Attrs, imap.MailboxAttrNonExistent) {
			continue
		}
		out = append(out, MailboxInfo{Name: m.Mailbox, Role: mailboxRole(m.Mailbox, m.Attrs)})
	}
	return out, nil
}

// SyncFolder mengambil header pesan ber-UID > lastUID pada mailbox.
// Mengembalikan header terurut UID naik dan UID tertinggi yang diproses.
func (cl *Client) SyncFolder(ctx context.Context, mailbox string, lastUID uint32) ([]HeaderInfo, uint32, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if _, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, 0, fmt.Errorf("SELECT %s: %w", mailbox, err)
	}
	criteria := &imap.SearchCriteria{
		UID: []imap.UIDSet{{{Start: imap.UID(lastUID + 1), Stop: 0}}}, // 0 = "*"
	}
	sd, err := cl.c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, 0, fmt.Errorf("UID SEARCH %s: %w", mailbox, err)
	}
	uids, ok := sd.All.(imap.UIDSet)
	if !ok {
		return nil, 0, fmt.Errorf("UID SEARCH %s: hasil bukan UID set", mailbox)
	}

	var headers []HeaderInfo
	const batchSize = 500
	maxUID := uint32(0)
	for start := 0; start < len(uids); start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		end := min(start+batchSize, len(uids))
		chunk := uids[start:end]
		msgs, err := cl.c.Fetch(chunk, &imap.FetchOptions{
			Envelope:     true,
			Flags:        true,
			InternalDate: true,
			RFC822Size:   true,
			UID:          true,
		}).Collect()
		if err != nil {
			return nil, 0, fmt.Errorf("FETCH header %s: %w", mailbox, err)
		}
		for _, m := range msgs {
			h := headerFromFetch(m)
			headers = append(headers, h)
			if h.UID > maxUID {
				maxUID = h.UID
			}
		}
	}
	return headers, maxUID, nil
}

// FetchRaw mengambil pesan utuh (header + body) ber-UID untuk parsing lokal.
func (cl *Client) FetchRaw(ctx context.Context, mailbox string, uid uint32) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := cl.c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("SELECT %s: %w", mailbox, err)
	}
	set := imap.UIDSetNum(imap.UID(uid))
	msgs, err := cl.c.Fetch(set, &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}},
		UID:         true,
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("FETCH body %s uid=%d: %w", mailbox, uid, err)
	}
	if len(msgs) == 0 || len(msgs[0].BodySection) == 0 {
		return nil, fmt.Errorf("FETCH body %s uid=%d: pesan tidak ditemukan", mailbox, uid)
	}
	data := msgs[0].BodySection[0].Bytes
	if len(data) == 0 {
		return nil, fmt.Errorf("FETCH body %s uid=%d: body kosong", mailbox, uid)
	}
	// Batasi 25 MB: pesan biasa cukup; lampiran besar diambil saat dibuka.
	if len(data) > 25<<20 {
		data = data[:25<<20]
	}
	return data, nil
}

// mailboxRole menentukan peran folder dari special-use attribute, lalu
// fallback nama standar, terakhir "other".
func mailboxRole(name string, attrs []imap.MailboxAttr) string {
	switch {
	case hasAttr(attrs, imap.MailboxAttrSent):
		return "sent"
	case hasAttr(attrs, imap.MailboxAttrDrafts):
		return "drafts"
	case hasAttr(attrs, imap.MailboxAttrTrash):
		return "trash"
	case hasAttr(attrs, imap.MailboxAttrArchive):
		return "archive"
	case hasAttr(attrs, imap.MailboxAttrJunk):
		return "junk"
	case hasAttr(attrs, imap.MailboxAttrAll):
		return "archive"
	}
	n := strings.ToLower(name)
	switch n {
	case "inbox":
		return "inbox"
	case "sent", "sent messages", "sent items", "terkirim":
		return "sent"
	case "drafts", "draf", "konsep":
		return "drafts"
	case "trash", "deleted messages", "deleted items", "sampah", "tempat sampah":
		return "trash"
	case "archive", "arsip":
		return "archive"
	case "junk", "spam":
		return "junk"
	}
	return "other"
}

func hasAttr(attrs []imap.MailboxAttr, want imap.MailboxAttr) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

// headerFromFetch mengubah satu hasil FETCH menjadi HeaderInfo.
func headerFromFetch(m *imapclient.FetchMessageBuffer) HeaderInfo {
	h := HeaderInfo{
		UID: uint32(m.UID),
	}
	if m.Envelope != nil {
		env := m.Envelope
		h.MessageID = env.MessageID
		if len(env.InReplyTo) > 0 {
			h.InReplyTo = env.InReplyTo[0]
		}
		h.Subject = env.Subject
		h.Sender, h.SenderAddr = formatAddrs(env.From)
		h.Recipients = joinAddrs(append(append([]imap.Address{}, env.To...), env.Cc...))
		if env.Date.After(m.InternalDate) {
			h.Date = env.Date
		}
	}
	if h.Date.IsZero() {
		h.Date = m.InternalDate
	}
	h.Size = m.RFC822Size
	for _, f := range m.Flags {
		switch f {
		case imap.FlagSeen:
			// terbaca
		case imap.FlagHasAttachment:
			h.HasAttachment = true
		}
	}
	h.Unread = !hasFlag(m.Flags, imap.FlagSeen)
	return h
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// formatAddrs mengembalikan pengirim pertama yang valid sebagai
// ("Nama <a@b>", "a@b").
func formatAddrs(addrs []imap.Address) (name string, addr string) {
	for _, a := range addrs {
		if a.Addr() == "" {
			continue
		}
		if a.Name != "" {
			return fmt.Sprintf("%s <%s>", a.Name, a.Addr()), a.Addr()
		}
		return "", a.Addr()
	}
	return "", ""
}

// joinAddrs menggabungkan daftar alamat menjadi "a@b, C <d@e>".
func joinAddrs(addrs []imap.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Addr() == "" {
			continue
		}
		if a.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.Name, a.Addr()))
		} else {
			parts = append(parts, a.Addr())
		}
	}
	return strings.Join(parts, ", ")
}
