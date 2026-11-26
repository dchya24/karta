// Package ui membangun antarmuka native mygo/ui Karta: layout 3-pane
// (sidebar, daftar pesan, reading pane) plus form tambah akun.
// Semua kode di sini berjalan di main thread; pekerjaan jaringan lewat
// goroutine yang melaporkan hasil via mygo Window.Update.
package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	kartaicons "karta/assets/icons"
	"karta/internal/app"
	"karta/internal/sync"
)

// state adalah kondisi UI: dibaca dan diubah hanya di main thread (view dan
// callback window), diperbarui dari goroutine lewat Window.Update.
// accFolders adalah satu bagian sidebar: satu akun + folder IMAP-nya.
type accFolders struct {
	acc     app.Account
	folders []app.Folder
}

// sectionKey adalah id unik item sidebar "<accountID>:<folderID>".
func sectionKey(f app.Folder) string {
	return strconv.Itoa(f.AccountID) + ":" + strconv.Itoa(f.ID)
}

// folderLabel menggeneralisasi nama folder di sidebar berdasarkan peran
// standar (Inbox, Sent, Drafts, Archive, Spam, Trash) dan memangkas prefix
// namespace server seperti "[Gmail]/" atau "[Google Mail]/".
func folderLabel(f app.Folder) string {
	switch f.Role {
	case "inbox":
		return "Inbox"
	case "sent":
		return "Sent"
	case "drafts":
		return "Drafts"
	case "archive":
		return "Archive"
	case "junk":
		return "Spam"
	case "trash":
		return "Trash"
	}
	name := f.Name
	for _, p := range []string{"[Gmail]/", "[Google Mail]/", "INBOX.", "INBOX/"} {
		if len(name) > len(p) && strings.EqualFold(name[:len(p)], p) {
			return name[len(p):]
		}
	}
	return name
}

type state struct {
	svc       *app.Service
	win       *mygo.Window
	sections  []accFolders // satu bagian per akun, urut email
	folderAcc map[int]int  // folderID → accountID
	account   int          // akun terpilih
	folder    int          // folder terpilih (ID)
	folderSel string       // pilihan sidebar (sectionKey)
	query     string
	searchRef ui.Handle
	list      ui.ListState
	msgs      []app.MessageHeader
	selected  int

	// cache dokumen reading pane (richtext read-only dengan tautan klik)
	readerID      int
	readerText    string
	readerDoc     *richtext.Document
	pendingToasts []ui.Toast

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

	// pesan yang body-nya sedang diambil lazy (ID → berlangsung) & error per pesan
	fetching     map[int]bool
	fetchErr     map[int]string
	reloginEmail string
	lastSyncErr  string // teks error sinkron latar terakhir, agar toast tidak dobel
	oauthFlow    int    // penomoran flow OAuth; klik ulang membatalkan flow lama
	oauthCancel  context.CancelFunc

	// sinkronisasi manual
	syncBusy bool
	syncMsg  string
	syncFail bool

	// tema; nil = ikut penampilan sistem
	theme *ui.Theme

	// modal konfirmasi hapus akun (native mygo/ui, bukan dialog OS)
	delAcc     app.Account // akun yang menunggu konfirmasi
	delAccOpen bool

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
	st := &state{svc: svc, fetching: map[int]bool{}, fetchErr: map[int]string{}}
	switch loadPrefs().Appearance {
	case "light":
		st.theme = kartaTheme(false)
	case "dark":
		st.theme = kartaTheme(true)
	}
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
// startSync menjalankan worker polling di goroutine; tiap siklus memicu
// repaint lewat Window.Update (main thread). Error sinkronisasi tampil
// sebagai toast — dedup sampai ada siklus yang sukses.
func (s *state) startSync(ctx context.Context) {
	win := s.win
	go sync.NewWorker(s.svc, 5*time.Minute, func(err error) {
		win.Update(func() {
			if err != nil {
				if msg := err.Error(); msg != s.lastSyncErr {
					s.lastSyncErr = msg
					s.notify("Sinkronisasi gagal: "+msg, "error")
				}
			} else {
				s.lastSyncErr = ""
			}
			s.reloadFolders()
		})
	}).Run(ctx)
}

// refreshNow menjalankan sinkronisasi segera di goroutine, dengan
// guard anti dobel-klik dan laporan status/error di sidebar.
func (s *state) refreshNow() {
	if s.syncBusy {
		return
	}
	s.syncBusy = true
	win := s.win
	go func() {
		err := s.svc.Refresh(context.Background())
		win.Update(func() {
			s.syncBusy = false
			s.reloadFolders()
			if err != nil {
				s.syncMsg, s.syncFail = err.Error(), true
				s.handleErr("", err)
				return
			}
			s.lastSyncErr = ""
			s.syncMsg, s.syncFail = "Sinkron "+time.Now().Format("15:04:05"), false
		})
	}()
}

// reloadFolders memuat folder akun dan pesan folder terpilih. Main thread saja.
func (s *state) reloadFolders() {
	accs, err := s.svc.ListAccounts()
	if err != nil {
		log.Printf("ui: akun: %v", err)
		return
	}
	s.sections = nil
	s.folderAcc = map[int]int{}
	for _, a := range accs {
		fs, err := s.svc.ListFolders(a.ID)
		if err != nil {
			log.Printf("ui: folder %s: %v", a.Email, err)
			continue
		}
		s.sections = append(s.sections, accFolders{acc: a, folders: fs})
		for _, f := range fs {
			s.folderAcc[f.ID] = a.ID
		}
	}
	// Pertahankan seleksi lama kalau foldernya masih ada.
	if s.folder != 0 {
		if _, ok := s.folderAcc[s.folder]; !ok {
			s.folder = 0
		}
	}
	if s.folder == 0 {
		s.folder, s.folderSel = 0, ""
		for _, sec := range s.sections {
			if len(sec.folders) > 0 {
				f := sec.folders[0]
				s.account, s.folder, s.folderSel = f.AccountID, f.ID, sectionKey(f)
				break
			}
		}
	}
	s.reloadMessages()
}

// reloadMessages memuat daftar percakapan folder terpilih + filter query.
// Tanpa query: satu baris per thread (pesan terbarunya) dengan jumlah anggota.
// Dengan query: hasil pencarian flat per pesan (ListThreads delegasi FTS).
func (s *state) reloadMessages() {
	msgs, err := s.svc.ListThreads(s.folder, s.query)
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
		s.notify("Gagal simpan akun: "+err.Error(), "error")
		return
	}
	s.account = id
	s.showAccount = false
	s.notify("Akun ditambahkan", "success")
	s.reloadFolders()
	s.refreshNow() // sinkron pertama langsung
}

