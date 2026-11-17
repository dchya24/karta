package ui

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"karta/internal/app"
)

// composerForm menggambar form Compose/Reply/Forward (PRD §3.4).
func (s *state) composerForm(c *ui.Context) {
	title := map[string]string{
		"new": "Email Baru", "reply": "Balas", "forward": "Teruskan",
	}[s.composerMode]
	ui.Text(c, title).Bold().FontSize(18)

	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Kepada").Width(100)
		ui.TextInputBase(c, &s.dTo).Grow(1).Placeholder("a@contoh.id, b@contoh.id").Label("Kepada")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Cc").Width(100)
		ui.TextInputBase(c, &s.dCc).Grow(1).Placeholder("opsional").Label("Cc")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Subjek").Width(100)
		ui.TextInputBase(c, &s.dSubject).Grow(1).Label("Subjek")
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
	quote := ""
	if m.PlainText != "" {
		quote = "\n\n----- Pesan asli -----\n" + m.PlainText
	}
	switch mode {
	case "reply":
		s.dTo = firstAddr(m.SenderAddr)
		s.dSubject = rePrefix(m.Subject)
		s.dBody = quote
	case "forward":
		s.dSubject = fwdPrefix(m.Subject)
		s.dBody = "----- Pesan diteruskan -----\nDari: " + m.Sender +
			"\nSubjek: " + m.Subject + "\n\n" + m.PlainText
	}
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
