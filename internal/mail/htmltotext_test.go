package mail

import (
	"strings"
	"testing"
)

func TestHTMLToTextParagraphs(t *testing.T) {
	in := `<html><body><p> halo dunia </p><p>baris kedua</p></body></html>`
	got := HTMLToText(in)
	want := "halo dunia\n\nbaris kedua"
	if got != want {
		t.Fatalf("dapat %q, mau %q", got, want)
	}
}

func TestHTMLToTextLink(t *testing.T) {
	in := `<p>lihat <a href="https://contoh.id/x">halaman ini</a> ya</p>`
	got := HTMLToText(in)
	want := "lihat halaman ini (https://contoh.id/x) ya"
	if got != want {
		t.Fatalf("dapat %q, mau %q", got, want)
	}
}

func TestHTMLToTextBareLink(t *testing.T) {
	in := `<a href="https://contoh.id">https://contoh.id</a>`
	got := HTMLToText(in)
	if got != "https://contoh.id" {
		t.Fatalf("dapat %q, mau URL polos", got)
	}
}

func TestHTMLToTextSkipsScriptStyle(t *testing.T) {
	in := `<head><style>p{color:red}</style><script>alert(1)</script></head><body><p>isi</p></body>`
	got := HTMLToText(in)
	if strings.Contains(got, "alert") || strings.Contains(got, "color") {
		t.Fatalf("script/style bocor: %q", got)
	}
	if got != "isi" {
		t.Fatalf("dapat %q, mau %q", got, "isi")
	}
}

func TestHTMLToTextListAndBreak(t *testing.T) {
	in := `<p>a<br>b</p><ul><li>satu</li><li>dua</li></ul>`
	got := HTMLToText(in)
	for _, want := range []string{"a\nb", "satu", "dua"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hilang %q dalam %q", want, got)
		}
	}
}