// toggleTheme mengalihkan tema terang/gelap dan menyimpan pilihannya.
func (s *state) toggleTheme(dark bool) {
	if dark {
		s.theme = kartaTheme(false)
		savePrefs(prefs{Appearance: "light"})
		return
	}
	s.theme = kartaTheme(true)
	savePrefs(prefs{Appearance: "dark"})
}

// themeIcon ikon tombol tema: saat gelap tawarkan terang, dan sebaliknya.
func themeIcon(dark bool) string {
	if dark {
		return "☀"
	}
	return "🌙"
}

// switchTo memilih folder pertama akun sebagai folder aktif. Main thread saja.
func (s *state) switchTo(accID int) {
	for _, sec := range s.sections {
		if sec.acc.ID != accID || len(sec.folders) == 0 {
			continue
		}
		f := sec.folders[0]
		s.account, s.folder, s.folderSel = accID, f.ID, sectionKey(f)
		s.reloadMessages()
		return
	}
}

// deleteAccount membuka modal konfirmasi hapus akun (native mygo/ui,
// bukan dialog OS). Eksekusi hapus di confirmDeleteAccount.
func (s *state) deleteAccount(acc app.Account) {
	s.delAcc, s.delAccOpen = acc, true
}

// confirmDeleteAccount menghapus akun setelah user konfirmasi di modal
// (pesan tersinkron ikut terhapus dari perangkat; kredensial keyring dibersihkan).
func (s *state) confirmDeleteAccount() {
	acc := s.delAcc
	s.delAcc, s.delAccOpen = app.Account{}, false
	if err := s.svc.RemoveAccount(context.Background(), acc.ID); err != nil {
		log.Printf("ui: hapus akun %s: %v", acc.Email, err)
		s.notify("Gagal hapus akun: "+err.Error(), "error")
		return
	}
	s.account, s.folder, s.folderSel = 0, 0, ""
	s.notify("Akun dihapus", "info")
	s.reloadFolders()
}

