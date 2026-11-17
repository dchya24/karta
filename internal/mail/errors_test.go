package mail

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestIsTransientClassification(t *testing.T) {
	imapAuth := &imap.Error{Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeAuthenticationFailed, Text: "bad password"}
	imapBusy := &imap.Error{Type: imap.StatusResponseTypeNo, Text: "server busy"}
	var netErr net.Error = &fakeTimeout{}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient-wrapped", &TransientError{Op: "dial", Err: errors.New("refused")}, true},
		{"transient-wrapped-auth", &TransientError{Op: "dial", Err: imapAuth}, true},
		{"imap-auth-failed", imapAuth, false},
		{"imap-server-busy", imapBusy, true},
		{"net-timeout", netErr, true},
		{"plain", errors.New("kesalahan logika"), false},
		{"eof-string", fmt.Errorf("baca header: EOF"), true},
		{"tls-string", fmt.Errorf("handshake: tls: alert"), true},
	}
	for _, c := range cases {
		if got := IsTransient(c.err); got != c.want {
			t.Fatalf("IsTransient(%s)=%v mau %v", c.name, got, c.want)
		}
	}
}

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }
