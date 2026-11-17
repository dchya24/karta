package app

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// smtpFakePort menjalankan fake SMTP server (dialog minimal) untuk test
// SendDraft dan mengembalikan portnya.
func smtpFakePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go serveFakeSMTP(l)
	return l.Addr().(*net.TCPAddr).Port
}

func serveFakeSMTP(l net.Listener) {
	conn, err := l.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	write := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
	write("220 fake ready")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				write("250 ok")
			}
			continue
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			write("250 fake")
		case strings.HasPrefix(up, "AUTH"):
			write("235 ok")
		case strings.HasPrefix(up, "DATA"):
			write("354 go")
			inData = true
		case strings.HasPrefix(up, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}
