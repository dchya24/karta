package app

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/emersion/go-imap/v2"

	"karta/internal/mail"
	"karta/internal/store"
)

// appender adalah IMAPClient yang mendukung APPEND (mail.Client).
type appender interface {
	Append(ctx context.Context, mailbox string, data []byte, flags ...imap.Flag) error
}

// parseAddrList memecah string "a@b, Nama <c@d>" menjadi daftar alamat.
func parseAddrList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// outgoingFromDraft membangun OutgoingMessage dari draft.
func outgoingFromDraft(from string, d Draft) mail.OutgoingMessage {
	return mail.OutgoingMessage{
		From:            from,
		To:              parseAddrList(d.To),
		Cc:              parseAddrList(d.Cc),
		Subject:         d.Subject,
		Body:            d.Body,
		AttachmentPaths: d.Attachments,
	}
}

// SendDraft mengirim pesan via SMTP lalu menaruh salinan ke folder Sent
// (best-effort: gagal append tidak menggagalkan pengiriman).
func (s *Service) SendDraft(ctx context.Context, accountID int, d Draft) error {
	acc, err := s.accountByID(accountID)
	if err != nil {
		return err
	}
	if d.To == "" {
		return fmt.Errorf("penerima (To) kosong")
	}
	out := outgoingFromDraft(acc.Email, d)
	secret, err := s.getPassword(acc.Email)
	if err != nil {
		return err
	}
	method := mail.AuthPassword
	if acc.Auth == "oauth2" {
		method = mail.AuthOAuth2
		secret, err = s.oauthToken(ctx, acc.Email)
		if err != nil {
			return err
		}
	}
	if err := mail.SendMail(mail.SMTPConfig{Host: acc.SMTPHost, Port: acc.SMTPPort,
		TLS: acc.SMTPTLS, User: acc.Email, Pass: secret, Auth: method}, out); err != nil {
		return err
	}

	// Salinan ke Sent, best-effort.
	cl, err := s.dialIMAP(ctx, acc)
	if err != nil {
		log.Printf("app: salinan Sent tidak dikirim: %v", err)
		return nil
	}
	defer cl.Close()
	box, err := mailboxByRole(ctx, cl, "sent")
	if err != nil {
		log.Printf("app: folder Sent tidak ditemukan: %v", err)
		return nil
	}
	ap, ok := cl.(appender)
	if !ok {
		log.Printf("app: klien IMAP tidak mendukung APPEND")
		return nil
	}
	copyBytes, err := mail.ComposeMessage(out)
	if err != nil {
		return fmt.Errorf("buat salinan Sent: %w", err)
	}
	// Gmail (dan layanan yang auto-copy via SMTP submission) tidak boleh
	// di-APPEND ke Sent: pesan akan dobel.
	if isGoogleAccount(acc) {
		log.Printf("app: %s via Google SMTP — salinan Sent dibiarkan ke server", acc.Email)
		return nil
	}
	if err := ap.Append(ctx, box, copyBytes); err != nil {
		log.Printf("app: append salinan Sent: %v", err)
	}
	return nil
}

// SaveDraft menyimpan draft ke folder Drafts IMAP akun.
func (s *Service) SaveDraft(ctx context.Context, accountID int, d Draft) error {
	acc, err := s.accountByID(accountID)
	if err != nil {
		return err
	}
	cl, err := s.dialIMAP(ctx, acc)
	if err != nil {
		return err
	}
	defer cl.Close()
	box, err := mailboxByRole(ctx, cl, "drafts")
	if err != nil {
		return fmt.Errorf("folder Drafts tidak ditemukan: %w", err)
	}
	ap, ok := cl.(appender)
	if !ok {
		return fmt.Errorf("klien IMAP tidak mendukung APPEND")
	}
	data, err := mail.ComposeMessage(outgoingFromDraft(acc.Email, d))
	if err != nil {
		return err
	}
	return ap.Append(ctx, box, data, imap.FlagDraft)
}

// isGoogleAccount true untuk Gmail/Google Workspace: SMTP submission Google
// otomatis menyimpan salinan Sent, jadi APPEND manual tidak boleh dilakukan.
func isGoogleAccount(acc store.Account) bool {
	d := strings.ToLower(acc.Email[strings.LastIndexByte(acc.Email, '@')+1:])
	h := strings.ToLower(acc.SMTPHost)
	return d == "gmail.com" || d == "googlemail.com" ||
		strings.Contains(h, "gmail-smtp") || strings.Contains(h, "aspmx") ||
		h == "smtp.gmail.com" || strings.HasSuffix(h, ".google.com")
}

// accountByID mengembalikan satu akun berdasarkan ID.
func (s *Service) accountByID(accountID int) (store.Account, error) {
	accs, err := s.db.ListAccounts()
	if err != nil {
		return store.Account{}, err
	}
	for _, a := range accs {
		if a.ID == accountID {
			return a, nil
		}
	}
	return store.Account{}, fmt.Errorf("akun %d tidak ditemukan", accountID)
}

// getPassword membaca password akun dari keyring.
func (s *Service) getPassword(email string) (string, error) {
	pw, err := s.keyring.Get(keyringService, email)
	if err != nil {
		return "", fmt.Errorf("baca kredensial %s: %w", email, err)
	}
	return pw, nil
}

// mailboxByRole mencari nama mailbox dengan peran tertentu.
func mailboxByRole(ctx context.Context, cl IMAPClient, role string) (string, error) {
	boxes, err := cl.ListMailboxes(ctx)
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		if b.Role == role {
			return b.Name, nil
		}
	}
	return "", fmt.Errorf("tidak ada folder dengan peran %q", role)
}
