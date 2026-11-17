// Package ui membangun antarmuka native mygo/ui Karta: layout 3-pane
// (sidebar, daftar pesan, reading pane) plus form tambah akun.
// Semua kode di sini berjalan di main thread; pekerjaan jaringan lewat
// goroutine yang melaporkan hasil via mygo Window.Update.
package ui

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"karta/internal/app"
	"karta/internal/sync"
	kartaicons "karta/assets/icons"
)

// state adalah kondisi UI: dibaca dan diubah hanya di main thread (view dan
// callback window), diperbarui dari goroutine lewat Window.Update.
type state struct {
	svc       *app.Service
	win       *mygo.Window
	folders   []app.Folder
	account   int    // akun terpilih (0 = pertama)
	folder    int    // folder terpilih (ID)
	folderSel string // pilihan sidebar (indeks folder sebagai string)
	query     string
	list      ui.ListState
	msgs      []app.MessageHeader
	selected  int

	// form tambah akun
	showAccount bool
	oauthBusy   bool   // flow OAuth sedang berjalan
	oauthMsg    string // status/error OAuth tampil inline di form
	oauthFail   bool   // oauthMsg = error (tampilkan merah)
	fEmail      string
	fPass       string
	fIMAPHost   string
	fIMAPPort   string
	fSMTPHost   string
	fSMTPPort   string
	fIMAPTLS    bool

	// pesan yang body-nya sedang diambil lazy (ID → berlangsung)
	fetching map[int]bool

	// composer
	composerOpen              bool
	composerMode              string // new | reply | forward
	dTo, dCc, dSubject, dBody string
	dDoc                      *richtext.Document
	dFiles                    []string
	dStatus                   string
}

// readyLogger dipanggil saat window utama siap (untuk metrik cold start).
var readyLogger func()

// SetReadyLogger memasang callback metrik siap (dipanggil main).
func SetReadyLogger(fn func()) { readyLogger = fn }

// Run membuka window utama, memulai worker sinkronisasi, dan menjalankan
// loop aplikasi sampai keluar.
func Run(svc *app.Service) error {
	st := &state{svc: svc, fetching: map[int]bool{}}
	syncCtx, cancelSync := context.WithCancel(context.Background())
	defer cancelSync()
	mygo.App.WhenReady(func() {
		st.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Karta",
			Width: 1100, Height: 720,
			MinWidth: 760, MinHeight: 480,
			StateKey: "karta",
			Content:  ui.View(st.view),
		})
		// Logo Karta: ikon window/taskbar (PNG) + ikon Dock (macOS).
		// PNG di-embed agar binary tetap satu file.
		if err := st.win.SetIcon(kartaicons.PNG256); err != nil {
			log.Printf("karta: set icon: %v", err)
		}
		if err := mygo.App.Dock.SetIcon(kartaicons.PNG256); err != nil {
			log.Printf("karta: set dock icon: %v", err)
		}
		st.reloadFolders()
		st.startSync(syncCtx)
		if readyLogger != nil {
			readyLogger()
		}
	})
	return mygo.App.Run()
}

// startSync menjalankan worker polling di goroutine; setiap siklus memicu
// repaint lewat Window.Update (main thread), tidak pernah sentuh widget langsung.
func (s *state) startSync(ctx context.Context) {
	win := s.win
	go sync.NewWorker(s.svc, 5*time.Minute, func() {
		win.Update(func() { s.reloadFolders() })
	}).Run(ctx)
}

// refreshNow menjalankan sinkronisasi segera di goroutine.
func (s *state) refreshNow() {
	win := s.win
	go func() {
		if err := s.svc.Refresh(context.Background()); err != nil {
			log.Printf("ui: refresh: %v", err)
		}
		win.Update(func() { s.reloadFolders() })
	}()
}

// reloadFolders memuat folder akun dan pesan folder terpilih. Main thread saja.
func (s *state) reloadFolders() {
	accs, err := s.svc.ListAccounts()
	if err != nil {
		log.Printf("ui: akun: %v", err)
		return
	}
	s.folders = nil
	if len(accs) > 0 {
		if s.account == 0 || s.account > accs[len(accs)-1].ID {
			s.account = accs[0].ID
		}
		fs, err := s.svc.ListFolders(s.account)
		if err != nil {
			log.Printf("ui: folder: %v", err)
		} else {
			s.folders = fs
		}
	}
	if len(s.folders) > 0 {
		s.folder = s.folders[0].ID
		s.folderSel = "0"
	} else {
		s.folder, s.folderSel = 0, ""
	}
	s.reloadMessages()
}

