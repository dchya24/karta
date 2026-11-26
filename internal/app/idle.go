package app

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"karta/internal/mail"
	"karta/internal/store"
)

// imapIdle membuka sesi IMAP IDLE; variabel agar unit test dapat menggantinya.
var imapIdle = mail.IdleMailbox

var (
	idleBaseDelay   = 2 * time.Second
	idleMaxDelay    = 60 * time.Second
	idleRescanDelay = 5 * time.Second
)

// Watch menjalankan IMAP IDLE untuk setiap folder inti setiap akun (satu
// koneksi per folder) dan memanggil onEvent setiap kali server mendorong
// perubahan baru. Daftar akun/folder dipindai ulang setiap ronde, jadi akun
// atau folder yang muncul belakangan ikut terawasi. Memblokir sampai ctx
// dibatalkan.
func (s *Service) Watch(ctx context.Context, onEvent func()) {
	for {
		if ctx.Err() != nil {
			return
		}
		// Login ulang OAuth menghentikan IDLE (kredensial tidak bisa
		// diperbarui tanpa interaksi user); polling tetap berjalan.
		var relogin atomic.Bool
		s.watchRound(ctx, onEvent, &relogin)
		if relogin.Load() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(idleRescanDelay):
		}
	}
}

// watchRound mengawasi folder inti semua akun yang ada saat ini. Memblokir
// sampai semua watcher folder selesai.
func (s *Service) watchRound(ctx context.Context, onEvent func(), relogin *atomic.Bool) {
	accs, err := s.db.ListAccounts()
	if err != nil {
		log.Printf("app: idle daftar akun: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, acc := range accs {
		for _, box := range s.idleFolders(acc) {
			ac, mailbox := acc, box
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.watchFolder(ctx, ac, mailbox, onEvent, relogin)
			}()
		}
	}
	wg.Wait()
}

// idleFolders memilih nama folder inti akun (dari hasil sync terakhir).
func (s *Service) idleFolders(acc store.Account) []string {
	fs, err := s.db.ListFolders(int64(acc.ID))
	if err != nil {
		log.Printf("app: idle folder %s: %v", acc.Email, err)
		return nil
	}
	var out []string
	for _, f := range fs {
		if coreRoles[f.Role] {
			out = append(out, f.Name)
		}
	}
	return out
}

// watchFolder menjaga satu mailbox dengan reconnect backoff eksponensial
// sampai ctx dibatalkan. Server tanpa IDLE → folder diserahkan ke polling.
func (s *Service) watchFolder(ctx context.Context, acc store.Account, mailbox string, onEvent func(), relogin *atomic.Bool) {
	delay := idleBaseDelay
	for {
		if ctx.Err() != nil {
			return
		}
		cfg, err := s.imapConfig(ctx, acc)
		if err != nil {
			log.Printf("app: idle %s/%s kredensial: %v", acc.Email, mailbox, err)
			if _, ok := IsOAuthRelogin(err); ok {
				relogin.Store(true)
				return
			}
		} else {
			err = imapIdle(ctx, cfg, mailbox, onEvent)
			if err == nil || ctx.Err() != nil {
				return
			}
			if _, ok := IsOAuthRelogin(err); ok {
				relogin.Store(true)
				return
			}
			if errors.Is(err, mail.ErrIdleUnsupported) {
				log.Printf("app: %s/%s tidak mendukung IDLE, fallback polling", acc.Email, mailbox)
				return
			}
			log.Printf("app: idle %s/%s terputus, ulang dalam %s: %v", acc.Email, mailbox, delay, err)
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
