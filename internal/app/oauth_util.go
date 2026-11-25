package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"os/exec"
	"runtime"

	"karta/internal/store"
)

// randomToken menghasilkan string acak url-safe n karakter.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// pkceS256 menghitung challenge PKCE dari verifier (RFC 7636).
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// freePort meminta OS port kosong (untuk server callback).
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("cari port kosong: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// openInSystemBrowser membuka URL di browser default OS.
func openInSystemBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
}

// oauthPort adalah port tetap callback loopback (RFC 8252): sama tiap
// login, agar tab browser lama tetap valid.
const oauthPort = 8699

// fixedOrFreePort mengembalikan port tetap bila bisa di-bind; kalau sibuk,
// jatuh ke port acak dari OS.
func fixedOrFreePort(preferred int) (int, error) {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
	if err == nil {
		l.Close()
		return preferred, nil
	}
	return freePort()
}

// dialIMAP membuka koneksi IMAP akun sesuai metode autentikasinya:
// password (keyring) atau OAuth2 (token keyring + refresh otomatis).
func (s *Service) dialIMAP(ctx context.Context, acc store.Account) (IMAPClient, error) {
	cfg, err := s.imapConfig(ctx, acc)
	if err != nil {
		return nil, err
	}
	return imapDial(cfg)
}