// reloadMessages memuat daftar pesan folder terpilih + filter query.
func (s *state) reloadMessages() {
	msgs, err := s.svc.ListMessages(s.folder, s.query)
	if err != nil {
		log.Printf("ui: pesan: %v", err)
		return
	}
	s.msgs = msgs
	if s.selected >= len(s.msgs) {
		s.selected = max(0, len(s.msgs)-1)
	}
}

// saveAccount membaca form manual (password), menyimpan akun, memicu
// sinkronisasi pertama. Login Google punya jalur sendiri di loginGoogle.
func (s *state) saveAccount() {
	imapPort, err := strconv.Atoi(s.fIMAPPort)
	if err != nil || imapPort <= 0 {
		imapPort = 993
	}
	smtpPort, err := strconv.Atoi(s.fSMTPPort)
	if err != nil || smtpPort <= 0 {
		smtpPort = 465
	}
	method := app.AuthPassword
	id, err := s.svc.AddAccount(context.Background(), app.AccountConfig{
		Email: s.fEmail, Password: s.fPass,
		IMAPHost: s.fIMAPHost, IMAPPort: imapPort, IMAPTLS: s.fIMAPTLS,
		SMTPHost: s.fSMTPHost, SMTPPort: smtpPort, SMTPTLS: true,
		Auth: method,
	})
	if err != nil {
		log.Printf("ui: tambah akun: %v", err)
		return
	}
	s.account = id
	s.showAccount = false
	s.reloadFolders()
	s.refreshNow() // sinkron pertama langsung
}

// fetchBodyLazy mengambil isi pesan di goroutine bila belum ter-cache.
func (s *state) fetchBodyLazy(id int) {
	if s.fetching[id] {
		return
	}
	s.fetching[id] = true
	win := s.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := s.svc.GetMessage(ctx, id); err != nil {
			log.Printf("ui: fetch body pesan %d: %v", id, err)
		}
		win.Update(func() { delete(s.fetching, id) })
	}()
}

// view adalah builder UI native; berjalan tiap frame di main thread.
func (s *state) view(c *ui.Context) {
	t := c.Theme()
	s.shortcuts(c)

	if s.composerOpen {
		ui.Column(c).Fill().Padding(24, 32).Gap(10).Children(func() {
			s.composerForm(c)
		})
		return
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// Pane 1: sidebar akun + folder + unread.
		ui.Column(c).Width(220).Shrink(0).PaddingY(12).Gap(6).Background(t.Surface).Children(func() {
			ui.Row(c).Padding(0, 16).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Image(c, kartaLogo).Size(28, 28)
				ui.Text(c, "Karta").Bold().Grow(1)
				if ui.Button(c, "+").Label("Tambah akun").Clicked() {
					s.showAccount = !s.showAccount
				}
			})
			if len(s.folders) > 0 {
				if ui.Sidebar(c, &s.folderSel, func() {
					for i, f := range s.folders {
						label := f.Name
						if f.Unread > 0 {
							label += " (" + strconv.Itoa(f.Unread) + ")"
						}
						ui.SidebarItem(c, strconv.Itoa(i), nil, label)
					}
				}).Grow(1).Label("Folder").Changed() {
					if idx, err := strconv.Atoi(s.folderSel); err == nil && idx >= 0 && idx < len(s.folders) {
						s.folder = s.folders[idx].ID
						s.reloadMessages()
					}
				}
			} else {
				ui.Text(c, "Belum ada akun.\nTekan + untuk menambah.").TextColor(t.TextMuted).Padding(16)
			}
			if ui.Button(c, "Sinkronkan").Label("Sinkronkan sekarang").Clicked() {
				s.refreshNow()
			}
		})

		// Pane 2+3: daftar pesan | reading pane; atau form akun.
		if s.showAccount {
			ui.Column(c).Grow(1).MinWidth(0).Padding(24, 32).Gap(10).Children(func() {
				s.accountForm(c)
			})
			return
		}
		var splitSize float32 = 380
		ui.Split(c, &splitSize, func() {
			ui.Column(c).Fill().Gap(0).Children(func() {
				ui.Row(c).Padding(8, 10).Children(func() {
					if ui.TextInputBase(c, &s.query).Grow(1).
						Placeholder("Cari (FTS5 lokal)").Label("Cari").Changed() {
						s.reloadMessages()
					}
				})
				s.list.Selected = &s.selected
				ui.List(c, &s.list, len(s.msgs), func(i int) { s.row(c, i) }).
					Grow(1).Children(func() {
					if len(s.msgs) == 0 {
						ui.Text(c, "Tidak ada pesan").TextColor(t.TextMuted).Padding(16)
					}
				})
			})
		}, func() {
			ui.Column(c).Grow(1).MinWidth(0).Padding(16, 20).Gap(8).Children(func() {
				s.readingPane(c)
			})
		}).Grow(1)
	})
}

