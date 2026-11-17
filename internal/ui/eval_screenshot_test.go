package ui

// Harness evaluasi visual headless: render view Karta ke PNG di /tmp.
// Jalan manual: KARTA_UI_EVAL=1 go test ./internal/ui -run TestEvalRender -v
// Tidak berjalan di CI biasa.

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func savePNG(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	img := tt.Image()
	if img == nil {
		t.Fatalf("%s: Image() nil", name)
	}
	f, err := os.Create("/tmp/karta-ui-" + name + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("simpan /tmp/karta-ui-%s.png", name)
}

func TestEvalRender(t *testing.T) {
	if os.Getenv("KARTA_UI_EVAL") == "" {
		t.Skip("set KARTA_UI_EVAL=1 untuk merender PNG")
	}
	st := &state{svc: testService(t), fetching: map[int]bool{}}
	st.reloadFolders()

	// 1. View utama 3-pane, light.
	tt := ui.NewTester(st.view, 1100, 720)
	savePNG(t, tt, "main-light")

	// 2. Dark.
	tt2 := ui.NewTester(st.view, 1100, 720)
	tt2.SetDark(true)
	savePNG(t, tt2, "main-dark")

	// 3. Form tambah akun.
	st.showAccount = true
	tt.Frame()
	savePNG(t, tt, "account-form")

	// 4. Composer.
	st.showAccount = false
	st.openComposer("new")
	tt.Frame()
	savePNG(t, tt, "composer")

	// 5. Form akun sempit (320px) — responsivitas.
	st.composerOpen = false
	st.showAccount = true
	tt.SetSize(320, 560)
	tt.Frame()
	savePNG(t, tt, "account-form-narrow")

	// Audit aksesibilitas: hitung elemen tanpa nama pada kontrol interaktif.
	audit := 0
	for _, a := range tt.AccessAll() {
		if a.Name == "" {
			audit++
			t.Logf("tanpa nama: role=%v", a.Role)
		}
	}
	t.Logf("elemen aksesibilitas tanpa nama: %d dari %d", audit, len(tt.AccessAll()))
}
