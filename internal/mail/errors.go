package mail

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// TransientError menandai kegagalan jaringan yang bisa dicoba lagi
// (dial gagal, TLS handshake putus, dsb).
type TransientError struct {
	Op  string
	Err error
}

func (e *TransientError) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *TransientError) Unwrap() error { return e.Err }

// IsTransient memutuskan apakah error layak dicoba ulang:
// dial/TLS/timeout/putus koneksi → ya; login salah → tidak;
// error IMAP lain → ya (server sesekali sibuk); sisanya → tidak.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var te *TransientError
	if errors.As(err, &te) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && (ne.Timeout() || ne.Temporary()) {
		return true
	}
	var imapErr *imap.Error
	if errors.As(err, &imapErr) {
		return imapErr.Code != imap.ResponseCodeAuthenticationFailed
	}
	// Error koneksi putus sering berupa io.EOF / reset dari wrapper bawaan
	// pustaka tanpa tipe khusus; deteksi teks sebagai jaring pengaman.
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"eof", "connection reset", "broken pipe", "use of closed",
		"i/o timeout", "connection refused", "tls:",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
