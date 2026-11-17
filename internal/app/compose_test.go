package app

import (
	"context"
	"strings"
	"testing"

	"karta/internal/mail"
	"karta/internal/store"
)

// appendStub adalah IMAPClient stub yang mencatat APPEND (polistik stubIMAP).
type appendStub struct {
	boxes     []mail.MailboxInfo
	appends   []appendCall
	listCalls int
}

type appendCall struct {
	mailbox string
	data    string
}

func (s *appendStub) ListMailboxes(ctx context.Context) ([]mail.MailboxInfo, error) {
	s.listCalls++
	return s.boxes, nil
}

func (s *appendStub) SyncFolder(ctx context.Context, mailbox string, lastUID uint32) ([]mail.HeaderInfo, uint32, error) {
	return nil, 0, nil
}

func (s *appendStub) FetchRaw(ctx context.Context, mailbox string, uid uint32) ([]byte, error) {
	return nil, nil
}

func (s *appendStub) Append(ctx context.Context, mailbox string, data []byte) error {
	s.appends = append(s.appends, appendCall{mailbox, string(data)})
	return nil
}

func (s *appendStub) Close() error { return nil }

// TestSaveDraftAppendsToDrafts memastikan draft masuk folder role "drafts".
func TestSaveDraftAppendsToDrafts(t *testing.T) {
	svc := newTestService(t)
	accID, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stub := &appendStub{boxes: []mail.MailboxInfo{
		{Name: "INBOX", Role: "inbox"}, {Name: "Drafts", Role: "drafts"},
	}}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	d := Draft{To: "dia@contoh.id", Subject: "Draf uji", Body: "isi draf"}
	if err := svc.SaveDraft(context.Background(), accID, d); err != nil {
		t.Fatal(err)
	}
	if len(stub.appends) != 1 {
		t.Fatalf("append %d kali, mau 1", len(stub.appends))
	}
	if stub.appends[0].mailbox != "Drafts" {
		t.Fatalf("mailbox %q, mau Drafts", stub.appends[0].mailbox)
	}
	for _, want := range []string{"Subject: Draf uji", "isi draf", "dia@contoh.id"} {
		if !strings.Contains(stub.appends[0].data, want) {
			t.Fatalf("data APPEND tidak mengandung %q", want)
		}
	}
}

// TestSaveDraftWithoutDraftsFolder memastikan error jelas bila tak ada folder.
func TestSaveDraftWithoutDraftsFolder(t *testing.T) {
	svc := newTestService(t)
	accID, _ := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	stub := &appendStub{boxes: []mail.MailboxInfo{{Name: "INBOX", Role: "inbox"}}}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	if err := svc.SaveDraft(context.Background(), accID, Draft{To: "x@y"}); err == nil {
		t.Fatal("tanpa folder Drafts harus error")
	}
}

// TestSendDraftCopiesToSent memastikan salinan terkirim masuk folder Sent.
func TestSendDraftCopiesToSent(t *testing.T) {
	svc := newTestService(t)
	accID, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "127.0.0.1", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "127.0.0.1", SMTPPort: smtpFakePort(t), SMTPTLS: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	stub := &appendStub{boxes: []mail.MailboxInfo{{Name: "Sent", Role: "sent"}}}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	err = svc.SendDraft(context.Background(), accID, Draft{
		To: "dia@contoh.id", Subject: "Kirim uji", Body: "isi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.appends) != 1 || stub.appends[0].mailbox != "Sent" {
		t.Fatalf("salinan Sent tidak tercatat: %+v", stub.appends)
	}
	if !strings.Contains(stub.appends[0].data, "Subject: Kirim uji") {
		t.Fatalf("salinan salah: %q", stub.appends[0].data)
	}
}

// TestAttachmentDataAndRawHTML memastikan lampiran + raw_html terbaca kembali.
func TestAttachmentDataAndRawHTML(t *testing.T) {
	svc := newTestService(t)
	accID, _ := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	inbox, _ := svc.db.EnsureFolder(int64(accID), "INBOX", "inbox")
	id, err := svc.db.UpsertMessage(&store.MessageHeader{
		FolderID: int(inbox), UID: 9, Subject: "html", Date: "2026-11-17T00:00:00Z",
		Unread: true,
	}, &store.MessageBody{PlainText: "teks", RawHTML: "<p>html</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.db.InsertAttachment(id, "file.pdf", "application/pdf", []byte("PDFDATA")); err != nil {
		t.Fatal(err)
	}
	data, ct, err := svc.AttachmentData(int(id), "file.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "PDFDATA" || ct != "application/pdf" {
		t.Fatalf("lampiran %q %q", data, ct)
	}
	if _, _, err := svc.AttachmentData(int(id), "hilang.txt"); err == nil {
		t.Fatal("lampiran tak ada harus error")
	}
	has, err := svc.db.HasRawHTML(id)
	if err != nil || !has {
		t.Fatalf("HasRawHTML %v %v", has, err)
	}
}
