package mail

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func TestMailboxRoleSpecialUse(t *testing.T) {
	cases := []struct {
		name  string
		attrs []imap.MailboxAttr
		want  string
	}{
		{"Whatever", []imap.MailboxAttr{imap.MailboxAttrSent}, "sent"},
		{"Drafts", []imap.MailboxAttr{imap.MailboxAttrDrafts}, "drafts"},
		{"Deleted", []imap.MailboxAttr{imap.MailboxAttrTrash}, "trash"},
		{"Cool", []imap.MailboxAttr{imap.MailboxAttrArchive}, "archive"},
		{"Bulk", []imap.MailboxAttr{imap.MailboxAttrJunk}, "junk"},
	}
	for _, c := range cases {
		if got := mailboxRole(c.name, c.attrs); got != c.want {
			t.Fatalf("role(%q)=%q mau %q", c.name, got, c.want)
		}
	}
}

func TestMailboxRoleByName(t *testing.T) {
	cases := map[string]string{
		"INBOX": "inbox", "Sent": "sent", "Terkirim": "sent",
		"Trash": "trash", "Sampah": "trash", "Archive": "archive",
		"Junk": "junk", "Spam": "junk", "Proyek/Klien": "other",
	}
	for name, want := range cases {
		if got := mailboxRole(name, nil); got != want {
			t.Fatalf("role(%q)=%q mau %q", name, got, want)
		}
	}
}

func TestHeaderFromFetch(t *testing.T) {
	date := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	m := &imapclient.FetchMessageBuffer{
		UID:          42,
		Flags:        []imap.Flag{imap.FlagHasAttachment}, // tanpa \Seen → belum dibaca
		InternalDate: date,
		RFC822Size:   1234,
		Envelope: &imap.Envelope{
			MessageID: "<1@x>",
			Subject:   "Uji",
			From:      []imap.Address{{Name: "Budi", Mailbox: "budi", Host: "x.id"}},
			To:        []imap.Address{{Mailbox: "aku", Host: "contoh.id"}},
			Cc:        []imap.Address{{Name: "Cici", Mailbox: "cici", Host: "x.id"}},
			Date:      date.Add(time.Hour),
		},
	}
	h := headerFromFetch(m)
	if h.UID != 42 || h.Subject != "Uji" {
		t.Fatalf("header %+v", h)
	}
	if h.Sender != "Budi <budi@x.id>" || h.SenderAddr != "budi@x.id" {
		t.Fatalf("sender %q %q", h.Sender, h.SenderAddr)
	}
	want := "aku@contoh.id, Cici <cici@x.id>"
	if h.Recipients != want {
		t.Fatalf("recipients %q mau %q", h.Recipients, want)
	}
	if h.To != "aku@contoh.id" || h.Cc != "Cici <cici@x.id>" {
		t.Fatalf("to=%q cc=%q", h.To, h.Cc)
	}
	if !h.Unread || !h.HasAttachment {
		t.Fatalf("unread=%v attachment=%v", h.Unread, h.HasAttachment)
	}
	if !h.Date.Equal(date.Add(time.Hour)) {
		t.Fatalf("date %v", h.Date)
	}
	if h.Size != 1234 {
		t.Fatalf("size %d", h.Size)
	}
}
