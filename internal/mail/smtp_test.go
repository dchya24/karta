package mail

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"os"
	"strings"
	"testing"
)

// fakeSMTP adalah server SMTP dialog-minimal untuk test SendMail.
type fakeSMTP struct {
	listener net.Listener
	mailFrom string
	rcpts    []string
	data     string
	quitSeen bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{listener: l}
	go f.serve()
	t.Cleanup(func() { l.Close() })
	return f
}

// serve memproses satu koneksi: dialog SMTP sederhana.
func (f *fakeSMTP) serve() {
	conn, err := f.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	writeLine := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
	writeLine("220 fake ready")
	inData := false
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				f.data = data.String()
				writeLine("250 ok queued")
				continue
			}
			data.WriteString(line + "\n")
			continue
		}
		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"),
			strings.HasPrefix(strings.ToUpper(line), "HELO"):
			writeLine("250 fake greets you")
		case strings.HasPrefix(strings.ToUpper(line), "AUTH"):
			writeLine("235 ok authed")
		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"):
			f.mailFrom = line[len("MAIL FROM:"):]
			writeLine("250 ok")
		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
			f.rcpts = append(f.rcpts, line[len("RCPT TO:"):])
			writeLine("250 ok")
		case strings.HasPrefix(strings.ToUpper(line), "DATA"):
			writeLine("354 end with .")
			inData = true
		case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
			f.quitSeen = true
			writeLine("221 bye")
			return
		default:
			writeLine("250 ok")
		}
	}
}

func TestSendMailPlain(t *testing.T) {
	f := newFakeSMTP(t)
	port := f.listener.Addr().(*net.TCPAddr).Port
	err := SendMail(SMTPConfig{Host: "127.0.0.1", Port: port},
		OutgoingMessage{
			From: "aku@contoh.id", To: []string{"dia@contoh.id"},
			Subject: "Halo", Body: "Isi pesan uji.",
		})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.mailFrom, "aku@contoh.id") {
		t.Fatalf("MAIL FROM %q", f.mailFrom)
	}
	if len(f.rcpts) != 1 || !strings.Contains(f.rcpts[0], "dia@contoh.id") {
		t.Fatalf("RCPT %q", f.rcpts)
	}
	for _, want := range []string{"Subject: Halo", "Isi pesan uji.", "Date:"} {
		if !strings.Contains(f.data, want) {
			t.Fatalf("data tidak mengandung %q:\n%s", want, f.data)
		}
	}
}

func TestSendMailWithAttachment(t *testing.T) {
	f := newFakeSMTP(t)
	port := f.listener.Addr().(*net.TCPAddr).Port
	att := t.TempDir() + "/dok.txt"
	if err := writeFile(att, []byte("isi lampiran")); err != nil {
		t.Fatal(err)
	}
	err := SendMail(SMTPConfig{Host: "127.0.0.1", Port: port},
		OutgoingMessage{
			From: "aku@contoh.id", To: []string{"dia@contoh.id", "cc@contoh.id"},
			Subject: "Lampiran", Body: "Dengan lampiran.",
			AttachmentPaths: []string{att},
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.rcpts) != 2 {
		t.Fatalf("RCPT %q, mau 2", f.rcpts)
	}
	// Isi lampiran di-encode base64 oleh MIME writer.
	wantB64 := base64.StdEncoding.EncodeToString([]byte("isi lampiran"))
	for _, want := range []string{"dok.txt", "multipart/mixed", wantB64} {
		if !strings.Contains(f.data, want) {
			t.Fatalf("data tidak mengandung %q", want)
		}
	}
}

func TestSendMailValidation(t *testing.T) {
	if err := SendMail(SMTPConfig{Host: "127.0.0.1", Port: 1},
		OutgoingMessage{From: "a@b", To: []string{}}); err == nil {
		t.Fatal("To kosong harus gagal")
	}
	if err := SendMail(SMTPConfig{Host: "127.0.0.1", Port: 1},
		OutgoingMessage{From: "", To: []string{"x@y"}}); err == nil {
		t.Fatal("From kosong harus gagal")
	}
	if _, err := ComposeMessage(OutgoingMessage{
		From: "a@b", To: []string{"x@y"},
		AttachmentPaths: []string{"/tidak/ada/file.txt"},
	}); err == nil {
		t.Fatal("lampiran hilang harus gagal saat compose")
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
