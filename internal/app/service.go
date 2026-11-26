package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/zalando/go-keyring"

	"karta/internal/mail"
	"karta/internal/store"
)

// keyringService adalah nama layanan di OS keyring.
const keyringService = "karta"

// credentialStore adalah penyimpanan kredensial (OS keyring). Dipisah agar
// Service dapat diuji tanpa keyring nyata.
type credentialStore interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

// osKeyring menghubungkan credentialStore ke paket go-keyring.
type osKeyring struct{}

func (osKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// IMAPClient adalah kemampuan koneksi IMAP yang dibutuhkan Service
// (dipenuhi mail.Client; direplikasi agar Service dapat diuji dengan stub).
type IMAPClient interface {
	ListMailboxes(ctx context.Context) ([]mail.MailboxInfo, error)
	SyncFolder(ctx context.Context, mailbox string, lastUID uint32) ([]mail.HeaderInfo, uint32, error)
	FetchRaw(ctx context.Context, mailbox string, uid uint32) ([]byte, error)
	Close() error
}

type flagSetter interface {
	SetSeen(ctx context.Context, mailbox string, uid uint32, seen bool) error
}

type messageMover interface {
	MoveMessage(ctx context.Context, srcMailbox string, uid uint32, destMailbox string) (uint32, error)
	DeleteMessage(ctx context.Context, mailbox string, uid uint32) error
}

// imapDial membuka koneksi IMAP; variabel agar test bisa menyetubnya.
var imapDial = func(cfg mail.IMAPConfig) (IMAPClient, error) {
	return mail.DialIMAP(cfg)
}

// perAccountTimeout membatasi satu siklus sinkronisasi satu akun.
const perAccountTimeout = 2 * time.Minute

// Service adalah facade utama aplikasi: satu-satunya jalan UI menyentuh
// data dan protokol. Method UI memanggil Service, bukan store/mail langsung.
type Service struct {
	db      *store.DB
	keyring credentialStore
	notify  *sessionNotifier
}

// New membuat Service di atas store yang sudah terbuka.
func New(db *store.DB) *Service {
	return &Service{db: db, keyring: osKeyring{}, notify: newSessionNotifier(mygoNotifier{})}
}

// SetKeyringForTesting mengganti penyimpanan kredensial (khusus test
// yang tidak punya OS keyring, mis. runner CI tanpa gnome-keyring).
func (s *Service) SetKeyringForTesting(cs credentialStore) {
	s.keyring = cs
}

// NewFakeKeyring membuat penyimpanan kredensial di memori untuk test
// (dipakai bersama paket ui via SetKeyringForTesting).
func NewFakeKeyring() credentialStore {
	return &fakeKeyring{secret: map[string]string{}}
}

// fakeKeyring mengimplementasikan credentialStore di memori.
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

// Close menutup resource service.
func (s *Service) Close() error { return s.db.Close() }

// AddAccount menyimpan akun baru: metadata ke SQLite, password ke keyring.
// Sinkronisasi awal berjalan di Refresh (dipanggil worker / setelah tambah).
// Upsert: bila email sudah terdaftar, konfigurasi & kredensial diperbarui.
func (s *Service) AddAccount(ctx context.Context, cfg AccountConfig) (int, error) {
	if cfg.Email == "" || cfg.IMAPHost == "" || cfg.SMTPHost == "" {
		return 0, fmt.Errorf("konfigurasi akun tidak lengkap")
	}
	auth := string(cfg.Auth)
	if auth == "" {
		auth = "password"
	}
	var (
		id  int64
		err error
	)
	accs, lerr := s.db.ListAccounts()
	if lerr != nil {
		return 0, lerr
	}
	existing := -1
	for _, a := range accs {
		if a.Email == cfg.Email {
			existing = a.ID
			break
		}
	}
	if existing >= 0 {
		err = s.db.UpdateAccount(cfg.Email, cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPTLS,
			cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLS, auth)
		id = int64(existing)
	} else {
		id, err = s.db.AddAccount(cfg.Email, cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPTLS,
			cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLS, auth)
	}
	if err != nil {
		return 0, err
	}
	if err := s.keyring.Set(keyringService, cfg.Email, cfg.Password); err != nil {
		// Gagal keyring: jangan tinggalkan akun tanpa kredensial.
		if existing < 0 {
			_ = s.RemoveAccount(ctx, int(id))
		}
		return 0, fmt.Errorf("simpan kredensial ke keyring: %w", err)
	}
	return int(id), nil
}

// EnsureGmailAccount menyimpan akun Gmail (OAuth2) dari hasil login;
// AddAccount upsert, jadi email yang sudah terdaftar cukup diperbarui.
func (s *Service) EnsureGmailAccount(email string) (int, error) {
	return s.AddAccount(context.Background(), AccountConfig{
		Email: email, IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: true,
		SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPTLS: true,
		Auth: AuthOAuth2,
	})
}

// RemoveAccount menghapus akun beserta kredensial keyring-nya.
func (s *Service) RemoveAccount(_ context.Context, id int) error {
	accs, err := s.db.ListAccounts()
	if err != nil {
		return err
	}
	for _, a := range accs {
		if a.ID == id {
			if err := s.keyring.Delete(keyringService, a.Email); err != nil &&
				err != keyring.ErrNotFound {
				return fmt.Errorf("hapus kredensial %s: %w", a.Email, err)
			}
			_ = s.keyring.Delete(oauthKeyringService, a.Email)
		}
	}
	return s.db.DeleteAccount(int64(id))
}

// ListAccounts mengembalikan akun terdaftar.
func (s *Service) ListAccounts() ([]Account, error) {
	rows, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, Account{ID: r.ID, Email: r.Email})
	}
	return out, nil
}

