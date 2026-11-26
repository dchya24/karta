package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"karta/internal/mail"
)

// fakeNotifier merekam panggilan notifikasi; tidak pernah menyentuh OS.
type fakeNotifier struct {
	mu    sync.Mutex
	calls []NewMessage
	err   error
}

func (f *fakeNotifier) NotifyNewMessage(_ context.Context, n NewMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, n)
	return f.err
}

func (f *fakeNotifier) snapshot() []NewMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]NewMessage(nil), f.calls...)
}

func TestDedupeKey(t *testing.T) {
	withID := NewMessage{MessageID: "<a@x>", AccountEmail: "a@b", FolderName: "INBOX", UID: 1}
	if k := withID.dedupeKey(); k != "id:<a@x>" {
		t.Fatalf("kunci Message-ID %q", k)
	}
	withoutID := NewMessage{AccountEmail: "a@b", FolderName: "INBOX", UID: 7}
	if k := withoutID.dedupeKey(); k != "uid:a@b/INBOX/7" {
		t.Fatalf("kunci UID+folder %q", k)
	}
	// Folder sama, akun beda → kunci beda.
	other := withoutID
	other.AccountEmail = "z@z"
	if other.dedupeKey() == withoutID.dedupeKey() {
		t.Fatal("akun berbeda harus punya kunci berbeda")
	}
}

// TestGateBackfillThenNotify: sync awal folder (backfill) tidak dinotifikasi;
// pesan yang benar-benar baru tiba setelahnya dinotifikasi sekali.
func TestGateBackfillThenNotify(t *testing.T) {
	fake := &fakeNotifier{}
	gate := newSessionNotifier(fake)
	ctx := context.Background()
	folder := int64(1)
	headers1 := []mail.HeaderInfo{
		{UID: 3, MessageID: "<satu@x>", Sender: "Budi <b@x.id>", Subject: "halo"},
		{UID: 5, Sender: "c@x.id", Subject: "tanpa-id"},
	}

	// Sync awal folder: backfill, tidak ada notifikasi.
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id", headers1) // backfill
	gate.waitSends()
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("backfill dinotifikasi: %+v", calls)
	}

	// Pesan lama dikirim ulang server (reconnect IDLE): tetap hening.
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id", headers1)
	gate.waitSends()
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("pesan lama dinotifikasi ulang: %+v", calls)
	}

	// Pesan baru benar-benar tiba: satu notifikasi, body sender+subjek.
	headers2 := append(append([]mail.HeaderInfo(nil), headers1...),
		mail.HeaderInfo{UID: 9, MessageID: "<baru@x>", Sender: "Cici <cici@x.id>", Subject: "undangan"})
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id", headers2)
	gate.waitSends()
	calls := fake.snapshot()
	if len(calls) != 1 {
		t.Fatalf("notifikasi %d kali, mau 1: %+v", len(calls), calls)
	}
	want := NewMessage{AccountEmail: "aku@contoh.id", FolderName: "INBOX",
		MessageID: "<baru@x>", UID: 9, Sender: "Cici <cici@x.id>", Subject: "undangan"}
	if calls[0] != want {
		t.Fatalf("notifikasi %+v, mau %+v", calls[0], want)
	}

	// Pesan yang sama di siklus berikutnya: tidak dinotifikasi dua kali.
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id", headers2)
	gate.waitSends()
	if calls := fake.snapshot(); len(calls) != 1 {
		t.Fatalf("duplikat dinotifikasi: %+v", calls)
	}
}

// TestGateFolderArmedIndependently: folder yang muncul belakangan (mis. akun
// baru ditambahkan di tengah sesi) menjalani backfill sendiri.
func TestGateFolderArmedIndependently(t *testing.T) {
	fake := &fakeNotifier{}
	gate := newSessionNotifier(fake)
	ctx := context.Background()

	// Folder inbox sudah armed dengan pesan lama.
	gate.observe(ctx, 1, "INBOX", "aku@contoh.id",
		[]mail.HeaderInfo{{UID: 1, MessageID: "<lama@x>", Subject: "lama"}})
	// Akun baru muncul: sync awal Archive-nya menyeret banyak pesan.
	gate.observe(ctx, 2, "Arsip", "baru@contoh.id",
		[]mail.HeaderInfo{{UID: 1, MessageID: "<backfill@x>", Subject: "backfill"},
			{UID: 2, MessageID: "<backfill2@x>", Subject: "backfill2"}})
	gate.waitSends()
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("backfill folder baru dinotifikasi: %+v", calls)
	}
	// Pesan baru folder baru dinotifikasi.
	gate.observe(ctx, 2, "Arsip", "baru@contoh.id",
		[]mail.HeaderInfo{{UID: 3, MessageID: "<masuk@x>", Subject: "masuk"}})
	gate.waitSends()
	calls := fake.snapshot()
	if len(calls) != 1 || calls[0].Subject != "masuk" {
		t.Fatalf("notifikasi %+v, mau 1 subjek 'masuk'", calls)
	}
}

