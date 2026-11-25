package ui

import (
	"context"
	"log"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"karta/internal/app"
)

// composerForm menggambar form Compose/Reply/Reply All/Forward (PRD §3.4).
func (s *state) composerForm(c *ui.Context) {
	title := map[string]string{
		"new": "Email Baru", "reply": "Balas", "reply_all": "Balas Semua", "forward": "Teruskan",
	}[s.composerMode]
	ui.Text(c, title).Bold().FontSize(18)

	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Kepada").Width(100)
		ui.TextInput(c.Key("c-to"), &s.dTo).Grow(1).Placeholder("a@contoh.id, b@contoh.id").Label("Kepada")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Cc").Width(100)
		ui.TextInput(c.Key("c-cc"), &s.dCc).Grow(1).Placeholder("opsional").Label("Cc")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Subjek").Width(100)
		ui.TextInput(c.Key("c-subject"), &s.dSubject).Grow(1).Label("Subjek")
	})

	if s.dDoc == nil {
		s.dDoc = richtext.NewDocument(richtext.NewText(s.dBody))
	}
	ui.Text(c, "Isi").TextColor(c.Theme().TextMuted)
	richtext.Editor(c.Key("composer-body"), s.dDoc, richtext.EditorOptions{
		Label:       "Isi pesan",
		Placeholder: "Tulis pesan…",
	}).Grow(1)

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "Lampirkan").Label("Lampirkan file").Clicked() {
			s.attachFiles()
		}
		if ui.Button(c, "Kirim").Clicked() {
			s.sendDraft()
			return
		}
		if ui.Button(c, "Simpan Draft").Clicked() {
			s.saveDraftToIMAP()
			return
		}
		if ui.Button(c, "Batal").Clicked() {
			s.closeComposer()
		}
	})
	if len(s.dFiles) > 0 {
		ui.Textf(c, "Lampiran: %s", strings.Join(s.dFiles, ", ")).
			TextColor(c.Theme().TextMuted).FontSize(12)
	}
	if s.dStatus != "" {
		ui.Textf(c, "%s", s.dStatus).TextColor(c.Theme().TextMuted).FontSize(12)
	}
}

// openComposer membuka composer dengan pra-isi sesuai mode.
func (s *state) openComposer(mode string) {
	s.composerOpen = true
	s.composerMode = mode
	s.dTo, s.dCc, s.dSubject, s.dBody = "", "", "", ""
	s.dFiles, s.dStatus, s.dDoc = nil, "", nil

	if mode == "new" || s.selected >= len(s.msgs) {
		return
	}
	m, err := s.svc.PeekMessage(s.msgs[s.selected].ID)
	if err != nil {
		return
	}
	// Reply/forward dikirim dari akun pesan asal, bukan akun terpilih.
	if acc, ok := s.folderAcc[m.FolderID]; ok {
		s.account = acc
	}
	quote := ""
	if m.PlainText != "" {
		quote = "\n\n----- Pesan asli -----\n" + m.PlainText
	}
	switch mode {
	case "reply":
		s.dTo = firstAddr(m.SenderAddr)
		s.dSubject = rePrefix(m.Subject)
		s.dBody = quote
	case "reply_all":
		s.dTo = firstAddr(m.SenderAddr)
		s.dCc = replyAllCc(m.To, m.Cc, s.dTo, s.accountEmail(s.account))
		s.dSubject = rePrefix(m.Subject)
		s.dBody = quote
	case "forward":
		s.dSubject = fwdPrefix(m.Subject)
		s.dBody = "----- Pesan diteruskan -----\nDari: " + m.Sender +
			"\nSubjek: " + m.Subject + "\n\n" + m.PlainText
	}
}

// openMailto membuka composer baru dengan tujuan dan subjek dari tautan mailto:.
func (s *state) openMailto(raw string) {
	s.openComposer("new")
	s.dTo, s.dSubject = parseMailto(raw)
}