// accountForm menggambar form tambah akun: input manual + tombol simpan,
// plus tombol Login via Google yang langsung memicu flow OAuth.
func (s *state) accountForm(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Tambah Akun").Bold().FontSize(18)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Email").Width(120)
		ui.TextInputBase(c, &s.fEmail).Grow(1).Placeholder("nama@contoh.id").Label("Email")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Password").Width(120)
		ui.TextInputBase(c, &s.fPass).Grow(1).Password().Placeholder("App password").Label("Password")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "IMAP host").Width(120)
		ui.TextInputBase(c, &s.fIMAPHost).Grow(1).Placeholder("imap.contoh.id").Label("IMAP host")
		ui.TextInputBase(c, &s.fIMAPPort).Width(80).Placeholder("993").Label("IMAP port")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "SMTP host").Width(120)
		ui.TextInputBase(c, &s.fSMTPHost).Grow(1).Placeholder("smtp.contoh.id").Label("SMTP host")
		ui.TextInputBase(c, &s.fSMTPPort).Width(80).Placeholder("465").Label("SMTP port")
	})
	ui.Checkbox(c, &s.fIMAPTLS, "Gunakan TLS untuk IMAP")
	if s.oauthMsg != "" {
		msg := ui.Textf(c, "%s", s.oauthMsg).FontSize(12)
		if s.oauthFail {
			msg.TextColor(t.Danger)
		} else {
			msg.TextColor(t.TextMuted)
		}
	}
	ui.Row(c).Gap(12).Children(func() {
		if ui.Button(c, s.saveLabel()).Clicked() && !s.oauthBusy {
			s.saveAccount()
		}
		if ui.Button(c, "Login via Google").Label("Login via Google (OAuth2)").Clicked() && !s.oauthBusy {
			s.loginGoogle()
		}
		if ui.Button(c, "Batal").Clicked() {
			s.showAccount = false
		}
	})
	ui.Text(c, "Login via Google: host Gmail diisi otomatis; klien ID dari"+
		" env KARTA_OAUTH_CLIENT_ID atau ~/.config/karta/oauth.json").
		TextColor(t.TextMuted).FontSize(12)
}

// loginGoogle memicu flow OAuth dari satu klik — tanpa input email:
// email akun dibaca otomatis dari id_token Google, lalu akun Gmail dibuat.
func (s *state) loginGoogle() {
	win := s.win
	s.oauthBusy, s.oauthMsg, s.oauthFail = true, "Menunggu authorize di browser…", false
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		err := s.svc.StartOAuthLogin(ctx, func(email string, err error) {
			win.Update(func() {
				s.oauthBusy = false
				if err != nil {
					s.oauthMsg, s.oauthFail = err.Error(), true
					log.Printf("ui: oauth: %v", err)
					return
				}
				id, aerr := s.svc.AddAccount(context.Background(), app.AccountConfig{
					Email: email, IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: true,
					SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPTLS: true,
					Auth: app.AuthOAuth2,
				})
				if aerr != nil {
					s.oauthMsg, s.oauthFail = aerr.Error(), true
					log.Printf("ui: oauth add akun: %v", aerr)
					return
				}
				s.account = id
				s.showAccount = false
				s.reloadFolders()
				s.refreshNow()
			})
		})
		if err != nil {
			log.Printf("ui: mulai oauth: %v", err)
			win.Update(func() {
				s.oauthBusy = false
				s.oauthMsg, s.oauthFail = err.Error(), true
			})
		}
	}()
}

// row menggambar satu baris daftar pesan.
func (s *state) row(c *ui.Context, i int) {
	m := s.msgs[i]
	t := c.Theme()
	sender, subject := m.Sender, m.Subject
	if sender == "" {
		sender = m.SenderAddr
	}
	weight := 400
	if m.Unread {
		weight = 700
	}
	suffix := ""
	if m.HasAttachment {
		suffix = " 📎"
	}
	ui.Row(c).Padding(8, 10).Gap(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Textf(c, "%s%s", sender, suffix).FontWeight(weight).Grow(0)
			ui.Textf(c, "%s", subject).TextColor(t.TextMuted).FontSize(12)
		})
		if m.Date != "" {
			ui.Textf(c, "%s", shortDate(m.Date)).TextColor(t.TextMuted).FontSize(11)
		}
	})
}