// ListFolders mengembalikan folder satu akun.
func (s *Service) ListFolders(accountID int) ([]Folder, error) {
	rows, err := s.db.ListFolders(int64(accountID))
	if err != nil {
		return nil, err
	}
	out := make([]Folder, 0, len(rows))
	for _, r := range rows {
		out = append(out, Folder{ID: r.ID, AccountID: r.AccountID,
			Name: r.Name, Role: r.Role, Unread: r.Unread})
	}
	return out, nil
}

// ListMessages mengembalikan daftar pesan folder, terbaru dulu. Query kosong
// berarti semua; query dijalankan lewat FTS5 lokal (instan).
func (s *Service) ListMessages(folderID int, query string) ([]MessageHeader, error) {
	rows, err := s.db.ListMessages(int64(folderID), query)
	if err != nil {
		return nil, err
	}
	out := make([]MessageHeader, 0, len(rows))
	for _, r := range rows {
		out = append(out, MessageHeader{ID: r.ID, FolderID: r.FolderID,
			UID: r.UID, Subject: r.Subject, Sender: r.Sender,
			SenderAddr: r.SenderAddr, Date: r.Date, Unread: r.Unread,
			HasAttachment: r.HasAttachment})
	}
	return out, nil
}

// PeekMessage mengembalikan pesan dari cache lokal tanpa jaringan (aman
// dipanggil di main thread UI). Mark-read lokal terjadi di sini.
func (s *Service) PeekMessage(id int) (*Message, error) {
	h, b, err := s.db.GetMessage(int64(id))
	if err != nil {
		return nil, err
	}
	m := &Message{MessageHeader: MessageHeader{ID: h.ID, FolderID: h.FolderID,
		UID: h.UID, Subject: h.Subject, Sender: h.Sender,
		SenderAddr: h.SenderAddr, Date: h.Date, Unread: h.Unread,
		HasAttachment: h.HasAttachment},
		To: h.Recipients, Cc: h.Cc, PlainText: b.PlainText, RawHTML: b.RawHTML, Fetched: b.Fetched}
	atts, err := s.db.ListAttachments(int64(id))
	if err == nil {
		for _, a := range atts {
			m.Attachments = append(m.Attachments, Attachment{
				Filename: a[0], ContentType: a[1]})
		}
	}
	if m.Unread {
		// Membuka pesan menandainya terbaca (lokal; IMAP flag menyusul di sync).
		_ = s.db.MarkRead(int64(id), true)
		m.Unread = false
	}
	return m, nil
}

