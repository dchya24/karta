package mail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func sampleMessage() *ExportMessage {
	return &ExportMessage{
		FromName:  "Pengirim",
		FromAddr:  "pengirim@contoh.id",
		To:        "tujuan@contoh.id",
		Cc:        "teman@contoh.id",
		Subject:   "Salam",
		Date:      time.Date(2026, 2, 5, 9, 30, 0, 0, time.UTC),
		MessageID: "abc@contoh.id",
		PlainText: "Isi pesan\nbaris kedua",
	}
}

func TestBuildEMLSimple(t *testing.T) {
	data, err := BuildEML(sampleMessage())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"From: Pengirim <pengirim@contoh.id>\r\n",
		"To: tujuan@contoh.id\r\n",
		"Cc: teman@contoh.id\r\n",
		"Subject: Salam\r\n",
		"Date: Thu, 05 Feb 2026 09:30:00 +0000\r\n",
		"Message-ID: <abc@contoh.id>\r\n",
		"MIME-Version: 1.0\r\n",
		`Content-Type: text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding: quoted-printable\r\n",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("header/body %q tidak ada di:\n%s", want, s)
		}
	}
	if strings.Contains(s, "\n\nbaris") || !strings.Contains(s, "Isi pesan\r\n") {
		t.Fatalf("body teks salah:\n%s", s)
	}
	// Boleh di-parse kembali sebagai RFC822: header terbaca utuh.
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse balik eml: %v", err)
	}
	if msg.Header.Get("Subject") != "Salam" || msg.Header.Get("From") != "Pengirim <pengirim@contoh.id>" {
		t.Fatalf("header parse balik: %+v", msg.Header)
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "Isi pesan\r\nbaris kedua" {
		t.Fatalf("body parse balik: %q", body)
	}
}

func TestBuildEMLMultipartAttachment(t *testing.T) {
	m := sampleMessage()
	m.Attachments = []ExportAttachment{
		{Filename: "laporan.txt", ContentType: "text/plain", Data: []byte("isi lampiran biner")},
	}
	// Lampiran tanpa BLOB tidak boleh jadi part.
	m.Attachments = append(m.Attachments, ExportAttachment{Filename: "kosong.bin"})
	data, err := BuildEML(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "Content-Type: multipart/mixed; boundary=") {
		t.Fatalf("bukan multipart/mixed:\n%s", s)
	}
	if strings.Contains(s, "kosong.bin") {
		t.Fatal("lampiran tanpa BLOB ikut diekspor")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("media type: %q %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	parts := 0
	for {
		p, err := mr.NextRawPart() // raw: header CTE tetap ada, body belum didekode
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		parts++
		raw, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		var dispName string
		if disp := p.Header.Get("Content-Disposition"); disp != "" {
			_, dparams, derr := mime.ParseMediaType(disp)
			if derr != nil {
				t.Fatal(derr)
			}
			dispName = dparams["filename"]
		}
		switch {
		case dispName == "":
			// Part teks: quoted-printable.
			if p.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
				t.Fatalf("part teks encoding: %q", p.Header.Get("Content-Transfer-Encoding"))
			}
			txt, _ := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
			if string(txt) != "Isi pesan\r\nbaris kedua" {
				t.Fatalf("part teks: %q", txt)
			}
		case dispName == "laporan.txt":
			if p.Header.Get("Content-Transfer-Encoding") != "base64" {
				t.Fatal("part lampiran bukan base64")
			}
			dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatalf("decode base64: %v", err)
			}
			if string(dec) != "isi lampiran biner" {
				t.Fatalf("isi lampiran: %q", dec)
			}
		default:
			t.Fatalf("part tak terduga: %v", p.Header)
		}
	}
	if parts != 2 {
		t.Fatalf("jumlah part = %d, mau 2", parts)
	}
}

func TestBuildEMLNonASCIIHeader(t *testing.T) {
	m := sampleMessage()
	m.Subject = "Uji ✉ emoji"
	m.FromName = "Sudirman Åkesson"
	data, err := BuildEML(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "Subject: =?utf-8?q?") {
		t.Fatalf("subjek tidak ter-encode:\n%s", s)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if subj != "Uji ✉ emoji" {
		t.Fatalf("subjek ter-decode: %q", subj)
	}
}

func TestBuildMboxEscaping(t *testing.T) {
	m := sampleMessage()
	m.PlainText = "Halo,\nFrom aku\nbaris tiga\nFrom  dua spasi"
	m.Date = time.Date(2026, 2, 5, 9, 30, 0, 0, time.UTC)
	data, err := BuildMbox(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "From pengirim@contoh.id Thu Feb 05 09:30:00 2026\n") {
		t.Fatalf("baris amplop salah:\n%s", s)
	}
	if !strings.Contains(s, "\n>From aku\n") {
		t.Fatalf("escaping >From tidak ada:\n%s", s)
	}
	// Baris body "From  dua spasi" (diawali "From ") tetap di-escape —
	// konvensi mbox: setiap baris yang diawali "From " di-escape.
	if !strings.Contains(s, "\n>From  dua spasi") {
		t.Fatalf("baris berawalan From tidak di-escape:\n%s", s)
	}
	// Setelah baris amplop, tidak ada "From " palsu yang lolos.
	rest := s[strings.Index(s, "\n")+1:]
	if strings.Contains(rest, "\nFrom ") {
		t.Fatalf("ada baris From tanpa escaping:\n%s", rest)
	}
	if !strings.Contains(s, "Subject: Salam\n") {
		t.Fatalf("header hilang:\n%s", s)
	}
	// Entri berakhir baris kosong pemisah.
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("tidak berakhir baris kosong: %q", s[len(s)-8:])
	}
}

func TestBuildMboxZeroDateAndNoAddr(t *testing.T) {
	m := &ExportMessage{Subject: "tanpa tanggal", PlainText: "x"}
	data, err := BuildMbox(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "From MAILER-DAEMON ") {
		t.Fatalf("amplop tanpa pengirim salah: %q", s[:40])
	}
	if strings.Contains(s, "\nDate: ") {
		t.Fatalf("header Date harus hilang saat kosong:\n%s", s)
	}
}
