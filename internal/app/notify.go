package app

import (
	"context"
	"fmt"
	"log"
	"runtime"
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

// logSendError mencatat kegagalan notifikasi. Di macOS ditambahi hint:
// notifikasi OS hanya tampil saat app ter-package sebagai .app.
func logSendError(err error) {
	if runtime.GOOS == "darwin" {
		err = fmt.Errorf("%w (macOS: notifikasi hanya tampil saat app ter-package sebagai .app)", err)
	}
	log.Printf("app: notifikasi pesan baru: %v", err)
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
	queue chan notifyBatch
	wg    sync.WaitGroup // batch yang masih di antrean/diproses
}

// notifyBatch adalah antrean pengiriman satu siklus observe. ctx disertakan
// agar notifikasi mengikuti konteks pemanggil (inner sudah detach cancel).
type notifyBatch struct {
	ctx  context.Context
	msgs []NewMessage
}

func newSessionNotifier(inner Notifier) *sessionNotifier {
	m := &sessionNotifier{
		inner: inner,
		seen:  map[string]bool{},
		armed: map[int64]bool{},
		queue: make(chan notifyBatch, 256),
	}
	// Satu goroutine konsumen seumur gate: pengiriman FIFO, sinkronisasi
	// tidak pernah menunggu pengiriman notifikasi. Gate hidup selama sesi
	// app, jadi goroutine ini sengaja tidak pernah dihentikan.
	go func() {
		for b := range m.queue {
			for _, msg := range b.msgs {
				if err := m.inner.NotifyNewMessage(b.ctx, msg); err != nil {
					logSendError(err)
				}
			}
			m.wg.Done()
		}
	}()
	return m
}

// waitSends menunggu seluruh antrean pengiriman tuntas; dipakai unit test
// agar asertif atas fake Notifier deterministik.
func (m *sessionNotifier) waitSends() { m.wg.Wait() }

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

	// Kirim asinkron: siklus sync tidak menunggu pengiriman notifikasi
	// (puluhan pesan baru sekaligus tidak lagi menunda siklus sync).
	// Dedupe sudah tuntas di atas, jadi antrean tidak bisa mengirim dobel.
	if len(pending) > 0 {
		m.wg.Add(1)
		m.queue <- notifyBatch{ctx: ctx, msgs: pending}
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

// waitNotifierSends menunggu antrean notifikasi tuntas (dipakai test yang
// mengganti Notifier via SetNotifierForTesting).
func (s *Service) waitNotifierSends() {
	if s.notify != nil {
		s.notify.waitSends()
	}
}
