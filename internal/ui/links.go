package ui

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui/richtext"
)

var linkPattern = regexp.MustCompile(`(?i)(?:https?://|mailto:)[^\s<>()]+`)

// linkifyText mengubah teks polos menjadi richtext.Text dengan atribut Link
// pada URL http(s):// dan mailto: sehingga dapat diklik di reading pane.
func linkifyText(plain string) richtext.Text {
	var t richtext.Text
	matches := linkPattern.FindAllStringIndex(plain, -1)
	if len(matches) == 0 {
		return richtext.NewText(plain)
	}
	pos := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		raw := plain[start:end]
		clean := strings.TrimRight(raw, ".,;:!?'\"])")
		if clean == "" {
			continue
		}
		linkEnd := start + len(clean)
		if start > pos {
			t = t.Append(plain[pos:start], richtext.Attributes{})
		}
		t = t.Append(clean, richtext.Attributes{Link: clean})
		pos = linkEnd
	}
	if pos < len(plain) {
		t = t.Append(plain[pos:], richtext.Attributes{})
	}
	return t
}

// parseMailto mengekstrak alamat tujuan dan subjek opsional dari tautan mailto:.
func parseMailto(raw string) (to, subject string) {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimPrefix(raw, "mailto:"), ""
	}
	to = u.Opaque
	subject = u.Query().Get("subject")
	return strings.TrimSpace(to), strings.TrimSpace(subject)
}