// GetMessage mengembalikan pesan lengkap; body yang belum ter-cache di-fetch
// lazy dari IMAP lalu disimpan. Aman dipanggil dari goroutine (bukan main thread).
func (s *Service) GetMessage(ctx context.Context, id int) (*Message, error) {
	h, b, err := s.db.GetMessage(int64(id))
	if err != nil {
		return nil, err
	}
	if !b.Fetched {
		if err := s.fetchAndStoreBody(ctx, int64(id), int64(h.FolderID), h.UID); err != nil {
			return nil, err
		}
		if _, b, err = s.db.GetMessage(int64(id)); err != nil {
			return nil, err
		}
	}
	m := &Message{MessageHeader: MessageHeader{ID: h.ID, FolderID: h.FolderID,
		UID: h.UID, Subject: h.Subject, Sender: h.Sender,
		SenderAddr: h.SenderAddr, Date: h.Date, Unread: h.Unread,
		HasAttachment: h.HasAttachment},
		To: h.Recipients, Cc: h.Cc, PlainText: b.PlainText, RawHTML: b.RawHTML, Fetched: b.Fetched}
	atts, err := s.db.ListAttachments(int64(id))
	if err == nil {
		for _, a := range atts {
			m.Attachments = append(m.Attachments, Attachment{
				Filename: a[0], ContentType: a[1]})
		}
	}
	if m.Unread {
		_ = s.db.MarkRead(int64(id), true)
		m.Unread = false
	}
	return m, nil
}

// fetchAndStoreBody mengambil raw pesan dari IMAP, mem-parse, menyimpan isi.
func (s *Service) fetchAndStoreBody(ctx context.Context, id, folderID int64, uid int) error {
	folder, err := s.db.FolderByID(folderID)
	if err != nil {
		return err
	}
	accs, err := s.db.ListAccounts()
	if err != nil {
		return err
	}
	var acc *store.Account
	for i := range accs {
		if accs[i].ID == folder.AccountID {
			acc = &accs[i]
			break
		}
	}
	if acc == nil {
		return fmt.Errorf("akun folder %d tidak ditemukan", folderID)
	}
	cl, err := s.dialIMAP(ctx, *acc)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.FetchRaw(ctx, folder.Name, uint32(uid))
	if err != nil {
		return err
	}
	parsed, err := mail.ParseMessage(raw)
	if err != nil {
		return err
	}
	body := &store.MessageBody{PlainText: parsed.PlainText, RawHTML: parsed.RawHTML}
	if err := s.db.SetMessageBody(id, body); err != nil {
		return err
	}
	if len(parsed.Attachments) > 0 {
		// Gmail dkk. tidak menawarkan flag lampiran: deteksi dari hasil parse.
		_ = s.db.SetHasAttachment(id, true)
	}
	for _, att := range parsed.Attachments {
		if err := s.db.InsertAttachment(id, att.Filename, att.ContentType, att.Data); err != nil {
			return err
		}
	}
	return nil
}

// MarkRead mengubah status baca pesan secara lokal lalu menyinkronkan flag
// \Seen ke server IMAP (best-effort: gagal sync menjadi SyncWarning).
func (s *Service) MarkRead(ctx context.Context, id int, read bool) error {
	if err := s.db.MarkRead(int64(id), read); err != nil {
		return err
	}
	h, _, err := s.db.GetMessage(int64(id))
	if err != nil || h.UID <= 0 {
		return nil
	}
	folder, acc, err := s.messageLocation(int64(h.FolderID))
	if err != nil {
		return nil
	}
	cl, err := s.dialIMAP(ctx, acc)
	if err != nil {
		return &SyncWarning{Msg: fmt.Sprintf("flag dibaca lokal, sync \\Seen gagal: %v", err)}
	}
	defer cl.Close()
	if fs, ok := cl.(flagSetter); ok {
		if err := fs.SetSeen(ctx, folder.Name, uint32(h.UID), read); err != nil {
			return &SyncWarning{Msg: fmt.Sprintf("flag dibaca lokal, set \\Seen gagal: %v", err)}
		}
	}
	return nil
}

