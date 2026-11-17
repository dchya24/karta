package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"karta/internal/mail"
	"karta/internal/store"
)

// fakeKeyring mencatat kredensial di memori untuk test tanpa OS keyring.
type fakeKeyring struct {
	secret map[string]string
}

func (f *fakeKeyring) Set(service, user, password string) error {
	f.secret[service+"/"+user] = password
	return nil
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	pw, ok := f.secret[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return pw, nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	delete(f.secret, service+"/"+user)
	return nil
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := New(db)
	svc.keyring = &fakeKeyring{secret: map[string]string{}}
	t.Cleanup(func() { svc.Close() })
	return svc
}

func TestAddAndRemoveAccount(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	id, err := svc.AddAccount(ctx, AccountConfig{
		Email: "aku@contoh.id", Password: "rahasia",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	accs, err := svc.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].ID != id {
		t.Fatalf("akun %+v", accs)
	}
	// Password tidak boleh bocor lewat DTO mana pun.
	b, _ := marshalJSON(accs)
	if contains(string(b), "rahasia") {
		t.Fatal("password bocor di serialisasi DTO")
	}
	if err := svc.RemoveAccount(ctx, id); err != nil {
		t.Fatal(err)
	}
	if accs, _ = svc.ListAccounts(); len(accs) != 0 {
		t.Fatal("akun harus terhapus")
	}
}

func TestAddAccountValidation(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.AddAccount(context.Background(), AccountConfig{Email: "x@y"}); err == nil {
		t.Fatal("akun tanpa host harus ditolak")
	}
}

func TestMessageFlow(t *testing.T) {
	svc := newTestService(t)
	accID, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.EnsureFolder(int64(accID), "INBOX", "inbox"); err != nil {
		t.Fatal(err)
	}
	// Seed langsung lewat store (simulasi hasil sync).
	if _, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: 1, UID: 1, Subject: "halo", Sender: "b <b@x.id>",
		SenderAddr: "b@x.id", Date: "2026-11-17T00:00:00Z", Unread: true,
	}, &store.MessageBody{PlainText: "isi halo"}); err != nil {
		t.Fatal(err)
	}
	folders, err := svc.ListFolders(accID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 {
		t.Fatalf("folder %+v", folders)
	}
	msgs, err := svc.ListMessages(1, "halo")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !msgs[0].Unread {
		t.Fatalf("pesan %+v", msgs)
	}
	m, err := svc.PeekMessage(msgs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.PlainText != "isi halo" {
		t.Fatalf("isi %q", m.PlainText)
	}
	if m.Unread {
		t.Fatal("PeekMessage harus menandai terbaca")
	}
	if err := svc.MarkRead(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// stubIMAP mensimulasikan server IMAP untuk test Refresh tanpa jaringan.
type stubIMAP struct{
	boxes []mail.MailboxInfo
	inbox []mail.HeaderInfo
}

func (s *stubIMAP) ListMailboxes(ctx context.Context) ([]mail.MailboxInfo, error) {
	return s.boxes, nil
}

func (s *stubIMAP) SyncFolder(ctx context.Context, mailbox string, lastUID uint32) ([]mail.HeaderInfo, uint32, error) {
	var out []mail.HeaderInfo
	max := lastUID
	for _, h := range s.inbox {
		if h.UID > lastUID {
			out = append(out, h)
		}
		if h.UID > max {
			max = h.UID
		}
	}
	return out, max, nil
}

func (s *stubIMAP) FetchRaw(ctx context.Context, mailbox string, uid uint32) ([]byte, error) {
	return []byte("From: b@x.id\r\nSubject: halo\r\n\r\nisi"), nil
}

func (s *stubIMAP) Close() error { return nil }

// TestRefreshWithStub memastikan header baru masuk store dan last_seen_uid maju.
func TestRefreshWithStub(t *testing.T) {
	svc := newTestService(t)
	accID, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) {
		return &stubIMAP{
			boxes: []mail.MailboxInfo{{Name: "INBOX", Role: "inbox"},
				{Name: "Arsip", Role: "archive"}},
			inbox: []mail.HeaderInfo{
				{UID: 3, Subject: "halo", Sender: "Budi <b@x.id>", SenderAddr: "b@x.id",
					Date: time.Unix(0, 0).UTC(), Unread: true},
				{UID: 5, Subject: "dua", SenderAddr: "c@x.id",
					Date: time.Unix(10, 0).UTC(), Unread: false},
			},
		}, nil
	}
	defer func() { imapDial = old }()

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	folders, err := svc.ListFolders(accID)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 {
		t.Fatalf("folder %+v (folder non-inti tetap tersimpan)", folders)
	}
	var inboxID int
	for _, f := range folders {
		if f.Role == "inbox" {
			inboxID = f.ID
		}
	}
	msgs, err := svc.ListMessages(inboxID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("pesan %+v", msgs)
	}
	// last_seen_uid maju: sync ulang tidak menduplikasi.
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if msgs, _ = svc.ListMessages(inboxID, ""); len(msgs) != 2 {
		t.Fatalf("duplikasi setelah sync ulang: %+v", msgs)
	}
}

func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }
