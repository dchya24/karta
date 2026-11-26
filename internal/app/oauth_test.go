package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"golang.org/x/oauth2"
	"karta/internal/mail"
	"karta/internal/store"
)

// fakeGmailSMTP menerima AUTH XOAUTH2 dan memverifikasi tokennya.
type fakeGmailSMTP struct {
	listener net.Listener
	authSeen string
	mailOK   bool
}

func startFakeGmailSMTP(t *testing.T, wantToken string) *fakeGmailSMTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGmailSMTP{listener: l}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer func() { f.mailOK = true }()
		w := []func(string){func(s string) {}}
		_ = w
		buf := make([]byte, 4096)
		write := func(s string) {
			conn.Write([]byte(s + "\r\n"))
		}
		write("220 fake gmail")
		authorized := false
		for {
			_ = authorized // diagnostik
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			line := strings.TrimSpace(string(buf[:n]))
			up := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				write("250-fake")
				write("250 AUTH XOAUTH2 PLAIN")
			case strings.HasPrefix(up, "AUTH XOAUTH"), strings.HasPrefix(up, "AUTH PLAIN"):
				// initial response base64 setelah mekanisme
				parts := strings.SplitN(line, " ", 3)
				if len(parts) < 3 {
					write("334 ") // minta lanjutan
					continue
				}
				dec, derr := base64Decode(parts[2])
				if derr != nil || !strings.Contains(dec, "Bearer "+wantToken) {
					write("334 eyJzdGF0dXMiOiI0MDAifQ==") // error JSON base64
					continue
				}
				f.authSeen = dec
				authorized = true
				write("235 2.7.0 Accepted")
			case strings.HasPrefix(up, "MAIL FROM") && authorized:
				write("250 ok")
			case strings.HasPrefix(up, "RCPT TO") && authorized:
				write("250 ok")
			case strings.HasPrefix(up, "DATA") && authorized:
				write("354 go")
				// baca sampai CRLF.CRLF sederhana
				for {
					m, rerr := conn.Read(buf)
					if rerr != nil {
						return
					}
					if strings.HasSuffix(strings.TrimSpace(string(buf[:m])), ".") {
						break
					}
				}
				write("250 queued")
			case strings.HasPrefix(up, "QUIT"):
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()
	return f
}

func base64Decode(s string) (string, error) {
	dec, err := base64.StdEncoding.DecodeString(s)
	return string(dec), err
}

// TestXoauth2SMTPFlow menguji jalur SendMail XOAUTH2 ke server fake Gmail.
func TestXoauth2SMTPFlow(t *testing.T) {
	f := startFakeGmailSMTP(t, "tok123")
	port := f.listener.Addr().(*net.TCPAddr).Port
	err := mail.SendMail(mail.SMTPConfig{
		Host: "127.0.0.1", Port: port, TLS: false,
		User: "user@gmail.com", Pass: "tok123", Auth: mail.AuthOAuth2,
	}, mail.OutgoingMessage{
		From: "user@gmail.com", To: []string{"teman@contoh.id"},
		Subject: "oauth kirim", Body: "isi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.authSeen, "Bearer tok123") ||
		!strings.Contains(f.authSeen, "user=user@gmail.com") {
		t.Fatalf("xoauth2 tidak sesuai: %q", f.authSeen)
	}
}

// TestSASLAuthSelection memastikan pilihan SASL benar.
func TestSASLAuthSelection(t *testing.T) {
	c, err := mail.SASLAuth(mail.AuthOAuth2, "a@gmail.com", "tok")
	if err != nil {
		t.Fatal(err)
	}
	mech, _, err := c.Start()
	if err != nil || mech != "XOAUTH2" {
		t.Fatalf("mech=%q err=%v", mech, err)
	}
	if _, ok := c.(sasl.Client); !ok {
		t.Fatal("bukan sasl.Client")
	}
}

// TestEmailFromIDToken memverifikasi ekstraksi klaim email dari JWT.
func TestEmailFromIDToken(t *testing.T) {
	makeTok := func(claims string) *oauth2.Token {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
		tok := &oauth2.Token{AccessToken: "x"}
		tok = tok.WithExtra(map[string]any{"id_token": header + "." + payload + ".sig"})
		return tok
	}
	email, err := emailFromIDToken(makeTok(`{"email":"budi@gmail.com","email_verified":true}`))
	if err != nil || email != "budi@gmail.com" {
		t.Fatalf("email=%q err=%v", email, err)
	}
	if _, err := emailFromIDToken(makeTok(`{"sub":"123"}`)); err == nil {
		t.Fatal("id_token tanpa email harus error")
	}
}

// TestOAuthWaitCallback menguji server callback loopback tanpa GUI:
// redirect pertama memberi code, redirect ulang tetap dapat halaman sukses.
func TestOAuthWaitCallback(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := New(db)
	defer svc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port, err := fixedOrFreePort(8699)
	if err != nil {
		t.Fatal(err)
	}
	state := "teststate123"
	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		code, err := svc.OAuthWaitCallback(ctx, port, state)
		ch <- result{code, err}
	}()
	// Tunggu server hidup.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=beda", port))
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server callback tidak hidup dalam 5 detik")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Redirect pertama: code diterima.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=%s&code=abc123", port, state))
	if err != nil {
		t.Fatalf("redirect pertama: %v", err)
	}
	resp.Body.Close()
	r := <-ch
	if r.err != nil {
		t.Fatalf("OAuthWaitCallback: %v", r.err)
	}
	if r.code != "abc123" {
		t.Fatalf("code=%q", r.code)
	}
	// Redirect ulang: halaman sukses, bukan connection refused.
	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=%s&code=dup", port, state))
	if err != nil {
		t.Fatalf("redirect ulang: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Login berhasil") {
		t.Fatalf("halaman sukses tidak tampil: %s", body)
	}
}

func TestOAuthReloginErrorDetection(t *testing.T) {
	rawErr := fmt.Errorf(`oauth2: "invalid_grant" "Token has been expired or revoked."`)
	if !isOAuthTokenRevoked(rawErr) {
		t.Fatal("invalid_grant harus terdeteksi sebagai token revoked")
	}
	wrapped := fmt.Errorf("fetch body: %w", &OAuthReloginError{Email: "devdinar05@gmail.com", Err: rawErr})
	email, ok := IsOAuthRelogin(wrapped)
	if !ok || email != "devdinar05@gmail.com" {
		t.Fatalf("IsOAuthRelogin: ok=%v email=%q", ok, email)
	}
}

func TestGmailScopeValidation(t *testing.T) {
	if !hasGmailScope("https://mail.google.com/ openid email") ||
		!hasGmailScope("openid https://mail.google.com/") {
		t.Fatal("scope Gmail harus terdeteksi")
	}
	if hasGmailScope("https://www.googleapis.com/auth/userinfo.email openid") || hasGmailScope("") {
		t.Fatal("token tanpa izin Gmail tidak boleh lolos")
	}
	err := oauthReloginScope("devdinar05@gmail.com", "openid email")
	email, ok := IsOAuthRelogin(err)
	if !ok || email != "devdinar05@gmail.com" {
		t.Fatalf("oauthReloginScope harus OAuthReloginError: ok=%v email=%q", ok, email)
	}
}

func TestParseGoogleToken(t *testing.T) {
	body := []byte(`{"access_token":"ya29.x","token_type":"Bearer","expires_in":3599,
		"refresh_token":"1//rt","scope":"https://mail.google.com/ openid email",
		"id_token":"eyJhbGciOiJub25lIn0.eyJlbWFpbCI6ImRldmRpbmFyMDVAZ21haWwuY29tIn0."}`)
	tok, err := parseGoogleToken(body)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "ya29.x" || !tok.Valid() {
		t.Fatalf("token salah: %+v", tok)
	}
	if scope, _ := tok.Extra("scope").(string); !hasGmailScope(scope) {
		t.Fatalf("scope hilang: %v", tok.Extra("scope"))
	}
	raw := map[string]any{}
	_ = json.Unmarshal(body, &raw)
	if email, err := emailFromIDToken(tok); err != nil || email != "devdinar05@gmail.com" {
		t.Fatalf("email dari id_token: %q %v", email, err)
	}
	// Respons tanpa access_token harus ditolak.
	if _, err := parseGoogleToken([]byte(`{"error":"invalid_grant"}`)); err == nil {
		t.Fatal("tanpa access_token harus error")
	}
}