// TestGateNotifyErrorLogged: error dari Notifier tidak boleh menggagalkan
// gate maupun menghentikan notifikasi pesan berikutnya.
func TestGateNotifyErrorLogged(t *testing.T) {
	fake := &fakeNotifier{err: errors.New("dbus mati")}
	gate := newSessionNotifier(fake)
	ctx := context.Background()
	folder := int64(1)
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id",
		[]mail.HeaderInfo{{UID: 1, MessageID: "<a@x>"}}) // backfill
	gate.waitSends()

	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	// Siklus berikutnya: notifikasi gagal, tapi pesan berikut tetap dicoba.
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id",
		[]mail.HeaderInfo{{UID: 1, MessageID: "<a@x>"}, {UID: 2, MessageID: "<b@x>"}})
	gate.observe(ctx, folder, "INBOX", "aku@contoh.id",
		[]mail.HeaderInfo{{UID: 3, MessageID: "<c@x>"}})
	gate.waitSends()
	calls := fake.snapshot()
	if len(calls) != 2 {
		t.Fatalf("notifikasi %d kali, mau 2 (<b@x> gagal dicatat, <c@x> dicoba): %+v",
			len(calls), calls)
	}
	if calls[0].MessageID != "<b@x>" || calls[1].MessageID != "<c@x>" {
		t.Fatalf("urutan pesan salah: %+v", calls)
	}
}

// TestGateConcurrentObserve: observe dari banyak goroutine sekaligus tetap
// mengirim paling satu notifikasi per pesan (diuji di bawah -race).
func TestGateConcurrentObserve(t *testing.T) {
	fake := &fakeNotifier{}
	gate := newSessionNotifier(fake)
	ctx := context.Background()
	folder := int64(1)
	headers := []mail.HeaderInfo{{UID: 1, MessageID: "<race@x>", Subject: "race"}}

	gate.observe(ctx, folder, "INBOX", "aku@contoh.id", headers) // backfill

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.observe(ctx, folder, "INBOX", "aku@contoh.id", headers)
		}()
	}
	wg.Wait()
	gate.waitSends()
	if calls := fake.snapshot(); len(calls) > 1 {
		t.Fatalf("pesan sama dinotifikasi %d kali", len(calls))
	}
}

// notifyStubIMAP mensimulasikan server IMAP: daftar header dikendalikan test
// antar siklus (mengabaikan lastUID agar gate dedupe yang menyaring).
type notifyStubIMAP struct {
	mu    sync.Mutex
	boxes []mail.MailboxInfo
	inbox []mail.HeaderInfo
}

func (s *notifyStubIMAP) ListMailboxes(context.Context) ([]mail.MailboxInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boxes, nil
}

func (s *notifyStubIMAP) SyncFolder(_ context.Context, mailbox string, _ uint32) ([]mail.HeaderInfo, uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []mail.HeaderInfo
	var max uint32
	if mailbox == "INBOX" {
		out = append(out, s.inbox...)
	}
	for _, h := range out {
		if h.UID > max {
			max = h.UID
		}
	}
	return out, max, nil
}

func (s *notifyStubIMAP) FetchRaw(context.Context, string, uint32) ([]byte, error) {
	return []byte("From: b@x.id\r\nSubject: x\r\n\r\nisi"), nil
}

func (s *notifyStubIMAP) Close() error { return nil }

