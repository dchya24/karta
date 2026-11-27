package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// newBulkState membangun state UI headless di atas service test (2 pesan di
// INBOX) dengan frame pertama sudah dirender.
func newBulkState(t *testing.T) (*state, *ui.Tester) {
	t.Helper()
	st := &state{svc: testService(t), fetching: map[int]bool{}, fetchErr: map[int]string{}}
	st.reloadFolders()
	tt := ui.NewTester(st.view, 1100, 720)
	tt.Frame()
	return st, tt
}

// folderIDByRole mencari ID folder berdasarkan peran dari sidebar state.
func folderIDByRole(st *state, role string) int {
	for _, sec := range st.sections {
		for _, f := range sec.folders {
			if f.Role == role {
				return f.ID
			}
		}
	}
	return 0
}

// TestBulkToggleAndClear: x menandai/menandai ulang pesan terpilih, hint
// aksi bulk tampil sesuai jumlah, Escape kosongkan, dan pindah folder /
// reload daftar juga mengosongkan set pilihan.
func TestBulkToggleAndClear(t *testing.T) {
	st, tt := newBulkState(t)

	if tt.HasText("terpilih:") {
		t.Fatal("hint bulk tidak boleh tampil saat set kosong")
	}
	// x pada pesan pertama → 1 terpilih.
	tt.Key(0, ui.KeyX)
	tt.Frame()
	if st.bulkCount() != 1 || !st.bulkSel[st.msgs[0].ID] {
		t.Fatalf("set pilihan salah: %v", st.bulkSel)
	}
	if !tt.HasText("1 terpilih: [A]rsip  [D]elete  [x]Batal") {
		t.Fatal("hint bulk 1 terpilih tidak tampil")
	}
	// x lagi pada pesan yang sama → batal pilih (toggle).
	tt.Key(0, ui.KeyX)
	tt.Frame()
	if st.bulkCount() != 0 {
		t.Fatalf("toggle harus menghapus dari set: %v", st.bulkSel)
	}
	// Pilih dua pesan: x, j, x.
	tt.Key(0, ui.KeyX)
	tt.Key(0, ui.KeyJ)
	tt.Key(0, ui.KeyX)
	tt.Frame()
	if st.bulkCount() != 2 {
		t.Fatalf("harus 2 terpilih: %v", st.bulkSel)
	}
	if !tt.HasText("2 terpilih") {
		t.Fatal("hint bulk 2 terpilih tidak tampil")
	}
	// Escape kosongkan set.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if st.bulkCount() != 0 {
		t.Fatalf("Escape harus kosongkan set: %v", st.bulkSel)
	}

	// Reload daftar (juga terjadi saat pindah folder) mengosongkan set.
	st.toggleBulk()
	if st.bulkCount() != 1 {
		t.Fatalf("persiapan gagal: %v", st.bulkSel)
	}
	st.folder = folderIDByRole(st, "archive")
	st.reloadMessages()
	if st.bulkCount() != 0 {
		t.Fatalf("reload daftar harus kosongkan set: %v", st.bulkSel)
	}
}

// TestBulkArchiveShortcut: Shift+A mengarsipkan semua pesan terpilih lewat
// Service.ArchiveMany (bukan loop di UI), menampilkan toast ringkasan,
// lalu mengosongkan set dan memuat ulang daftar.
func TestBulkArchiveShortcut(t *testing.T) {
	st, tt := newBulkState(t)
	archiveID := folderIDByRole(st, "archive")
	inboxID := folderIDByRole(st, "inbox")

	// Pilih kedua pesan lalu arsip bulk.
	tt.Key(0, ui.KeyX)
	tt.Key(0, ui.KeyJ)
	tt.Key(0, ui.KeyX)
	tt.Key(ui.Shift, ui.KeyA)
	tt.Frame()

	if st.bulkCount() != 0 {
		t.Fatalf("set harus kosong setelah eksekusi: %v", st.bulkSel)
	}
	archived, err := st.svc.ListMessages(archiveID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("pesan di Archive %+v, mau 2", archived)
	}
	inbox, _ := st.svc.ListMessages(inboxID, "")
	if len(inbox) != 0 {
		t.Fatalf("INBOX harus kosong: %+v", inbox)
	}
	// Toast ringkasan: dial IMAP gagal di test headless → SyncWarning,
	// tetapi aksi lokal berhasil sehingga teksnya tetap "2 diarsipkan".
	if !tt.HasText("2 diarsipkan") {
		t.Fatal("toast ringkasan arsip bulk tidak tampil")
	}
	if len(st.msgs) != 0 {
		t.Fatalf("daftar pesan harus dimuat ulang: %+v", st.msgs)
	}
}

// TestBulkDeleteModal: Shift+D membuka modal konfirmasi (pola ui.Modal),
// konfirmasi mengeksekusi Service.DeleteMany dan memindahkan pesan ke Trash.
func TestBulkDeleteModal(t *testing.T) {
	st, tt := newBulkState(t)
	trashID := folderIDByRole(st, "trash")

	tt.Key(0, ui.KeyX) // pilih pesan pertama (terbaru)
	tt.Key(ui.Shift, ui.KeyD)
	tt.Frame()
	if !st.bulkDelOpen {
		t.Fatal("modal konfirmasi hapus bulk tidak terbuka")
	}
	if !tt.HasText("Hapus 1 pesan?") {
		t.Fatal("judul modal konfirmasi tidak tampil")
	}
	if err := tt.Click("Hapus 1 pesan"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.bulkDelOpen {
		t.Fatal("modal harus tertutup setelah konfirmasi")
	}
	if st.bulkCount() != 0 {
		t.Fatalf("set harus kosong setelah eksekusi: %v", st.bulkSel)
	}
	trashed, err := st.svc.ListMessages(trashID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 {
		t.Fatalf("pesan di Trash %+v, mau 1", trashed)
	}
	if !tt.HasText("1 dihapus") {
		t.Fatal("toast ringkasan hapus bulk tidak tampil")
	}
}
