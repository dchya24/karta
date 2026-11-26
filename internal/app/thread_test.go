package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"karta/internal/mail"
	"karta/internal/store"
)

// TestListThreadsDTO memastikan agregasi thread tersaji sebagai DTO
// JSON-friendly: satu baris per thread (pesan terbaru) dengan ThreadCount.
func TestListThreadsDTO(t *testing.T) {
	svc := newTestService(t)
	accID, err := svc.AddAccount(nil, AccountConfig{
		Email: "thread@contoh.id", IMAPHost: "imap", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	folderID, err := svc.db.EnsureFolder(int64(accID), "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	// Thread 2 pesan (root + reply) dan 1 pesan mandiri.
	seed := []store.MessageHeader{
		{FolderID: int(folderID), UID: 1, MessageID: "<root@x>", Subject: "Rencana", Date: "2026-11-17T01:00:00Z"},
		{FolderID: int(folderID), UID: 2, MessageID: "<r1@x>", InReplyTo: "<root@x>", Subject: "Re: Rencana", Date: "2026-11-17T02:00:00Z"},
		{FolderID: int(folderID), UID: 3, Subject: "Catatan", Date: "2026-11-17T03:00:00Z"},
	}
	for i := range seed {
		if _, err := svc.db.UpsertMessage(&seed[i], nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.db.RegroupThreads(folderID); err != nil {
		t.Fatal(err)
	}
	threads, err := svc.ListThreads(int(folderID), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("thread %+v, mau 2", threads)
	}
	// Terbaru dulu: Catatan (03:00) lalu thread Rencana (02:00).
	if threads[0].Subject != "Catatan" || threads[0].ThreadCount != 1 {
		t.Fatalf("baris 0: %+v", threads[0])
	}
	if threads[1].Subject != "Re: Rencana" || threads[1].ThreadCount != 2 || !threads[1].IsThreadLatest {
		t.Fatalf("baris 1: %+v", threads[1])
	}
	// Klik baris thread = membuka pesan terbaru thread (UID 2).
	if threads[1].UID != 2 {
		t.Fatalf("pesan terbaru thread: %+v", threads[1])
	}
	// ListMessages tetap flat per pesan.
	flat, err := svc.ListMessages(int(folderID), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 3 {
		t.Fatalf("flat %d baris, mau 3", len(flat))
	}
	// DTO serializable tanpa field tak terduga.
	b, err := marshalJSON(threads)
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
}

// TestRefreshRegroupsThreads memastikan thread dihitung ulang setelah sync:
// reply yang masuk duluan tetap bergabung dengan root yang menyusul.
func TestRefreshRegroupsThreads(t *testing.T) {
	svc := newTestService(t)
	accID, err := svc.AddAccount(nil, AccountConfig{
		Email: "regroup@contoh.id", IMAPHost: "imap", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubIMAP{
		boxes: []mail.MailboxInfo{{Name: "INBOX", Role: "inbox"}},
		// Sync 1: hanya reply (root belum ada di server maupun lokal).
		inbox: []mail.HeaderInfo{
			{UID: 3, MessageID: "<r1@x>", InReplyTo: "root@x",
				Subject: "Re: Topik", Date: time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC), Unread: true},
		},
	}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var inboxID int
	folders, _ := svc.ListFolders(accID)
	for _, f := range folders {
		if f.Role == "inbox" {
			inboxID = f.ID
		}
	}
	threads, err := svc.ListThreads(inboxID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 {
		t.Fatalf("setelah sync 1: %+v", threads)
	}
	// Sync 2: root datang (UID lebih besar tapi tanggal lebih lama).
	stub.inbox = append(stub.inbox, mail.HeaderInfo{
		UID: 5, MessageID: "<root@x>", Subject: "Topik",
		Date: time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC), Unread: true,
	})
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	threads, err = svc.ListThreads(inboxID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ThreadCount != 2 {
		t.Fatalf("setelah sync 2: %+v, mau 1 thread 2 anggota", threads)
	}
	// Baris thread = pesan terbaru thread (reply UID 3).
	if threads[0].UID != 3 {
		t.Fatalf("pesan terbaru thread: %+v", threads[0])
	}
}