// Archive memindahkan pesan ke folder Archive akun (lokal + best-effort IMAP).
func (s *Service) Archive(ctx context.Context, id int) error {
	return s.moveToRole(ctx, id, "archive")
}

// Delete memindahkan pesan ke folder Trash; bila sudah di Trash, pesan dihapus
// permanen beserta baris FTS-nya (lokal + best-effort IMAP).
func (s *Service) Delete(ctx context.Context, id int) error {
	h, _, err := s.db.GetMessage(int64(id))
	if err != nil {
		return err
	}
	folder, acc, err := s.messageLocation(int64(h.FolderID))
	if err != nil {
		return err
	}
	if folder.Role != "trash" {
		return s.moveToRole(ctx, id, "trash")
	}
	if err := s.db.DeleteMessage(int64(id)); err != nil {
		return err
	}
	if h.UID <= 0 {
		return nil
	}
	cl, err := s.dialIMAP(ctx, acc)
	if err != nil {
		return &SyncWarning{Msg: fmt.Sprintf("dihapus lokal, hapus di server gagal: %v", err)}
	}
	defer cl.Close()
	if mv, ok := cl.(messageMover); ok {
		if err := mv.DeleteMessage(ctx, folder.Name, uint32(h.UID)); err != nil {
			return &SyncWarning{Msg: fmt.Sprintf("dihapus lokal, expunge server gagal: %v", err)}
		}
	}
	return nil
}

func (s *Service) moveToRole(ctx context.Context, id int, role string) error {
	verb := "Dipindah"
	if role == "archive" {
		verb = "Diarsipkan"
	} else if role == "trash" {
		verb = "Dipindah ke Trash"
	}
	h, _, err := s.db.GetMessage(int64(id))
	if err != nil {
		return err
	}
	src, acc, err := s.messageLocation(int64(h.FolderID))
	if err != nil {
		return err
	}
	if src.Role == role {
		return nil
	}
	dst, err := s.db.FolderByRole(int64(acc.ID), role)
	if err != nil {
		return fmt.Errorf("folder %s tidak ditemukan: %w", role, err)
	}
	if err := s.db.MoveMessage(int64(id), int64(dst.ID), 0); err != nil {
		return err
	}
	if h.UID <= 0 {
		return nil
	}
	cl, err := s.dialIMAP(ctx, acc)
	if err != nil {
		return &SyncWarning{Msg: fmt.Sprintf("%s lokal, sync ke server gagal: %v", verb, err)}
	}
	defer cl.Close()
	if mv, ok := cl.(messageMover); ok {
		newUID, err := mv.MoveMessage(ctx, src.Name, uint32(h.UID), dst.Name)
		if err != nil {
			return &SyncWarning{Msg: fmt.Sprintf("%s lokal, MOVE server gagal: %v", verb, err)}
		}
		if newUID > 0 {
			_ = s.db.SetMessageUID(int64(id), newUID)
		}
	}
	return nil
}

func (s *Service) messageLocation(folderID int64) (*store.Folder, store.Account, error) {
	folder, err := s.db.FolderByID(folderID)
	if err != nil {
		return nil, store.Account{}, err
	}
	acc, err := s.accountByID(folder.AccountID)
	if err != nil {
		return nil, store.Account{}, err
	}
	return folder, acc, nil
}

// MultiError menggabungkan kegagalan beberapa akun dalam satu Refresh;
// errors.As tetap menemukan OAuthReloginError di dalamnya.
type MultiError struct{ Errs []error }

