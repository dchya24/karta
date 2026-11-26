package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"karta/internal/mail"
)

// Notifier mengirim satu notifikasi desktop untuk pesan baru. Dipisah agar
// unit test bisa menyuntik fake (tanpa memanggil OS asli).
type Notifier interface {
	NotifyNewMessage(ctx context.Context, n NewMessage) error
}

// NewMessage adalah data pesan baru yang akan dinotifikasikan
// (JSON-friendly, konsisten dengan DTO lain di paket app).
type NewMessage struct {
	AccountEmail string `json:"accountEmail"`
	FolderName   string `json:"folderName"`
	MessageID    string `json:"messageId"`
	UID          int    `json:"uid"`
	Sender       string `json:"sender"`
	Subject      string `json:"subject"`
}

// dedupeKey kunci dedupe sesi: Message-ID bila ada, selain itu
// akun/folder/UID (pesan tanpa Message-ID, mis. draft lokal).
func (n NewMessage) dedupeKey() string {
	if n.MessageID != "" {
		return "id:" + n.MessageID
	}
	return fmt.Sprintf("uid:%s/%s/%d", n.AccountEmail, n.FolderName, n.UID)
}

// notifyTimeout membatasi tunggu pengiriman satu notifikasi agar goroutine
// sync tidak menunggu tanpa batas bila main loop GUI tidak memproses.
const notifyTimeout = 10 * time.Second

// mygoNotifier mengirim notifikasi lewat API native mygo (Linux/macOS/
// Windows). ShowContext otomatis mendispatch ke main thread GUI, jadi aman
// dipanggil dari goroutine sinkronisasi; tidak pernah menyentuh widget.
type mygoNotifier struct{}

func (mygoNotifier) NotifyNewMessage(ctx context.Context, n NewMessage) error {
	// Cancellation sinkronisasi tidak boleh membatalkan notifikasi yang
	// sedang dikirim; Timeout memastikan goroutine tidak menggantung.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	if err := mygo.NewNotification(notificationOptions(n)).ShowContext(ctx); err != nil {
		return fmt.Errorf("kirim notifikasi desktop: %w", err)
	}
	return nil
}

// notificationOptions menyusun opsi notifikasi: nama app "Karta",
// body = pengirim + subjek, tanpa aksi klik.
func notificationOptions(n NewMessage) mygo.NotificationOptions {
	return mygo.NotificationOptions{
		Title: "Karta",
		Body:  fmt.Sprintf("%s: %s", n.Sender, n.Subject),
	}
}

// sessionNotifier memutuskan kapan notifikasi dikirim:
//   - Sinkronisasi awal tiap folder dalam satu sesi (backfill) tidak
//     dinotifikasikan: folder baru dianggap "terisi" saat sync pertamanya,
//     lalu melucuti pesan berikutnya. Akun/folder yang muncul di tengah
//     sesi otomatis ikut aturan ini.
//   - Pesan yang sama tidak dinotifikasikan dua kali per sesi (set
//     Message-ID, atau akun/folder/UID bila Message-ID kosong).
//
// Aman dipanggil dari beberapa goroutine sekaligus.
type sessionNotifier struct {
	inner Notifier
	mu    sync.Mutex
	seen  map[string]bool
	armed map[int64]bool // folderID → sync awal sesi sudah lewat
}

func newSessionNotifier(inner Notifier) *sessionNotifier {
	return &sessionNotifier{
		inner: inner,
		seen:  map[string]bool{},
		armed: map[int64]bool{},
	}
}

// observe memproses header baru satu folder hasil sync. folderID mengenali
// folder dalam sesi; pemanggilan pertama per folder dianggap backfill.
// Error dari inner hanya dicatat: kegagalan notifikasi tidak boleh
// menggagalkan sinkronisasi.
func (m *sessionNotifier) observe(ctx context.Context, folderID int64, folderName, accountEmail string, headers []mail.HeaderInfo) {
	m.mu.Lock()
	backfill := !m.armed[folderID]
	var pending []NewMessage
	for _, h := range headers {
		msg := NewMessage{AccountEmail: accountEmail, FolderName: folderName,
			MessageID: h.MessageID, UID: int(h.UID), Sender: h.Sender, Subject: h.Subject}
		key := msg.dedupeKey()
		if m.seen[key] {
			continue
		}
		m.seen[key] = true
		if !backfill {
			pending = append(pending, msg)
		}
	}
	m.armed[folderID] = true
	m.mu.Unlock()

	for _, msg := range pending {
		if err := m.inner.NotifyNewMessage(ctx, msg); err != nil {
			log.Printf("app: notifikasi pesan baru: %v", err)
		}
	}
}

// SetNotifierForTesting mengganti pengirim notifikasi (khusus test yang
// tidak boleh memanggil OS asli); state gate direset bersamaannya.
func (s *Service) SetNotifierForTesting(n Notifier) {
	s.notify = newSessionNotifier(n)
}

// notifyNewMessages meneruskan header baru folder inbox ke gate notifikasi.
// Hanya inbox: pesan yang muncul di sent/drafts/trash/archive bukan pesan
// yang "diterima" (mis. UID pesan pindahan bisa terlihat seperti pesan baru).
func (s *Service) notifyNewMessages(ctx context.Context, folderID int64, folderName, accountEmail string, headers []mail.HeaderInfo) {
	if s.notify == nil || len(headers) == 0 {
		return
	}
	s.notify.observe(ctx, folderID, folderName, accountEmail, headers)
}
