package mail

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestXoauth2Response(t *testing.T) {
	got := string(xoauth2Response("a@gmail.com", "tok123"))
	want := "user=a@gmail.com\x01auth=Bearer tok123\x01\x01"
	if got != want {
		t.Fatalf("resp %q mau %q", got, want)
	}
}

func TestXOAUTH2ClientStart(t *testing.T) {
	c := XOAUTH2Client{User: "a@gmail.com", Token: "tok"}
	mech, ir, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if mech != "XOAUTH2" {
		t.Fatalf("mech %q", mech)
	}
	dec, err := base64.StdEncoding.DecodeString(string(ir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dec), "auth=Bearer tok") {
		t.Fatalf("ir %q", string(dec))
	}
	if resp, err := c.Next([]byte("challenge")); err != nil || len(resp) != 0 {
		t.Fatalf("Next=%q err=%v", resp, err)
	}
}

func TestSMTPAuthSelection(t *testing.T) {
	if _, err := SMTPAuth(AuthOAuth2, "gmail.com", "a@b", "tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := SMTPAuth(AuthPassword, "gmail.com", "a@b", "pw"); err != nil {
		t.Fatal(err)
	}
	// Tanpa user/token harus ditolak saat Start.
	a, _ := SMTPAuth(AuthOAuth2, "gmail.com", "", "tok")
	if _, _, err := a.Start(nil); err == nil {
		t.Fatal("token kosong harus ditolak")
	}
}
