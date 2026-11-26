package app

import (
	"context"
	"testing"
	"time"

	"karta/internal/mail"
)

// folderedIMAP adalah stub IMAP dengan header per-mailbox dan catatan urutan
// pemanggilan SyncFolder — untuk test sinkronisasi folder dinamis.
type folderedIMAP struct {
	boxes        []mail.MailboxInfo
	byMailbox    map[string][]mail.HeaderInfo
	syncCalls    []string          // nama mailbox sesuai urutan pemanggilan
	lastSeenUIDs map[string]uint32 // mailbox → lastUID yang diterima
}

func (s *folderedIMAP) ListMailboxes(ctx context.Context) ([]mail.MailboxInfo, error) {
	return s.boxes, nil
}

func (s *folderedIMAP) SyncFolder(ctx context.Context, mailbox string, lastUID uint32) ([]mail.HeaderInfo, uint32, error) {
	s.syncCalls = append(s.syncCalls, mailbox)
	if s.lastSeenUIDs == nil {
		s.lastSeenUIDs = map[string]uint32{}
	}
	s.lastSeenUIDs[mailbox] = lastUID
	var out []mail.HeaderInfo
	max := lastUID
	for _, h := range s.byMailbox[mailbox] {
		if h.UID > lastUID {
			out = append(out, h)
		}
		if h.UID > max {
			max = h.UID
		}
	}
	return out, max, nil
}

func (s *folderedIMAP) FetchRaw(ctx context.Context, mailbox string, uid uint32) ([]byte, error) {
	return []byte("From: b@x.id\r\nSubject: halo\r\n\r\nisi"), nil
}

func (s *folderedIMAP) Close() error { return nil }

func testAccount(t *testing.T, svc *Service) int {
	t.Helper()
	id, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRefreshSyncsNonCoreFolders memastikan folder non-core (role "other")
// ikut disinkron: header pesannya masuk store dan last_seen_uid-nya maju.
func TestRefreshSyncsNonCoreFolders(t *testing.T) {
	svc := newTestService(t)
	accID := testAccount(t, svc)
	stub := &folderedIMAP{
		boxes: []mail.MailboxInfo{
			{Name: "INBOX", Role: "inbox"},
			{Name: "Proyek/Karta", Role: "other"},
		},
		byMailbox: map[string][]mail.HeaderInfo{
			"INBOX": {{UID: 1, Subject: "di inbox", SenderAddr: "a@x.id",
				Date: time.Unix(0, 0).UTC(), Unread: true}},
			"Proyek/Karta": {{UID: 7, Subject: "tiket dinamis", SenderAddr: "b@x.id",
				Date: time.Unix(5, 0).UTC(), Unread: true}},
		},
	}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.syncCalls) != 2 {
		t.Fatalf("folder yang disinkron: %v", stub.syncCalls)
	}
	folders, err := svc.ListFolders(accID)
	if err != nil {
		t.Fatal(err)
	}
	var nonCore *Folder
	for i := range folders {
		if folders[i].Role == "other" {
			nonCore = &folders[i]
		}
	}
	if nonCore == nil {
		t.Fatal("folder non-core tidak terdaftar")
	}
	msgs, err := svc.ListMessages(nonCore.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Subject != "tiket dinamis" || !msgs[0].Unread {
		t.Fatalf("pesan folder non-core: %+v", msgs)
	}
	if nonCore.Unread != 1 {
		t.Fatalf("unread folder non-core = %d, ingin 1", nonCore.Unread)
	}
	f, err := svc.db.FolderByID(int64(nonCore.ID))
	if err != nil {
		t.Fatal(err)
	}
	if f.LastSeenUID != 7 {
		t.Fatalf("last_seen_uid folder non-core = %d, ingin 7", f.LastSeenUID)
	}
}

// TestRefreshSyncOrder memastikan folder inti disinkron dulu sesuai urutan
// roleRank, lalu folder non-core alfabetis.
func TestRefreshSyncOrder(t *testing.T) {
	svc := newTestService(t)
	testAccount(t, svc)
	stub := &folderedIMAP{
		boxes: []mail.MailboxInfo{
			{Name: "Zulu", Role: "other"},
			{Name: "Trash", Role: "trash"},
			{Name: "Alfa", Role: "other"},
			{Name: "Sent", Role: "sent"},
			{Name: "Junk", Role: "junk"},
			{Name: "Drafts", Role: "drafts"},
			{Name: "INBOX", Role: "inbox"},
			{Name: "Archive", Role: "archive"},
		},
		byMailbox: map[string][]mail.HeaderInfo{},
	}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"INBOX", "Drafts", "Sent", "Archive", "Trash", "Junk", "Alfa", "Zulu"}
	if len(stub.syncCalls) != len(want) {
		t.Fatalf("urutan sync: %v", stub.syncCalls)
	}
	for i, w := range want {
		if stub.syncCalls[i] != w {
			t.Fatalf("urutan sync = %v, ingin %v", stub.syncCalls, want)
		}
	}
}

// TestRefreshSyncDedupeGuard memastikan folder non-core memakai LastSeenUID
// yang sama seperti folder inti: siklus kedua tidak mengambil ulang header.
func TestRefreshSyncDedupeGuard(t *testing.T) {
	svc := newTestService(t)
	accID := testAccount(t, svc)
	stub := &folderedIMAP{
		boxes: []mail.MailboxInfo{
			{Name: "INBOX", Role: "inbox"},
			{Name: "Label/Klien", Role: "other"},
		},
		byMailbox: map[string][]mail.HeaderInfo{
			"INBOX": {{UID: 3, Subject: "core", SenderAddr: "a@x.id",
				Date: time.Unix(0, 0).UTC(), Unread: true}},
			"Label/Klien": {{UID: 9, Subject: "noncore", SenderAddr: "b@x.id",
				Date: time.Unix(0, 0).UTC(), Unread: false}},
		},
	}
	old := imapDial
	imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()

	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Siklus kedua harus menerima lastUID = UID tertinggi siklus pertama
	// (header tidak diambil ulang) — guard dedupe generic folder non-core.
	if stub.lastSeenUIDs["INBOX"] != 3 || stub.lastSeenUIDs["Label/Klien"] != 9 {
		t.Fatalf("lastUID siklus terakhir: %v, ingin INBOX=3 Label/Klien=9", stub.lastSeenUIDs)
	}
	if got := len(stub.syncCalls); got != 4 { // 2 folder × 2 siklus
		t.Fatalf("jumlah pemanggilan sync = %d, ingin 4", got)
	}
	if stub.syncCalls[len(stub.syncCalls)-2] != "INBOX" ||
		stub.syncCalls[len(stub.syncCalls)-1] != "Label/Klien" {
		t.Fatalf("siklus kedua tidak lengkap: %v", stub.syncCalls)
	}
	// last_seen_uid sudah maju di store; header tidak terduplikasi: hanya
	// satu pesan per folder meski sync dijalankan dua kali.
	folders, err := svc.ListFolders(accID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		msgs, err := svc.ListMessages(f.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("folder %s berisi %d pesan, ingin 1 (tidak duplikat)", f.Name, len(msgs))
		}
		// INBOX punya 1 belum dibaca; Label/Klien pesannya sudah dibaca.
		wantUnread := map[string]bool{"INBOX": true, "Label/Klien": false}
		unread := 0
		for _, m := range msgs {
			if m.Unread {
				unread++
			}
		}
		if (unread == 1) != wantUnread[f.Name] || f.Unread != unread {
			t.Fatalf("unread folder %s: pesan=%d hitung=%d", f.Name, unread, f.Unread)
		}
	}
}

// TestListFoldersSidebarOrder memastikan urutan folder yang diberikan ke
// sidebar konsisten dengan urutan sinkronisasi: folder inti dulu, lalu
// non-core alfabetis.
func TestListFoldersSidebarOrder(t *testing.T) {
	svc := newTestService(t)
	accID := testAccount(t, svc)
	for _, f := range []struct{ name, role string }{
		{"Zulu", "other"}, {"Trash", "trash"}, {"Alfa", "other"},
		{"Sent", "sent"}, {"Drafts", "drafts"}, {"Junk", "junk"},
		{"INBOX", "inbox"}, {"Archive", "archive"},
	} {
		if _, err := svc.db.EnsureFolder(int64(accID), f.name, f.role); err != nil {
			t.Fatal(err)
		}
	}
	folders, err := svc.ListFolders(accID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range folders {
		got = append(got, f.Name)
	}
	want := []string{"INBOX", "Drafts", "Sent", "Archive", "Trash", "Junk", "Alfa", "Zulu"}
	if len(got) != len(want) {
		t.Fatalf("folder: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("urutan sidebar = %v, ingin %v", got, want)
		}
	}
}