// TestRefreshNotificationFlow: integration lewat Service.Refresh — backfill
// sync pertama hening, pesan baru berikutnya dinotifikasi persis sekali.
func TestRefreshNotificationFlow(t *testing.T) {
	svc := newTestService(t)
	fake := &fakeNotifier{}
	svc.SetNotifierForTesting(fake)

	if _, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	}); err != nil {
		t.Fatal(err)
	}
	stub := &notifyStubIMAP{
		boxes: []mail.MailboxInfo{{Name: "INBOX", Role: "inbox"},
			{Name: "Sent", Role: "sent"}},
		inbox: []mail.HeaderInfo{
			{UID: 3, MessageID: "<satu@x>", Sender: "Budi <b@x.id>",
				Subject: "halo", Date: time.Unix(0, 0).UTC(), Unread: true},
		},
	}
	old := imapDial
	imapDial = func(mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()
	ctx := context.Background()

	// Sync 1 (backfill sesi): dua pesan tersimpan, nol notifikasi.
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	svc.waitNotifierSends()
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("backfill dinotifikasi: %+v", calls)
	}

	// Sync 2: pesan lama dikirim ulang server — dedupe, tetap hening.
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	svc.waitNotifierSends()
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("pesan lama dinotifikasi ulang: %+v", calls)
	}

	// Sync 3: pesan baru tiba di INBOX — tepat satu notifikasi.
	stub.mu.Lock()
	stub.inbox = append(stub.inbox, mail.HeaderInfo{
		UID: 7, MessageID: "<baru@x>", Sender: "Cici <cici@x.id>",
		Subject: "undangan rapat", Date: time.Unix(60, 0).UTC(), Unread: true})
	stub.mu.Unlock()
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	svc.waitNotifierSends()
	calls := fake.snapshot()
	if len(calls) != 1 {
		t.Fatalf("notifikasi %d kali, mau 1: %+v", len(calls), calls)
	}
	want := NewMessage{AccountEmail: "aku@contoh.id", FolderName: "INBOX",
		MessageID: "<baru@x>", UID: 7, Sender: "Cici <cici@x.id>", Subject: "undangan rapat"}
	if calls[0] != want {
		t.Fatalf("notifikasi %+v, mau %+v", calls[0], want)
	}

	// Sync 4: pesan sama lagi — tidak ada notifikasi kedua.
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	svc.waitNotifierSends()
	if calls := fake.snapshot(); len(calls) != 1 {
		t.Fatalf("duplikat dinotifikasi: %+v", calls)
	}
}

// TestRefreshNotificationInboxOnly: pesan baru di folder selain inbox tidak
// dinotifikasi (pesan terkirim/pindahan bukan pesan diterima).
func TestRefreshNotificationInboxOnly(t *testing.T) {
	svc := newTestService(t)
	fake := &fakeNotifier{}
	svc.SetNotifierForTesting(fake)

	if _, err := svc.AddAccount(context.Background(), AccountConfig{
		Email: "aku@contoh.id", Password: "pw",
		IMAPHost: "imap.contoh.id", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.contoh.id", SMTPPort: 465, SMTPTLS: true,
	}); err != nil {
		t.Fatal(err)
	}
	stub := &notifyStubIMAP{
		boxes: []mail.MailboxInfo{{Name: "INBOX", Role: "inbox"},
			{Name: "Sent", Role: "sent"}},
	}
	old := imapDial
	imapDial = func(mail.IMAPConfig) (IMAPClient, error) { return stub, nil }
	defer func() { imapDial = old }()
	ctx := context.Background()

	// Backfill inbox + sent.
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	// Pesan baru hanya di Sent: tidak boleh ada notifikasi.
	stub.mu.Lock()
	stub.inbox = append(stub.inbox, mail.HeaderInfo{
		UID: 5, MessageID: "<terkirim@x>", Sender: "aku@contoh.id",
		Subject: "balasan", Date: time.Unix(0, 0).UTC()})
	stub.mu.Unlock()
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	svc.waitNotifierSends()
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("folder sent dinotifikasi: %+v", calls)
	}
}

// TestNotificationOptions memastikan bentuk notifikasi: app name "Karta",
// body = pengirim + subjek.
func TestNotificationOptions(t *testing.T) {
	opts := notificationOptions(NewMessage{Sender: "Budi <b@x.id>", Subject: "halo"})
	if opts.Title != "Karta" {
		t.Fatalf("Title %q, mau \"Karta\"", opts.Title)
	}
	if opts.Body != "Budi <b@x.id>: halo" {
		t.Fatalf("Body %q, mau \"Budi <b@x.id>: halo\"", opts.Body)
	}
	if opts.ID != "" || opts.Image != nil || opts.Sender != nil {
		t.Fatalf("opsi tidak semestinya terisi: %+v", opts)
	}
}
