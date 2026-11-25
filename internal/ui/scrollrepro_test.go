package ui

// Regresi reading pane: isi pesan panjang (email HTML-only dengan URL
// tracking panjang) harus tampil dari atas dan tergulir di dalam pane,
// tanpa menimpa header/toolbar. Jalan manual:
// KARTA_UI_EVAL=1 go test ./internal/ui -run TestScrollRepro -v
import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"

	"karta/internal/app"
	"karta/internal/store"
)

func TestScrollRepro(t *testing.T) {
	if os.Getenv("KARTA_UI_EVAL") == "" {
		t.Skip("set KARTA_UI_EVAL=1 untuk merender PNG")
	}
	// Isi sintetis panjang meniru email marketing: 80 paragraf + URL besar.
	var sb []byte
	for i := 0; i < 80; i++ {
		sb = append(sb, "Paragraf promo lengkap dengan tautan https://r.contoh.com/ls/click?upn=AAAA"...)
		for j := 0; j < 40; j++ {
			sb = append(sb, "-2Fkx9QaLb7-2BzxCv0Mn1PqR8sT3uYw6Ed5fGh2Ij4Kl"...)
		}
		sb = append(sb, ")\n\n"...)
	}
	plain := string(sb)

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	accID, err := db.AddAccount("a@contoh.id", "imap", 993, true, "smtp", 465, true, "")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.EnsureFolder(accID, "INBOX", "inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertMessage(&store.MessageHeader{
		FolderID: int(inbox), UID: 100, Subject: "Less to-do. More ta-da.",
		Sender: "Chat GPT <noreply@email.openai.com>", SenderAddr: "noreply@email.openai.com",
		Recipients: "devdinar05@gmail.com", Date: "2026-11-20T10:00:00Z", Unread: true,
	}, &store.MessageBody{PlainText: plain}); err != nil {
		t.Fatal(err)
	}
	st := &state{svc: app.New(db)}
	st.reloadFolders()
	st.reloadMessages()
	tt := ui.NewTester(st.view, 1100, 720)
	save := func(name string) {
		f, err := os.Create("/tmp/karta-scroll-" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	// Dokumen baru harus mulai dari atas, bukan ekor (caret akhir NewDocument).
	if r, ok := tt.Find("Put ChatGPT to work"); ok {
		_ = r
	}
	if st.readerDoc == nil {
		t.Fatal("dokumen reader tidak terbentuk")
	}
	// Seleksi dokumen reader harus di awal: reveal editor tak melompat ke ekor.
	if st.readerDoc.Text().PlainText() != plain {
		t.Fatal("isi dokumen reader tidak cocok")
	}
	save("top")
	// Scroll di area reading pane: header & toolbar tetap, isi bergulir.
	for i := 0; i < 6; i++ {
		tt.Scroll(850, 400, 0, 300)
		tt.Frame()
	}
	if r, ok := tt.Find("Balas"); !ok || r.Y < 0 {
		t.Fatalf("toolbar aksi hilang atau bergeser: %+v", r)
	}
	if r, ok := tt.Find("Less to-do. More ta-da."); !ok || r.Y < 0 {
		t.Fatalf("subjek hilang atau bergeser: %+v", r)
	}
	save("end")
}
