package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// oauthScope adalah scope Gmail (IMAP + SMTP XOAUTH2).
const oauthScope = "https://mail.google.com/"

// oauthKeyringService memisahkan kredensial OAuth dari password.
const oauthKeyringService = "karta-oauth"

// oauthClientID membaca Client ID dari env KARTA_OAUTH_CLIENT_ID atau
// file ~/.config/karta/oauth.json {"client_id": "..."}.
func (s *Service) oauthClientID() (string, error) {
	if id := os.Getenv("KARTA_OAUTH_CLIENT_ID"); id != "" {
		return strings.TrimSpace(id), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "karta", "oauth.json"))
	if err != nil {
		return "", fmt.Errorf("client_id tidak ada (env KARTA_OAUTH_CLIENT_ID atau ~/config/karta/oauth.json): %w", err)
	}
	var cfg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("baca oauth.json: %w", err)
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return "", fmt.Errorf("oauth.json tanpa client_id")
	}
	return strings.TrimSpace(cfg.ClientID), nil
}

// OAuthStartURL membangun URL login Google dengan PKCE + offline access.
func (s *Service) OAuthStartURL(clientID, state, codeVerifier string, port int) string {
	conf := &oauth2.Config{
		ClientID:    clientID,
		Endpoint:    google.Endpoint,
		Scopes:      []string{oauthScope},
		RedirectURL: fmt.Sprintf("http://127.0.0.1:%d", port),
	}
	// PKCE S256 + offline access: refresh token diberikan saat consent.
	return conf.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", pkceS256(codeVerifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"))
}

// OAuthWaitCallback menjalankan server HTTP lokal port sampai Google
// mengembalikan code (satu kali). Blokir sampai code atau timeout 5 menit.
func (s *Service) OAuthWaitCallback(ctx context.Context, port int, state string) (code string, err error) {
	mux := http.NewServeMux()
	done := make(chan struct{})
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state tidak cocok", http.StatusBadRequest)
			return
		}
		code = r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><p>Login berhasil. Kembali ke Karta.</p><script>window.close()</script></body></html>")
		close(done)
	})
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer srv.Close()
	select {
	case <-done:
		if code == "" {
			return "", fmt.Errorf("callback tanpa code")
		}
		return code, nil
	case err := <-errCh:
		return "", fmt.Errorf("server callback: %w", err)
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("tunggu callback lebih dari 5 menit")
	}
}

// OAuthExchange menukar code dengan token (PKCE verifier) dan menyimpannya.
func (s *Service) OAuthExchange(ctx context.Context, clientID, email, code, verifier string) error {
	conf := &oauth2.Config{
		ClientID: clientID,
		Endpoint: google.Endpoint,
		Scopes:   []string{oauthScope},
	}
	tok, err := conf.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return fmt.Errorf("tukar code: %w", err)
	}
	return s.saveOAuthToken(email, tok)
}

// saveOAuthToken menyimpan token JSON ke keyring.
func (s *Service) saveOAuthToken(email string, tok *oauth2.Token) error {
	data, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return s.keyring.Set(oauthKeyringService, email, string(data))
}

// oauthToken mengembalikan access token valid (refresh otomatis bila perlu).
func (s *Service) oauthToken(ctx context.Context, email string) (string, error) {
	raw, err := s.keyring.Get(oauthKeyringService, email)
	if err != nil {
		return "", fmt.Errorf("token OAuth %s tidak ada: %w", email, err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return "", fmt.Errorf("baca token %s: %w", email, err)
	}
	clientID, err := s.oauthClientID()
	if err != nil {
		return "", err
	}
	conf := &oauth2.Config{ClientID: clientID, Endpoint: google.Endpoint}
	src := conf.TokenSource(ctx, &tok)
	fresh, err := src.Token()
	if err != nil {
		// invalid_grant dsb = permanen, user harus login ulang.
		return "", fmt.Errorf("perbarui token %s: %w (login ulang via OAuth)", email, err)
	}
	if fresh.AccessToken != tok.AccessToken {
		if err := s.saveOAuthToken(email, fresh); err != nil {
			log.Printf("app: simpan token baru %s: %v", email, err)
		}
	}
	return fresh.AccessToken, nil
}

// StartOAuthLogin menjalankan flow penuh di goroutine: buka browser,
// tunggu callback, tukar code, lapor hasil via onUpdate.
func (s *Service) StartOAuthLogin(ctx context.Context, email string, onDone func(err error)) error {
	clientID, err := s.oauthClientID()
	if err != nil {
		return err
	}
	state := randomToken(16)
	verifier := randomToken(48)
	port, err := freePort()
	if err != nil {
		return err
	}
	url := s.OAuthStartURL(clientID, state, verifier, port)
	go func() {
		openErr := openInSystemBrowser(url)
		if openErr != nil {
			log.Printf("app: buka browser: %v", openErr)
		}
		code, err := s.OAuthWaitCallback(ctx, port, state)
		if err == nil {
			err = s.OAuthExchange(ctx, clientID, email, code, verifier)
		}
		if onDone != nil {
			onDone(err)
		}
	}()
	return nil
}
