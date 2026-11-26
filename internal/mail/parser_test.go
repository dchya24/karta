package mail

import (
	"strings"
	"testing"
)

const plainMIME = "From: Pengirim Utama <utama@contoh.id>\r\n" +
	"To: Penerima <penerima@contoh.id>\r\n" +
	"Cc: Teman <teman@contoh.id>\r\n" +
	"Subject: Uji coba\r\n" +
	"Date: Mon, 17 Nov 2026 10:00:00 +0700\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Halo ini isi pesan.\r\n\r\nBaris kedua.\r\n"

func TestParseMessagePlain(t *testing.T) {
	m, err := ParseMessage([]byte(plainMIME))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Uji coba" {
		t.Fatalf("subject %q", m.Subject)
	}
	if m.From != "Pengirim Utama <utama@contoh.id>" {
		t.Fatalf("from %q", m.From)
	}
	if !strings.Contains(m.PlainText, "Baris kedua.") {
		t.Fatalf("plain %q", m.PlainText)
	}
	if m.RawHTML != "" {
		t.Fatalf("raw_html harus kosong, dapat %q", m.RawHTML)
	}
	if m.Date.Year() != 2026 {
		t.Fatalf("date %v", m.Date)
	}
}

func TestParseMessageHTMLOnly(t *testing.T) {
	raw := "From: a@contoh.id\r\nSubject: html\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>paragraf satu</p><p>paragraf dua</p>"
	m, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := "paragraf satu\n\nparagraf dua"
	if m.PlainText != want {
		t.Fatalf("plain %q, mau %q", m.PlainText, want)
	}
	if m.RawHTML == "" {
		t.Fatal("raw_html harus tersimpan")
	}
}

func TestParseMessageMultipartWithAttachment(t *testing.T) {
	boundary := "bnd42"
	raw := "From: a@contoh.id\r\nSubject: lampiran\r\n" +
		"Content-Type: multipart/mixed; boundary=" + boundary + "\r\n\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"isi utama\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: application/pdf; name=\"dok.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"dok.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		"aGVsbG8=\r\n" +
		"--" + boundary + "--\r\n"
	m, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.PlainText, "isi utama") {
		t.Fatalf("plain %q", m.PlainText)
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("lampiran %d, mau 1", len(m.Attachments))
	}
	a := m.Attachments[0]
	if a.Filename != "dok.pdf" || string(a.Data) != "hello" {
		t.Fatalf("lampiran %+v data %q", a.Filename, a.Data)
	}
}

func TestParseMessageDate(t *testing.T) {
	m, err := ParseMessage([]byte(plainMIME))
	if err != nil {
		t.Fatal(err)
	}
	if m.Date.IsZero() {
		t.Fatal("date kosong")
	}
	// +0700 harus dipertahankan: 10:00 WIB = 03:00 UTC.
	if got := m.Date.UTC().Format("15:04"); got != "03:00" {
		t.Fatalf("UTC %q, mau 03:00", got)
	}
}

func TestParseMessageThreadHeaders(t *testing.T) {
	raw := "From: budi@contoh.id\r\n" +
		"Subject: Re: Rencana rilis\r\n" +
		"Message-ID: <c@x.id>\r\n" +
		"In-Reply-To: <b@x.id>\r\n" +
		"References: <a@x.id> <b@x.id>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"isi\r\n"
	m, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// Nilai mentah dipertahankan; normalisasi ada di internal/store.
	if m.MessageID != "<c@x.id>" {
		t.Fatalf("MessageID %q", m.MessageID)
	}
	if m.InReplyTo != "<b@x.id>" {
		t.Fatalf("InReplyTo %q", m.InReplyTo)
	}
	if m.References != "<a@x.id> <b@x.id>" {
		t.Fatalf("References %q", m.References)
	}
	// Pesan tanpa header reply sama sekali: kosong, bukan error.
	plain, err := ParseMessage([]byte(plainMIME))
	if err != nil {
		t.Fatal(err)
	}
	if plain.MessageID != "" || plain.InReplyTo != "" || plain.References != "" {
		t.Fatalf("header thread seharusnya kosong: %+v", plain)
	}
}