// fetchBodyLazy mengambil isi pesan di goroutine bila belum ter-cache.
// Kegagalan dicatat di s.fetchErr[id] agar frame berikutnya tidak memicu
// retry tak berujung saat token OAuth kedaluwarsa atau jaringan putus.
func (s *state) fetchBodyLazy(id int) {
	if s.fetching == nil {
		s.fetching = map[int]bool{}
	}
	if s.fetchErr == nil {
		s.fetchErr = map[int]string{}
	}
	if s.fetching[id] {
		return
	}
	s.fetching[id] = true
	delete(s.fetchErr, id)
	win := s.win
	if win == nil {
		_, err := s.svc.GetMessage(context.Background(), id)
		delete(s.fetching, id)
		if err != nil {
			s.fetchErr[id] = err.Error()
			s.handleErr("Gagal ambil isi pesan: ", err)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, err := s.svc.GetMessage(ctx, id)
		win.Update(func() {
			delete(s.fetching, id)
			if err != nil {
				log.Printf("ui: fetch body pesan %d: %v", id, err)
				s.fetchErr[id] = err.Error()
				s.handleErr("Gagal ambil isi pesan: ", err)
			}
		})
	}()
}

// notify mengantrekan toast notifikasi untuk ditampilkan di frame berikutnya.
func (s *state) notify(title, kind string) {
	s.pendingToasts = append(s.pendingToasts, ui.Toast{
		Title: title,
		Type:  kind,
	})
}

// handleErr mendeteksi OAuthReloginError untuk menampilkan toast aksi
// "Login ulang" + banner re-auth; error lain ditampilkan sebagai toast biasa.
func (s *state) handleErr(prefix string, err error) {
	var re *app.OAuthReloginError
	if errors.As(err, &re) {
		s.reloginEmail = re.Email
		s.pendingToasts = append(s.pendingToasts, ui.Toast{
			ID:       "relogin-" + re.Email,
			Title:    re.Error(),
			Type:     "error",
			Action:   "Login ulang",
			OnAction: func() { s.loginGoogle() },
		})
		return
	}
	s.notify(prefix+err.Error(), "error")
}

// view adalah builder UI native; berjalan tiap frame di main thread.
func (s *state) view(c *ui.Context) {
	// Tema brand Karta menggantikan tema sistem sebelum widget apa pun
	// dibangun; s.theme nil berarti mengikuti penampilan sistem.
	if s.theme != nil {
		c.SetTheme(s.theme)
	} else {
		c.SetTheme(kartaTheme(c.Theme().Dark))
	}
	t := c.Theme()
	for _, toast := range s.pendingToasts {
		c.AddToast(toast)
	}
	s.pendingToasts = s.pendingToasts[:0]
	s.shortcuts(c)

	if s.composerOpen {
		ui.Column(c).Fill().Padding(24, 32).Gap(10).Children(func() {
			s.composerForm(c)
		})
		return
	}

	// Form akun: layar penuh tanpa sidebar (responsif di lebar kecil).
	if s.showAccount {
		ui.Column(c).Fill().Padding(24, 32).Gap(10).Children(func() {
			s.accountForm(c)
		})
		return
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// Pane 1: sidebar akun + folder + unread.
		ui.Column(c).Width(240).Shrink(0).PaddingY(12).Gap(6).Background(t.Surface).Children(func() {
			ui.Row(c).Padding(0, 16).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Image(c, kartaLogo).Size(28, 28)
				ui.Text(c, "Karta").Bold().Grow(1)
				if ui.Button(c, themeIcon(t.Dark)).Label("Ganti tema terang/gelap").Clicked() {
					s.toggleTheme(t.Dark)
				}
				ui.MenuButton(c, "⋯", func(m *ui.Menu) {
					if m.Item("Tambah akun…").Chosen() {
						s.showAccount = true
					}
					m.Separator()
					for _, sec := range s.sections {
						acc := sec.acc
						if m.Item(acc.Email).Radio(acc.ID == s.account).Chosen() {
							s.switchTo(acc.ID)
						}
					}
					m.Separator()
					for _, sec := range s.sections {
						acc := sec.acc
						if m.Item("Hapus " + acc.Email).Danger().Chosen() {
							s.deleteAccount(acc)
						}
					}
				}).Label("Kelola akun")
			})
			if len(s.sections) > 0 {
				if ui.Sidebar(c, &s.folderSel, func() {
					for _, sec := range s.sections {
						acc, folders := sec.acc, sec.folders
						ui.SidebarSection(c, acc.Email, nil, func() {
							for _, f := range folders {
								ui.SidebarItem(c, sectionKey(f), nil, folderLabel(f)).Children(func() {
									if f.Unread > 0 {
										ui.Badge(c, strconv.Itoa(f.Unread))
									}
								})
							}
						})
					}
				}).Grow(1).Label("Akun").Changed() {
					var fID int
					if _, err := fmt.Sscanf(s.folderSel, "%d:%d", &s.account, &fID); err == nil && fID > 0 {
						s.folder = fID
						s.reloadMessages()
					}
				}
			} else {
				ui.Text(c, "Belum ada akun.\nBuka menu ⋯ untuk menambah.").TextColor(t.TextMuted).Padding(16)
			}
			if s.reloginEmail != "" {
				ui.Textf(c, "Sesi %s habis", s.reloginEmail).FontSize(11).PaddingX(10).TextColor(t.Danger)
				if ui.Button(c, "Login ulang Google").Label("Login ulang " + s.reloginEmail).MarginX(10).Clicked() {
					s.loginGoogle()
				}
			}
			syncLabel := "Sinkronkan"
			if s.syncBusy {
				syncLabel = "Menyinkronkan…"
			}
			if ui.Button(c, syncLabel).Label("Sinkronkan sekarang").MarginX(10).Clicked() {
				s.refreshNow()
			}
			if s.syncMsg != "" {
				msg := ui.Textf(c, "%s", s.syncMsg).FontSize(11).PaddingX(10)
				if s.syncFail {
					msg.TextColor(t.Danger)
				} else {
					msg.TextColor(t.TextMuted)
				}
			}
		})

		// Pane 2+3: daftar pesan | reading pane.
		var splitSize float32 = 380
		ui.Split(c, &splitSize, func() {
			ui.Column(c).Fill().Gap(0).Children(func() {
				ui.Row(c).Padding(8, 10).Gap(6).AlignItems(ui.Center).Children(func() {
					if ui.TextInput(c.Key("a-query"), &s.query).Bind(&s.searchRef).Grow(1).
						Placeholder("Cari… (/)").Label("Cari").Changed() {
						s.reloadMessages()
					}
					if ui.Button(c, "Tulis").Label("Tulis email baru").Clicked() {
						s.openComposer("new")
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

	s.deleteAccountModal(c)
}

// deleteAccountModal menampilkan konfirmasi hapus akun sebagai modal native
// mygo/ui (tema ikut aplikasi), bukan dialog bawaan OS.
func (s *state) deleteAccountModal(c *ui.Context) {
	if !s.delAccOpen {
		s.delAcc = app.Account{}
		return
	}
	t := c.Theme()
	ui.Modal(c, &s.delAccOpen, func() {
		acc := s.delAcc
		ui.Column(c).Width(420).Padding(20, 24).Gap(12).Children(func() {
			ui.Text(c, "Hapus akun").Bold().FontSize(16)
			ui.Textf(c, "Hapus akun %s?", acc.Email).FontSize(13)
			ui.Text(c, "Folder dan pesan yang tersinkron dihapus dari perangkat ini; "+
				"isi kotak surat tetap aman di server. Kredensial di Keyring ikut dihapus.").
				TextColor(t.TextMuted).FontSize(12)
			ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
				if ui.Button(c, "Batal").Clicked() {
					s.delAcc, s.delAccOpen = app.Account{}, false
				}
				if ui.Button(c, "Hapus").Label("Hapus akun " + acc.Email).Clicked() {
					s.confirmDeleteAccount()
				}
			})
		})
	})
}

// accountForm menggambar form tambah akun: input manual + tombol simpan,
// plus tombol Login via Google yang langsung memicu flow OAuth.
func (s *state) accountForm(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Tambah Akun").Bold().FontSize(18)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Email").Width(90)
		ui.TextInput(c.Key("a-email"), &s.fEmail).Grow(1).Placeholder("nama@contoh.id").Label("Email")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Password").Width(90)
		ui.TextInput(c.Key("a-pass"), &s.fPass).Grow(1).Password().Placeholder("App password").Label("Password")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "IMAP host").Width(90)
		ui.TextInput(c.Key("a-imap"), &s.fIMAPHost).Grow(1).Placeholder("imap.contoh.id").Label("IMAP host")
		ui.TextInput(c.Key("a-iport"), &s.fIMAPPort).Width(64).Placeholder("993").Label("IMAP port")
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "SMTP host").Width(90)
		ui.TextInput(c.Key("a-smtp"), &s.fSMTPHost).Grow(1).Placeholder("smtp.contoh.id").Label("SMTP host")
		ui.TextInput(c.Key("a-sport"), &s.fSMTPPort).Width(64).Placeholder("465").Label("SMTP port")
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
	ui.Row(c).Gap(12).Wrap().Children(func() {
		if ui.Button(c, s.saveLabel()).Clicked() && !s.oauthBusy {
			s.saveAccount()
		}
		if ui.Button(c, "Login via Google").Label("Login via Google (OAuth2)").Clicked() {
			s.loginGoogle()
		}
		if ui.Button(c, "Batal").Clicked() {
			s.showAccount = false
		}
	})
}

// loginGoogle memicu flow OAuth dari satu klik — tanpa input email:
// email akun dibaca otomatis dari id_token Google, lalu akun Gmail dibuat.
// Dipanggil ulang saat flow masih berjalan (mis. halaman authorize ditutup
// tanpa selesai) = flow lama dibatalkan dan flow baru dimulai.
// Catatan: StartOAuthLogin asinkron — cancel() tidak boleh defer, harus
// dipanggil saat flow selesai (di callback), bukan saat goroutine kembali.
func (s *state) loginGoogle() {
	win := s.win
	if s.oauthCancel != nil {
		s.oauthCancel() // flow lama berhenti; callback usang diabaikan lewat oauthFlow
		s.oauthCancel = nil
	}
	s.oauthFlow++
	flow := s.oauthFlow
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	s.oauthCancel = cancel
	s.oauthBusy, s.oauthMsg, s.oauthFail = true, "Menunggu authorize di browser…", false
	go func() {
		err := s.svc.StartOAuthLogin(ctx, func(email string, err error) {
			cancel() // flow beres (sukses/gagal) — matikan server callback
			win.Update(func() {
				if flow != s.oauthFlow {
					return // callback flow lama yang sudah dibatalkan
				}
				s.oauthCancel, s.oauthBusy = nil, false
				if err != nil {
					if ctx.Err() != nil {
						return // dibatalkan user (klik ulang)
					}
					s.oauthMsg, s.oauthFail = err.Error(), true
					log.Printf("ui: oauth: %v", err)
					return
				}
				id, aerr := s.svc.EnsureGmailAccount(email)
				if aerr != nil {
					s.oauthMsg, s.oauthFail = aerr.Error(), true
					log.Printf("ui: oauth add akun: %v", aerr)
					return
				}
				s.account = id
				s.showAccount = false
				s.reloginEmail = ""
				clear(s.fetchErr)
				s.notify("Login Google "+email+" berhasil", "success")
				s.reloadFolders()
				s.refreshNow()
			})
		})
		if err != nil {
			cancel()
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
	chosen := i == s.selected
	suffix := ""
	if m.HasAttachment {
		suffix = " 📎"
	}
	ui.Row(c).Padding(8, 10).Gap(8).Children(func() {
		// Dot penanda belum dibaca; bold saja kurang cepat discan.
		if m.Unread {
			ui.Textf(c, "●").FontSize(8).TextColor(t.Accent)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			senderText := ui.Textf(c, "%s%s", sender, suffix).FontWeight(weight).Grow(0)
			subjectText := ui.Textf(c, "%s", subject).FontSize(12)
			if chosen {
				// Di atas latar seleksi: teks terang, bukan muted.
				senderText.TextColor(t.AccentText)
				subjectText.TextColor(t.AccentText)
			} else {
				subjectText.TextColor(t.TextMuted)
			}
		})
		// Baris thread: badge jumlah anggota percakapan (baris = pesan
		// terbarunya, jadi tanggal baris otomatis pesan terbaru).
		if m.ThreadCount > 1 {
			badge := ui.Badge(c, strconv.Itoa(m.ThreadCount))
			if chosen {
				badge.TextColor(t.AccentText)
			}
		}
		if m.Date != "" {
			dateText := ui.Textf(c, "%s", shortDate(m.Date)).FontSize(11).Shrink(0)
			if chosen {
				dateText.TextColor(t.AccentText)
			} else {
				dateText.TextColor(t.TextMuted)
			}
		}
	})
}

// readingPane menampilkan pesan terpilih sebagai teks bersih dengan tautan
// yang dapat diklik. Body yang belum ter-cache diambil di goroutine.
func (s *state) readingPane(c *ui.Context) {
	t := c.Theme()
	if s.selected >= len(s.msgs) {
		ui.Text(c, "Pilih pesan untuk dibaca").TextColor(t.TextMuted)
		return
	}
	wasUnread := s.msgs[s.selected].Unread
	id := s.msgs[s.selected].ID
	m, err := s.svc.PeekMessage(id)
	if err != nil {
		ui.Textf(c, "gagal memuat pesan: %v", err).TextColor(t.TextMuted)
		return
	}
	if wasUnread && !m.Unread {
		s.msgs[s.selected].Unread = false
		s.adjustFolderUnread(m.FolderID, -1)
		s.syncSeenAsync(m.ID, true)
	}
	if !m.Fetched {
		if errMsg, failed := s.fetchErr[id]; failed {
			ui.Textf(c, "%s", m.Subject).Bold().FontSize(18)
			ui.Textf(c, "Gagal memuat isi pesan: %s", errMsg).TextColor(t.Danger)
			ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				if s.reloginEmail != "" {
					if ui.Button(c, "Login ulang Google").Clicked() {
						s.loginGoogle()
					}
				}
				if ui.Button(c, "Coba lagi").Clicked() {
					delete(s.fetchErr, id)
					s.fetchBodyLazy(id)
				}
			})
			return
		}
		ui.Text(c, "Mengambil isi dari server…").TextColor(t.TextMuted)
		s.fetchBodyLazy(id)
		return
	}
	ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "Balas").Clicked() {
			s.openComposer("reply")
		}
		if ui.Button(c, "Balas Semua").Clicked() {
			s.openComposer("reply_all")
		}
		if ui.Button(c, "Teruskan").Clicked() {
			s.openComposer("forward")
		}
		readLabel := "Belum dibaca"
		if m.Unread {
			readLabel = "Sudah dibaca"
		}
		if ui.Button(c, readLabel).Label("Tandai " + readLabel).Clicked() {
			s.toggleRead(m.ID, m.Unread)
		}
		if ui.Button(c, "Arsip").Clicked() {
			s.archiveMessage(m.ID)
		}
		if ui.Button(c, "Hapus").Clicked() {
			s.deleteMessage(m.ID)
		}
	})
	ui.Textf(c, "%s", m.Subject).Bold().FontSize(18)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Textf(c, "Dari: %s", m.Sender).TextColor(t.TextMuted).FontSize(12).Grow(1)
		if m.Date != "" {
			ui.Textf(c, "%s", formatFullDate(m.Date)).TextColor(t.TextMuted).FontSize(12).Shrink(0)
		}
	})
	ui.Textf(c, "Kepada: %s", m.To).TextColor(t.TextMuted).FontSize(12)
	if m.Cc != "" {
		ui.Textf(c, "Cc: %s", m.Cc).TextColor(t.TextMuted).FontSize(12)
	}
	ui.Divider(c)
	if m.PlainText == "" {
		ui.Text(c, "(isi pesan kosong)").TextColor(t.TextMuted).Grow(1)
	} else {
		doc := s.readerDocument(m.ID, m.PlainText)
		// Bungkus editor dengan Box Clip: painter clip mygo tak membatasi
		// Draw editor sendiri, jadi isi tergulir bisa meluber menimpa header.
		ui.Box(c).Grow(1).Clip().Children(func() {
			richtext.Editor(c.Key("reader-body"), doc, richtext.EditorOptions{
				Label:      "Isi pesan",
				ReadOnly:   true,
				NoMarkdown: true,
				OnLink:     s.openLink,
			}).Fill()
		})
	}
	// Aksi lampiran + fallback HTML (PRD §3.3).
	ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
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