func (s *state) accountEmail(accID int) string {
	for _, sec := range s.sections {
		if sec.acc.ID == accID {
			return sec.acc.Email
		}
	}
	return ""
}

// replyAllCc mengumpulkan penerima lain dari To + Cc tanpa duplikat, tanpa
// pengirim (yang sudah di To), dan tanpa alamat akun sendiri.
func replyAllCc(origTo, origCc, excludeTo, selfEmail string) string {
	seen := map[string]bool{}
	for _, skip := range []string{excludeTo, selfEmail} {
		if addr := bareAddr(skip); addr != "" {
			seen[addr] = true
		}
	}
	var out []string
	for _, raw := range []string{origTo, origCc} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if list, err := netmail.ParseAddressList(raw); err == nil {
			for _, a := range list {
				key := strings.ToLower(strings.TrimSpace(a.Address))
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, a.Address)
			}
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			addr := bareAddr(part)
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return strings.Join(out, ", ")
}

func bareAddr(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if a, err := netmail.ParseAddress(raw); err == nil {
		return strings.ToLower(strings.TrimSpace(a.Address))
	}
	return strings.ToLower(raw)
}

// attachFiles memilih file via dialog native (main thread) lalu menambahkan.
func (s *state) attachFiles() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:       "Pilih lampiran",
		Multiple:    true,
		ButtonLabel: "Lampirkan",
	})
	if err != nil {
		log.Printf("ui: dialog lampiran: %v", err)
		return
	}
	s.dFiles = append(s.dFiles, paths...)
}

// draftFromForm mengumpulkan isi form menjadi app.Draft.
func (s *state) draftFromForm() app.Draft {
	body := s.dBody
	if s.dDoc != nil {
		body = s.dDoc.Text().PlainText()
	}
	return app.Draft{
		To:          s.dTo,
		Cc:          s.dCc,
		Subject:     s.dSubject,
		Body:        body,
		Attachments: s.dFiles,
	}
}

// sendDraft mengirim draft di goroutine; hasil dilaporkan via Window.Update.
func (s *state) sendDraft() {
	d := s.draftFromForm()
	account := s.account
	win := s.win
	if strings.TrimSpace(d.To) == "" {
		s.dStatus = "Isi kolom Kepada dulu."
		return
	}
	s.dStatus = "Mengirim…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := s.svc.SendDraft(ctx, account, d)
		win.Update(func() {
			if err != nil {
				s.dStatus = "Gagal kirim: " + err.Error()
				return
			}
			s.closeComposer()
			s.refreshNow()
		})
	}()
}

// saveDraftToIMAP menyimpan draft ke folder Drafts IMAP di goroutine.
func (s *state) saveDraftToIMAP() {
	d := s.draftFromForm()
	account := s.account
	win := s.win
	s.dStatus = "Menyimpan draft…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := s.svc.SaveDraft(ctx, account, d)
		win.Update(func() {
			if err != nil {
				s.dStatus = "Gagal simpan draft: " + err.Error()
				return
			}
			s.dStatus = "Draft tersimpan di server."
		})
	}()
}

// closeComposer menutup composer dan mengosongkan form.
func (s *state) closeComposer() {
	s.composerOpen = false
	s.composerMode = ""
	s.dTo, s.dCc, s.dSubject, s.dBody = "", "", "", ""
	s.dFiles, s.dStatus, s.dDoc = nil, "", nil
}

// rePrefix menambahkan "Re: " bila subjek belum memilikinya.
func rePrefix(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

// fwdPrefix menambahkan "Fwd: " bila subjek belum memilikinya.
func fwdPrefix(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		return subject
	}
	return "Fwd: " + subject
}

// firstAddr mengambil alamat pertama dari "a@b, c@d".
func firstAddr(list string) string {
	if i := strings.IndexByte(list, ','); i >= 0 {
		return strings.TrimSpace(list[:i])
	}
	return strings.TrimSpace(list)
}
