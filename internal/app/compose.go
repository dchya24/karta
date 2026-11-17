package app

import (
	"context"
	"fmt"
	"log"
	"strings"

	"karta/internal/mail"
	"karta/internal/store"
)

// appender adalah IMAPClient yang mendukung APPEND (mail.Client).
type appender interface {
	Append(ctx context.Context, mailbox string, data []byte) error
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
	pass, err := s.getPassword(acc.Email)
	if err != nil {
		return err
	}
	if err := mail.SendMail(mail.SMTPConfig{Host: acc.SMTPHost, Port: acc.SMTPPort,
		TLS: acc.SMTPTLS, User: acc.Email, Pass: pass}, out); err != nil {
		return err
	}

	// Salinan ke Sent, best-effort.
	cl, err := imapDial(mail.IMAPConfig{Host: acc.IMAPHost, Port: acc.IMAPPort,
		TLS: acc.IMAPTLS, User: acc.Email, Pass: pass})
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
	pass, err := s.getPassword(acc.Email)
	if err != nil {
		return err
	}
	cl, err := imapDial(mail.IMAPConfig{Host: acc.IMAPHost, Port: acc.IMAPPort,
		TLS: acc.IMAPTLS, User: acc.Email, Pass: pass})
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
	return ap.Append(ctx, box, data)
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