func (s *state) readerDocument(id int, plain string) *richtext.Document {
	if s.readerDoc == nil || s.readerID != id || s.readerText != plain {
		s.readerID = id
		s.readerText = plain
		s.readerDoc = richtext.NewDocument(linkifyText(plain))
		// NewDocument meletakkan caret di akhir teks; reveal bawaan editor
		// lalu menggulir ke caret itu — baca pesan harus mulai dari atas.
		s.readerDoc.SetSelection(0, 0)
	}
	return s.readerDoc
}

func (s *state) openLink(raw string) {
	if strings.HasPrefix(strings.ToLower(raw), "mailto:") {
		s.openMailto(raw)
		return
	}
	win := s.win
	go func() {
		if err := mygo.Shell.OpenExternal(raw); err != nil {
			log.Printf("ui: buka tautan %s: %v", raw, err)
			if win != nil {
				win.Update(func() { s.notify("Gagal buka tautan: "+err.Error(), "error") })
			}
		}
	}()
}

// runAction menjalankan operasi pesan di goroutine lalu memperbarui UI di main
// thread via Window.Update; pada mode uji headless (win == nil) berjalan langsung.
// Kegagalan sync best-effort (SyncWarning) tampil sebagai toast peringatan:
// aksi lokal berhasil, hanya server yang tertinggal.
func (s *state) runAction(okToast string, op func(context.Context) error) {
	done := func(err error) {
		switch {
		case err == nil:
			if okToast != "" {
				s.notify(okToast, "success")
			}
		case app.IsSyncWarning(err):
			s.notify(err.Error(), "warning")
		default:
			s.handleErr("", err)
		}
		s.reloadFolders()
	}
	win := s.win
	if win == nil {
		done(op(context.Background()))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := op(ctx)
		win.Update(func() { done(err) })
	}()
}

