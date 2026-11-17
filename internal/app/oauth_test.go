package app

import (
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-sasl"
	"karta/internal/mail"
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
