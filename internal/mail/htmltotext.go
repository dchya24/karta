package mail

import (
	"golang.org/x/net/html"
	"strings"
)

// blockTags membuka paragraf baru sebelum dan sesudah kontennya.
var blockTags = map[string]bool{
	"p": true, "div": true, "tr": true, "table": true,
	"blockquote": true, "li": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "pre": true, "hr": true,
}

// skipTags tidak dikonversi sama sekali.
var skipTags = map[string]bool{
	"script": true, "style": true, "head": true, "title": true, "meta": true,
}

// HTMLToText mengonversi HTML menjadi teks bersih: jeda paragraf dipertahankan,
// link dirender sebagai "teks (url)" bila url berbeda dari teksnya.
func HTMLToText(raw string) string {
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	var b strings.Builder
	writeNode(root, &b)
	return collapseBlankLines(b.String())
}

func writeNode(n *html.Node, b *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		// Rapatkan whitespace agar layout HTML tidak menghasilkan spasi ganda.
		if text := strings.Join(strings.Fields(n.Data), " "); text != "" {
			b.WriteString(text)
			b.WriteString(" ")
		}
	case html.ElementNode:
		if skipTags[n.Data] {
			return
		}
		if n.Data == "br" {
			b.WriteString("\n")
			return
		}
		if blockTags[n.Data] {
			b.WriteString("\n\n")
		}
		if n.Data == "a" {
			writeLink(n, b)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeNode(c, b)
		}
		if blockTags[n.Data] {
			b.WriteString("\n")
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeNode(c, b)
	}
}

// writeLink menulis isi <a> beserta href-nya bila href bukan teks itu sendiri.
func writeLink(a *html.Node, b *strings.Builder) {
	href := ""
	for _, attr := range a.Attr {
		if attr.Key == "href" {
			href = strings.TrimSpace(attr.Val)
			break
		}
	}
	start := b.Len()
	for c := a.FirstChild; c != nil; c = c.NextSibling {
		writeNode(c, b)
	}
	text := strings.TrimSpace(b.String()[start:])
	if href == "" || text == href || strings.Contains(text, href) {
		return
	}
	if text == "" {
		b.WriteString(href)
	} else {
		b.WriteString("(")
		b.WriteString(href)
		b.WriteString(") ")
	}
}

func collapseBlankLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	for _, line := range lines {
		// Rapatkan spasi dalam baris: layout HTML sering menulis spasi ganda.
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	var b strings.Builder
	prevBlank := true // tanpa newline di awal
	for _, line := range out {
		blank := strings.TrimSpace(line) == ""
		if blank && (prevBlank || b.Len() == 0) {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
		prevBlank = blank
	}
	return strings.TrimRight(b.String(), "\n")
}
