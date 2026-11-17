package mail

import (
	"errors"
	"fmt"
	"net/smtp"

	"github.com/emersion/go-sasl"
)

// AuthMethod memilih mekanisme autentikasi IMAP/SMTP.
type AuthMethod int

const (
	AuthPassword AuthMethod = iota // LOGIN/PLAIN dengan password (default)
	AuthOAuth2                     // XOAUTH2 dengan access token Google
)

// xoauth2Response membangun initial response XOAUTH2 per RFC 7628:
// "user=<user>\x01auth=Bearer <token>\x01\x01". Raw bytes — library yang
// mengirim (go-imap, net/smtp) yang meng-base64 sendiri.
func xoauth2Response(user, token string) []byte {
	return []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", user, token))
}

// XOAUTH2Client mengimplementasikan sasl.Client untuk AUTHENTICATE XOAUTH2.
type XOAUTH2Client struct {
	User  string
	Token string
}

// Start mengirim mekanisme + initial response (SASL-IR).
func (c XOAUTH2Client) Start() (string, []byte, error) {
	return "XOAUTH2", xoauth2Response(c.User, c.Token), nil
}

// Next dipanggil bila server menolak initial response: Google mengirim
// error JSON base64 lalu menungug balasan kosong sebelum gagal.
func (c XOAUTH2Client) Next(challenge []byte) ([]byte, error) {
	return []byte{}, nil
}

// smtpXOAUTH2Auth mengimplementasikan smtp.Auth untuk AUTH XOAUTH2.
type smtpXOAUTH2Auth struct {
	user, token string
}

// Start mengembalikan mekanisme + initial response (net/smtp meng-base64
// sendiri isi toServer).
func (a *smtpXOAUTH2Auth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	if a.user == "" || a.token == "" {
		return "", nil, errors.New("xoauth2: user/token kosong")
	}
	return "XOAUTH2", []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.user, a.token)), nil
}

// Next: server yang menolak initial response mengirim challenge error
// JSON; net/smtp berhenti bila Next mengembalikan resp nil. Google SMTP
// menerima XOAUTH2 di initial response, jadi challenge ini hanya terjadi
// saat gagal — balas kosong lalu keluar.
func (a *smtpXOAUTH2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	return nil, nil
}

// SMTPAuth mengembalikan autentikator sesuai metode akun. host dipakai
// PlainAuth untuk pemeriksaan nama server TLS.
func SMTPAuth(method AuthMethod, host, user, secret string) (smtp.Auth, error) {
	switch method {
	case AuthOAuth2:
		return &smtpXOAUTH2Auth{user: user, token: secret}, nil
	default:
		return smtp.PlainAuth("", user, secret, host), nil
	}
}

// SASLAuth mengembalikan sasl.Client sesuai metode akun (untuk imapclient).
func SASLAuth(method AuthMethod, user, secret string) (sasl.Client, error) {
	switch method {
	case AuthOAuth2:
		return XOAUTH2Client{User: user, Token: secret}, nil
	default:
		return sasl.NewPlainClient("", user, secret), nil
	}
}
