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
	folderID, err := svc.db.EnsureFolder(int64(id), "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(folderID), UID: 1, Subject: "rahasia-fts", SenderAddr: "b@x.id",
		Date: "2026-11-17T00:00:00Z",
	}, &store.MessageBody{PlainText: "isi unik fts"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveAccount(ctx, id); err != nil {
		t.Fatal(err)
	}
	if accs, _ = svc.ListAccounts(); len(accs) != 0 {
		t.Fatal("akun harus terhapus")
	}
	var ftsCount int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM messages_fts`).Scan(&ftsCount); err != nil {
		t.Fatal(err)
	}
	if ftsCount != 0 {
		t.Fatalf("baris FTS tersisa %d setelah hapus akun", ftsCount)
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
	if err := svc.MarkRead(context.Background(), m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// stubIMAP mensimulasikan server IMAP untuk test Refresh tanpa jaringan.
type stubIMAP struct {
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

// TestAddAccountUpsert: menambah akun dengan email yang sama memperbarui
// konfigurasi (host baru) tanpa duplikat baris.
func TestAddAccountUpsert(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	id, err := svc.AddAccount(ctx, AccountConfig{
		Email: "aku@contoh.id", Password: "pw1",
		IMAPHost: "imap.lama.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.lama.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := svc.AddAccount(ctx, AccountConfig{
		Email: "aku@contoh.id", Password: "pw2",
		IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPTLS: true,
		Auth: AuthOAuth2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("upsert buat ID baru: %d vs %d", id2, id)
	}
	a, err := svc.db.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 {
		t.Fatalf("duplikat: %d akun", len(a))
	}
	row := a[0]
	if row.IMAPHost != "imap.gmail.com" || row.SMTPHost != "smtp.gmail.com" || row.Auth != "oauth2" {
		t.Fatalf("konfigurasi tidak terperbarui: %+v", row)
	}
	if svc.keyring.(*fakeKeyring).secret["karta/"+row.Email] != "pw2" {
		t.Fatalf("keyring tidak terperbarui")
	}
}

type actionStubIMAP struct {
	stubIMAP
	seenCalls   []struct{ box string; uid uint32; seen bool }
	moveCalls   []struct{ src, dst string; uid uint32 }
	deleteCalls []struct{ box string; uid uint32 }
	nextDestUID uint32
}

func (s *actionStubIMAP) SetSeen(_ context.Context, mailbox string, uid uint32, seen bool) error {
	s.seenCalls = append(s.seenCalls, struct{ box string; uid uint32; seen bool }{mailbox, uid, seen})
	return nil
}

func (s *actionStubIMAP) MoveMessage(_ context.Context, src string, uid uint32, dst string) (uint32, error) {
	s.moveCalls = append(s.moveCalls, struct{ src, dst string; uid uint32 }{src, dst, uid})
	return s.nextDestUID, nil
}

func (s *actionStubIMAP) DeleteMessage(_ context.Context, mailbox string, uid uint32) error {
	s.deleteCalls = append(s.deleteCalls, struct{ box string; uid uint32 }{mailbox, uid})
	return nil
}

func TestMessageActionsAndCc(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	accID, err := svc.AddAccount(ctx, AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := svc.db.EnsureFolder(int64(accID), "INBOX", "inbox")
	archive, _ := svc.db.EnsureFolder(int64(accID), "Archive", "archive")
	trash, _ := svc.db.EnsureFolder(int64(accID), "Trash", "trash")

	msgID, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(inbox), UID: 10, Subject: "uji aksi",
		Sender: "Budi <b@x.id>", SenderAddr: "b@x.id",
		Recipients: "aku@contoh.id", Cc: "cici@x.id",
		Date: "2026-11-17T01:00:00Z", Unread: true,
	}, &store.MessageBody{PlainText: "isi aksi"})
	if err != nil {
		t.Fatal(err)
	}

	stub := &actionStubIMAP{nextDestUID: 99}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	m, err := svc.PeekMessage(int(msgID))
	if err != nil || m.Cc != "cici@x.id" {
		t.Fatalf("PeekMessage Cc=%q err=%v", m.Cc, err)
	}
	if err := svc.MarkRead(ctx, int(msgID), false); err != nil {
		t.Fatal(err)
	}
	if len(stub.seenCalls) != 1 || stub.seenCalls[0].seen != false || stub.seenCalls[0].uid != 10 {
		t.Fatalf("SetSeen calls: %+v", stub.seenCalls)
	}

	// Archive: pindah INBOX -> Archive, UID diperbarui ke 99 dari IMAP.
	if err := svc.Archive(ctx, int(msgID)); err != nil {
		t.Fatal(err)
	}
	if len(stub.moveCalls) != 1 || stub.moveCalls[0].dst != "Archive" {
		t.Fatalf("Archive move: %+v", stub.moveCalls)
	}
	msgs, _ := svc.ListMessages(int(archive), "")
	if len(msgs) != 1 || msgs[0].UID != 99 {
		t.Fatalf("pesan di Archive: %+v", msgs)
	}

	// Delete 1: pindah Archive -> Trash.
	stub.nextDestUID = 105
	if err := svc.Delete(ctx, int(msgID)); err != nil {
		t.Fatal(err)
	}
	tMsgs, _ := svc.ListMessages(int(trash), "")
	if len(tMsgs) != 1 || tMsgs[0].UID != 105 {
		t.Fatalf("pesan di Trash: %+v", tMsgs)
	}

	// Delete 2 (sudah di Trash): hapus permanen + expunge IMAP.
	if err := svc.Delete(ctx, int(msgID)); err != nil {
		t.Fatal(err)
	}
	if len(stub.deleteCalls) != 1 || stub.deleteCalls[0].box != "Trash" || stub.deleteCalls[0].uid != 105 {
		t.Fatalf("DeleteMessage calls: %+v", stub.deleteCalls)
	}
	if tMsgs, _ = svc.ListMessages(int(trash), ""); len(tMsgs) != 0 {
		t.Fatalf("Trash harus kosong: %+v", tMsgs)
	}
}
