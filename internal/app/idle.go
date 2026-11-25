package app

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"karta/internal/mail"
	"karta/internal/store"
)

// imapIdle membuka sesi IMAP IDLE; variabel agar unit test dapat menggantinya.
var imapIdle = mail.IdleMailbox

var (
	idleBaseDelay = 2 * time.Second
	idleMaxDelay  = 60 * time.Second
)

// Watch menjalankan IMAP IDLE untuk semua akun terdaftar (folder INBOX) dan
// memanggil onEvent setiap kali server mendorong perubahan baru. Memblokir
// sampai ctx dibatalkan dan seluruh goroutine akun selesai.
func (s *Service) Watch(ctx context.Context, onEvent func()) {
	accs, err := s.db.ListAccounts()
	if err != nil {
		log.Printf("app: idle daftar akun: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, acc := range accs {
		ac := acc
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.watchAccount(ctx, ac, onEvent)
		}()
	}
	wg.Wait()
}

func (s *Service) watchAccount(ctx context.Context, acc store.Account, onEvent func()) {
	delay := idleBaseDelay
	for {
		if ctx.Err() != nil {
			return
		}
		cfg, err := s.imapConfig(ctx, acc)
		if err != nil {
			log.Printf("app: idle %s kredensial: %v", acc.Email, err)
			if _, ok := IsOAuthRelogin(err); ok {
				return
			}
		} else {
			err = imapIdle(ctx, cfg, "INBOX", onEvent)
			if err == nil || ctx.Err() != nil {
				return
			}
			if _, ok := IsOAuthRelogin(err); ok {
				return
			}
			if errors.Is(err, mail.ErrIdleUnsupported) {
				log.Printf("app: %s tidak mendukung IDLE, fallback polling", acc.Email)
				return
			}
			log.Printf("app: idle %s terputus, ulang dalam %s: %v", acc.Email, delay, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, idleMaxDelay)
	}
}

func (s *Service) imapConfig(ctx context.Context, acc store.Account) (mail.IMAPConfig, error) {
	method := mail.AuthPassword
	var secret string
	var err error
	if acc.Auth == "oauth2" {
		method = mail.AuthOAuth2
		secret, err = s.oauthToken(ctx, acc.Email)
	} else {
		secret, err = s.getPassword(acc.Email)
	}
	if err != nil {
		return mail.IMAPConfig{}, err
	}
	return mail.IMAPConfig{
		Host: acc.IMAPHost, Port: acc.IMAPPort, TLS: acc.IMAPTLS,
		User: acc.Email, Pass: secret, Auth: method,
	}, nil
}
