package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// OAuthReloginError menandakan refresh token OAuth sudah kedaluwarsa atau
// dicabut (mis. invalid_grant setelah >7 hari pada mode Testing Google)
// sehingga pengguna harus login ulang via browser.
type OAuthReloginError struct {
	Email string
	Err   error
}

func (e *OAuthReloginError) Error() string {
	return fmt.Sprintf("sesi Google %s kedaluwarsa — silakan login ulang", e.Email)
}

func (e *OAuthReloginError) Unwrap() error { return e.Err }

// IsOAuthRelogin memeriksa apakah err menuntut login ulang OAuth dan
// mengembalikan alamat email akun terkait.
func IsOAuthRelogin(err error) (string, bool) {
	var re *OAuthReloginError
	if errors.As(err, &re) {
		return re.Email, true
	}
	return "", false
}

const mailScope = "https://mail.google.com/"

// hasGmailScope memeriksa apakah string scope granted memuat scope Gmail.
func hasGmailScope(scope string) bool {
	return slices.Contains(strings.Fields(scope), mailScope)
}

// oauthReloginScope menandakan token berjalan tanpa izin Gmail sehingga
// Gmail menolak XOAUTH2 meski refresh masih sukses — butuh re-consent.
func oauthReloginScope(email, granted string) error {
	return &OAuthReloginError{Email: email,
		Err: fmt.Errorf("token %s tanpa izin Gmail (scope: %s) — login ulang dan setujui izin Gmail", email, granted)}
}

func isOAuthTokenRevoked(err error) bool {
	if err == nil {
		return false
	}
	var retErr *oauth2.RetrieveError
	if errors.As(err, &retErr) {
		switch retErr.ErrorCode {
		case "invalid_grant", "unauthorized_client", "invalid_client":
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "invalid_grant") || strings.Contains(s, "Token has been expired or revoked")
}

// oauthScope adalah scope Gmail (IMAP + SMTP XOAUTH2); openid+email
// menyertakan id_token agar email akun terdeteksi otomatis dari login.
var oauthScopes = []string{"https://mail.google.com/", "openid", "email"}

// oauthKeyringService memisahkan kredensial OAuth dari password.
const oauthKeyringService = "karta-oauth"

// oauthConfig membaca Client ID dan Client Secret dari env
// (KARTA_OAUTH_CLIENT_ID, KARTA_OAUTH_CLIENT_SECRET) atau file
// ~/.config/karta/oauth.json {"client_id": "...", "client_secret": "..."}.
// Secret opsional: client Desktop lama tanpa secret tetap jalan.
func (s *Service) oauthConfig() (id, secret string, err error) {
	id = strings.TrimSpace(os.Getenv("KARTA_OAUTH_CLIENT_ID"))
	secret = strings.TrimSpace(os.Getenv("KARTA_OAUTH_CLIENT_SECRET"))
	if id != "" {
		return id, secret, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "karta", "oauth.json"))
	if err != nil {
		return "", "", fmt.Errorf("client_id tidak ada (env KARTA_OAUTH_CLIENT_ID atau ~/.config/karta/oauth.json): %w", err)
	}
	var cfg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", "", fmt.Errorf("baca oauth.json: %w", err)
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return "", "", fmt.Errorf("oauth.json tanpa client_id")
	}
	return strings.TrimSpace(cfg.ClientID), strings.TrimSpace(cfg.ClientSecret), nil
}

// OAuthStartURL membangun URL login Google dengan PKCE + offline access.
func (s *Service) OAuthStartURL(clientID, state, codeVerifier string, port int) string {
	conf := &oauth2.Config{
		ClientID:    clientID,
		Endpoint:    google.Endpoint,
		Scopes:      oauthScopes,
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
// mengembalikan code. Halaman sukses tetap tersaji untuk redirect ulang
// (grace 2 menit setelah code diterima) — redirect pertama tidak gagal.
func (s *Service) OAuthWaitCallback(ctx context.Context, port int, state string) (code string, err error) {
	mux := http.NewServeMux()
	done := make(chan struct{})
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	success := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><p>Login berhasil. Kembali ke Karta.</p><script>window.close()</script></body></html>")
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state tidak cocok", http.StatusBadRequest)
			return
		}
		if c := r.URL.Query().Get("code"); c != "" && code == "" {
			code = c
			close(done)
		}
		success(w) // idempotent: redirect ulang tetap dapat halaman sukses
	})
	errCh := make(chan error, 1)
	l, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return "", fmt.Errorf("bind %s: %w", srv.Addr, err)
	}
	log.Printf("oauth: server callback mendengarkan di http://%s", l.Addr())
	go func() {
		if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
			log.Printf("oauth: server callback berhenti: %v", err)
			errCh <- err
		}
	}()
	select {
	case <-done:
		// Server tetap hidup sebentar untuk redirect ulang / refresh browser.
		go func() { time.Sleep(2 * time.Minute); srv.Close() }()
		if code == "" {
			return "", fmt.Errorf("callback tanpa code")
		}
		return code, nil
	case err := <-errCh:
		srv.Close()
		return "", fmt.Errorf("server callback: %w", err)
	case <-ctx.Done():
		srv.Close()
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		srv.Close()
		return "", fmt.Errorf("tunggu callback lebih dari 5 menit")
	}
}

// OAuthExchange menukar code dengan token (PKCE verifier), membaca email
// akun dari id_token, dan menyimpan token ke keyring di bawah email itu.
// redirectURI wajib sama dengan yang dipakai di authorize (Google menolak
// exchange tanpa parameter redirect_uri).
func (s *Service) OAuthExchange(ctx context.Context, clientID, secret, redirectURI, code, verifier string) (string, error) {
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: secret,
		Endpoint:     google.Endpoint,
		Scopes:       oauthScopes,
		RedirectURL:  redirectURI,
	}
	tok, err := conf.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return "", fmt.Errorf("tukar code: %w", err)
	}
	// Tolak simpan token tanpa izin Gmail: IMAP/SMTP XOAUTH2 pasti ditolak.
	if granted, _ := tok.Extra("scope").(string); granted != "" && !hasGmailScope(granted) {
		return "", oauthReloginScope("", granted)
	}
	email, err := emailFromIDToken(tok)
	if err != nil {
		return "", err
	}
	if err := s.saveOAuthToken(email, tok); err != nil {
		return "", fmt.Errorf("simpan token: %w", err)
	}
	return email, nil
}

// emailFromIDToken mengekstrak klaim email dari id_token (JWT).
func emailFromIDToken(tok *oauth2.Token) (string, error) {
	raw, _ := tok.Extra("id_token").(string)
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("id_token tidak valid")
	}
	payload := strings.TrimRight(parts[1], "=")
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("dekod id_token: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return "", fmt.Errorf("baca klaim id_token: %w", err)
	}
	if claims.Email == "" {
		return "", fmt.Errorf("id_token tanpa klaim email")
	}
	return claims.Email, nil
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
		if errors.Is(err, keyring.ErrNotFound) {
			return "", &OAuthReloginError{Email: email, Err: err}
		}
		return "", fmt.Errorf("token OAuth %s tidak ada: %w", email, err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return "", &OAuthReloginError{Email: email, Err: err}
	}
	clientID, secret, err := s.oauthConfig()
	if err != nil {
		return "", err
	}
	conf := &oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: google.Endpoint}
	src := conf.TokenSource(ctx, &tok)
	fresh, err := src.Token()
	if err != nil {
		if isOAuthTokenRevoked(err) {
			return "", &OAuthReloginError{Email: email, Err: err}
		}
		return "", fmt.Errorf("perbarui token %s: %w", email, err)
	}
	// Google bisa memberi token tanpa izin Gmail (scope ditolak/dihapus
	// pengguna saat consent) — refresh sukses tapi Gmail menolak XOAUTH2.
	// Deteksi di sini supaya UI menawarkan login ulang, bukan retry tanpa henti.
	if granted, _ := fresh.Extra("scope").(string); granted != "" && !hasGmailScope(granted) {
		return "", oauthReloginScope(email, granted)
	}
	if fresh.AccessToken != tok.AccessToken {
		if err := s.saveOAuthToken(email, fresh); err != nil {
			log.Printf("app: simpan token baru %s: %v", email, err)
		}
	}
	return fresh.AccessToken, nil
}

// StartOAuthLogin menjalankan flow penuh di goroutine: buka browser,
// tunggu callback, tukar code, lapor email akun + hasil via onDone.
func (s *Service) StartOAuthLogin(ctx context.Context, onDone func(email string, err error)) error {
	clientID, secret, err := s.oauthConfig()
	if err != nil {
		return err
	}
	state := randomToken(16)
	verifier := randomToken(48)
	port, err := fixedOrFreePort(oauthPort)
	if err != nil {
		return err
	}
	url := s.OAuthStartURL(clientID, state, verifier, port)
	go func() {
		openErr := openInSystemBrowser(url)
		if openErr != nil {
			log.Printf("app: buka browser: %v", openErr)
		}
		var email string
		code, err := s.OAuthWaitCallback(ctx, port, state)
		if err == nil {
			redirect := fmt.Sprintf("http://127.0.0.1:%d", port)
			email, err = s.OAuthExchange(ctx, clientID, secret, redirect, code, verifier)
		}
		if onDone != nil {
			onDone(email, err)
		}
	}()
	return nil
}