func (e *MultiError) Error() string {
	msgs := make([]string, len(e.Errs))
	for i, err := range e.Errs {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

func (e *MultiError) Unwrap() []error { return e.Errs }

// SyncWarning melaporkan kegagalan langkah best-effort ke server IMAP
// setelah aksi lokal berhasil — UI menampilkannya sebagai peringatan,
// bukan kegagalan aksi.
type SyncWarning struct{ Msg string }

func (w *SyncWarning) Error() string { return w.Msg }

// IsSyncWarning true bila err hanya peringatan sync best-effort.
func IsSyncWarning(err error) bool {
	var w *SyncWarning
	return errors.As(err, &w)
}

// Refresh menyinkronkan semua akun: daftar folder, header baru folder inti.
// Gagal satu akun tidak menghentikan akun lain; semua kegagalan dikembalikan
// sebagai MultiError (termasuk OAuthReloginError yang dapat ditemukan
// errors.As) agar UI dapat menampilkannya, bukan hanya mencatat log.
func (s *Service) Refresh(ctx context.Context) error {
	accs, err := s.db.ListAccounts()
	if err != nil {
		return err
	}
	var errs []error
	for _, acc := range accs {
		ac := acc
		if err := s.refreshAccount(ctx, ac); err != nil {
			errs = append(errs, fmt.Errorf("sync %s: %w", ac.Email, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &MultiError{Errs: errs}
}

// coreRoles adalah folder yang disinkron otomatis (PRD §3.2).
var coreRoles = map[string]bool{
	"inbox": true, "sent": true, "drafts": true, "trash": true, "archive": true,
}

// Retry: percobaan ulang per akun untuk error transient (dial/TLS/putus
// koneksi), backoff eksponensial. Var agar test bisa mempercepat.
var (
	retryAttempts  = 3 // total percobaan termasuk yang pertama
	retryBaseDelay = 1 * time.Second
)

// refreshAccount menjalankan sync satu akun dengan retry transient.
func (s *Service) refreshAccount(ctx context.Context, acc store.Account) error {
	ctx, cancel := context.WithTimeout(ctx, perAccountTimeout)
	defer cancel()
	delay := retryBaseDelay
	for attempt := 1; ; attempt++ {
		err := s.tryRefreshAccount(ctx, acc)
		if err == nil {
			return nil
		}
		if !mail.IsTransient(err) || attempt == retryAttempts {
			return err
		}
		log.Printf("app: sync %s gagal (percobaan %d/%d), ulang dalam %s: %v",
			acc.Email, attempt, retryAttempts, delay, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 4
	}
}

// tryRefreshAccount satu percobaan sync penuh: dial, daftar folder, fetch
// header baru folder inti.
func (s *Service) tryRefreshAccount(ctx context.Context, acc store.Account) error {
	cl, err := s.dialIMAP(ctx, acc)
	if err != nil {
		return err
	}
	defer cl.Close()

	boxes, err := cl.ListMailboxes(ctx)
	if err != nil {
		return err
	}
	folderIDs := map[string]int64{} // role → folder ID
	for _, b := range boxes {
		id, err := s.db.EnsureFolder(int64(acc.ID), b.Name, b.Role)
		if err != nil {
			return err
		}
		if coreRoles[b.Role] {
			folderIDs[b.Role] = id
		}
	}
	for role, folderID := range folderIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		folder, err := s.db.FolderByID(folderID)
		if err != nil {
			return err
		}
		headers, maxUID, err := cl.SyncFolder(ctx, folder.Name, folder.LastSeenUID)
		if err != nil {
			return fmt.Errorf("sync %s: %w", role, err)
		}
		for _, h := range headers {
			recips := h.To
			if recips == "" {
				recips = h.Recipients
			}
			sh := store.MessageHeader{FolderID: int(folderID), UID: int(h.UID),
				MessageID: h.MessageID, Subject: h.Subject,
				Sender: h.Sender, SenderAddr: h.SenderAddr, Recipients: recips, Cc: h.Cc,
				Date: h.Date.UTC().Format(time.RFC3339), Unread: h.Unread,
				HasAttachment: h.HasAttachment, Size: int(h.Size)}
			if _, err := s.db.UpsertMessage(&sh, nil); err != nil {
				return err
			}
		}
		if maxUID > 0 {
			if err := s.db.UpdateFolderSync(folderID, maxUID); err != nil {
				return err
			}
		}
		if role == "inbox" {
			s.notifyNewMessages(ctx, folderID, folder.Name, acc.Email, headers)
		}
	}
	return nil
}
