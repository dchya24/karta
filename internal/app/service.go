package app

import (
	"context"
	"fmt"
	"log"
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
}

// New membuat Service di atas store yang sudah terbuka.
func New(db *store.DB) *Service {
	return &Service{db: db, keyring: osKeyring{}}
}

// Close menutup resource service.
func (s *Service) Close() error { return s.db.Close() }

// AddAccount menyimpan akun baru: metadata ke SQLite, password ke keyring.
// Sinkronisasi awal berjalan di Refresh (dipanggil worker / setelah tambah).
func (s *Service) AddAccount(ctx context.Context, cfg AccountConfig) (int, error) {
	if cfg.Email == "" || cfg.IMAPHost == "" || cfg.SMTPHost == "" {
		return 0, fmt.Errorf("konfigurasi akun tidak lengkap")
	}
	id, err := s.db.AddAccount(cfg.Email, cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPTLS,
		cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLS)
	if err != nil {
		return 0, err
	}
	if err := s.keyring.Set(keyringService, cfg.Email, cfg.Password); err != nil {
		// Gagal keyring: jangan tinggalkan akun tanpa kredensial.
		_ = s.RemoveAccount(ctx, int(id))
		return 0, fmt.Errorf("simpan kredensial ke keyring: %w", err)
	}
	return int(id), nil
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
		}
	}
	if _, err := s.db.Exec(`DELETE FROM accounts WHERE id=?`, id); err != nil {
		return fmt.Errorf("hapus akun %d: %w", id, err)
	}
	return nil
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
		To: h.Recipients, PlainText: b.PlainText, RawHTML: b.RawHTML, Fetched: b.Fetched}
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
		To: h.Recipients, PlainText: b.PlainText, RawHTML: b.RawHTML, Fetched: b.Fetched}
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
	pass, err := s.keyring.Get(keyringService, acc.Email)
	if err != nil {
		return fmt.Errorf("baca kredensial %s: %w", acc.Email, err)
	}
	cl, err := imapDial(mail.IMAPConfig{Host: acc.IMAPHost, Port: acc.IMAPPort,
		TLS: acc.IMAPTLS, User: acc.Email, Pass: pass})
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
	for _, att := range parsed.Attachments {
		if err := s.db.InsertAttachment(id, att.Filename, att.ContentType, att.Data); err != nil {
			return err
		}
	}
	return nil
}

// MarkRead mengubah status baca pesan.
func (s *Service) MarkRead(id int, read bool) error {
	return s.db.MarkRead(int64(id), read)
}

// Refresh menyinkronkan semua akun: daftar folder, header baru folder inti.
// Gagal satu akun tidak menghentikan akun lain.
func (s *Service) Refresh(ctx context.Context) error {
	accs, err := s.db.ListAccounts()
	if err != nil {
		return err
	}
	for _, acc := range accs {
		ac := acc
		if err := s.refreshAccount(ctx, ac); err != nil {
			log.Printf("app: sync %s: %v", ac.Email, err)
		}
	}
	return nil
}

// coreRoles adalah folder yang disinkron otomatis (PRD §3.2).
var coreRoles = map[string]bool{
	"inbox": true, "sent": true, "drafts": true, "trash": true, "archive": true,
}

func (s *Service) refreshAccount(ctx context.Context, acc store.Account) error {
	ctx, cancel := context.WithTimeout(ctx, perAccountTimeout)
	defer cancel()
	pass, err := s.keyring.Get(keyringService, acc.Email)
	if err != nil {
		return fmt.Errorf("baca kredensial: %w", err)
	}
	cl, err := imapDial(mail.IMAPConfig{Host: acc.IMAPHost, Port: acc.IMAPPort,
		TLS: acc.IMAPTLS, User: acc.Email, Pass: pass})
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
			sh := store.MessageHeader{FolderID: int(folderID), UID: int(h.UID),
				MessageID: h.MessageID, Subject: h.Subject,
				Sender: h.Sender, SenderAddr: h.SenderAddr, Recipients: h.Recipients,
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
	}
	return nil
}