func (s *state) toggleRead(id int, currentlyUnread bool) {
	s.runAction("", func(ctx context.Context) error {
		return s.svc.MarkRead(ctx, id, currentlyUnread)
	})
}

func (s *state) archiveMessage(id int) {
	s.runAction("Pesan diarsipkan", func(ctx context.Context) error {
		return s.svc.Archive(ctx, id)
	})
}

func (s *state) deleteMessage(id int) {
	s.runAction("Pesan dihapus", func(ctx context.Context) error {
		return s.svc.Delete(ctx, id)
	})
}

func (s *state) syncSeenAsync(id int, read bool) {
	if s.win == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = s.svc.MarkRead(ctx, id, read)
	}()
}

func (s *state) adjustFolderUnread(folderID, delta int) {
	for i := range s.sections {
		for j := range s.sections[i].folders {
			if s.sections[i].folders[j].ID == folderID {
				s.sections[i].folders[j].Unread = max(0, s.sections[i].folders[j].Unread+delta)
				return
			}
		}
	}
}

// shortcuts memasang navigasi keyboard PRD §3.5.
func (s *state) shortcuts(c *ui.Context) {
	if s.composerOpen || s.showAccount || c.FocusTakesText() {
		return
	}
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
	if c.Shortcut(0, ui.KeySlash) {
		s.searchRef.SelectAll(c)
		return
	}
	if c.Shortcut(0, ui.KeyC) {
		s.openComposer("new")
		return
	}
	if s.selected >= len(s.msgs) {
		return
	}
	if c.Shortcut(ui.Shift, ui.KeyR) {
		s.openComposer("reply_all")
	} else if c.Shortcut(0, ui.KeyR) {
		s.openComposer("reply")
	}
	if c.Shortcut(0, ui.KeyF) {
		s.openComposer("forward")
	}
	if c.Shortcut(0, ui.KeyE) {
		s.archiveMessage(s.msgs[s.selected].ID)
	}
	if c.Shortcut(0, ui.KeyU) {
		m := s.msgs[s.selected]
		s.toggleRead(m.ID, m.Unread)
	}
	if c.Shortcut(0, ui.KeyDelete) {
		s.deleteMessage(s.msgs[s.selected].ID)
	}
}

// formatFullDate menampilkan timestamp ISO menjadi "YYYY-MM-DD HH:MM".
func formatFullDate(iso string) string {
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return iso
}

// saveLabel mengubah label tombol simpan saat OAuth berjalan.
func (s *state) saveLabel() string {
	if s.oauthBusy {
		return "Menunggu Google…"
	}
	return "Simpan"
}

// shortDate memangkas timestamp ISO menjadi jam:menit singkat.
// shortDate memformat timestamp ISO menjadi jam:menit lokal.
func shortDate(iso string) string {
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.Local().Format("15:04")
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
				s.notify("Gagal simpan lampiran: "+err.Error(), "error")
				return
			}
			s.notify("Lampiran tersimpan", "success")
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
				s.notify("Gagal buka di browser: "+err.Error(), "error")
			}
		})
	}()
}