// readingPane menampilkan pesan terpilih sebagai teks bersih. Body yang
// belum ter-cache diambil di goroutine (tidak pernah di main thread).
func (s *state) readingPane(c *ui.Context) {
	t := c.Theme()
	if s.selected >= len(s.msgs) {
		ui.Text(c, "Pilih pesan untuk dibaca").TextColor(t.TextMuted)
		return
	}
	id := s.msgs[s.selected].ID
	m, err := s.svc.PeekMessage(id)
	if err != nil {
		ui.Textf(c, "gagal memuat pesan: %v", err).TextColor(t.TextMuted)
		return
	}
	if !m.Fetched {
		ui.Text(c, "Mengambil isi dari server…").TextColor(t.TextMuted)
		s.fetchBodyLazy(id)
		return
	}
	ui.Textf(c, "%s", m.Subject).Bold().FontSize(18)
	ui.Textf(c, "%s  →  %s", m.Sender, m.To).TextColor(t.TextMuted).FontSize(12)
	ui.Divider(c)
	if m.PlainText == "" {
		ui.Text(c, "(isi pesan kosong)").TextColor(t.TextMuted)
	} else {
		ui.Textf(c, "%s", m.PlainText)
	}
	// Aksi lampiran + fallback HTML (PRD §3.3).
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if len(m.Attachments) > 0 {
			ui.Text(c, "Lampiran:").TextColor(t.TextMuted).FontSize(12)
			for _, a := range m.Attachments {
				if ui.Button(c, a.Filename).Label("Simpan " + a.Filename).Clicked() {
					s.saveAttachment(m.ID, a.Filename)
				}
			}
		}
		if m.RawHTML != "" {
			if ui.Button(c, "Buka di Browser").Clicked() {
				s.openInBrowser(m.ID)
			}
		}
	})
}

// shortcuts memasang navigasi keyboard PRD §3.5.
func (s *state) shortcuts(c *ui.Context) {
	if c.Shortcut(0, ui.KeyDown) || c.Shortcut(0, ui.KeyJ) {
		if s.selected < len(s.msgs)-1 {
			s.selected++
		}
	}
	if c.Shortcut(0, ui.KeyUp) || c.Shortcut(0, ui.KeyK) {
		if s.selected > 0 {
			s.selected--
		}
	}
	if s.composerOpen {
		return
	}
	if c.Shortcut(0, ui.KeyC) {
		s.openComposer("new")
	}
	if c.Shortcut(0, ui.KeyR) && s.selected < len(s.msgs) {
		s.openComposer("reply")
	}
	if c.Shortcut(0, ui.KeyF) && s.selected < len(s.msgs) {
		s.openComposer("forward")
	}
}

// saveLabel mengubah label tombol simpan saat OAuth berjalan.
func (s *state) saveLabel() string {
	if s.oauthBusy {
		return "Menunggu Google…"
	}
	return "Simpan"
}

// shortDate memangkas timestamp ISO menjadi jam:menit singkat.
func shortDate(iso string) string {
	if len(iso) >= 16 {
		return iso[11:16]
	}
	return iso
}

// saveAttachment menanyakan lokasi simpan (dialog native) lalu menulis
// isi lampiran ke disk di goroutine.
func (s *state) saveAttachment(messageID int, filename string) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       "Simpan lampiran",
		DefaultPath: filename,
		ButtonLabel: "Simpan",
	})
	if err != nil || path == "" {
		return
	}
	win := s.win
	go func() {
		data, _, err := s.svc.AttachmentData(messageID, filename)
		if err == nil {
			err = os.WriteFile(path, data, 0o600)
		}
		win.Update(func() {
			if err != nil {
				log.Printf("ui: simpan lampiran: %v", err)
			}
		})
	}()
}

// openInBrowser menyimpan HTML mentah ke file sementara (goroutine) lalu
// membukanya di browser default OS — jangan blokir main thread.
func (s *state) openInBrowser(messageID int) {
	win := s.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		path, err := s.svc.SaveHTMLForBrowser(ctx, messageID)
		if err == nil {
			err = mygo.Shell.OpenPath(path)
		}
		win.Update(func() {
			if err != nil {
				log.Printf("ui: buka di browser: %v", err)
			}
		})
	}()
}
