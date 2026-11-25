// Tema Karta: bawaan mygo dengan warna brand dari docs/DESIGN.md §9 —
// aksen Go Blue (#00ADD8), latar dark Obsidian Slate (#111827), teks
// Alabaster (#F8FAFC). Light memakai cyan gelap agar teks di atasnya
// tetap kontras (cyan terang + teks putih hanya ~2.6:1).
package ui

import "github.com/egoist/mygo/ui"

var (
	kartaLight = kartaThemeBuild(false)
	kartaDark  = kartaThemeBuild(true)
)

// kartaTheme mengembalikan tema brand untuk penampilan dark/light.
// Hasilnya cached; dipakai ulang tiap frame via pointer.
func kartaTheme(dark bool) *ui.Theme {
	if dark {
		return kartaDark
	}
	return kartaLight
}

func kartaThemeBuild(dark bool) *ui.Theme {
	if dark {
		t := ui.DarkTheme()
		t.Background = ui.Hex("#111827")
		t.Surface = ui.Hex("#1E293B")
		t.SurfaceHover = ui.Hex("#263449")
		t.SurfacePressed = ui.Hex("#2E3D54")
		t.Border = ui.Hex("#334155")
		t.Text = ui.Hex("#F8FAFC")
		t.TextMuted = ui.Hex("#94A3B8")
		t.Accent = ui.Hex("#00ADD8")
		t.AccentHover = ui.Hex("#38BDF8")
		t.AccentPressed = ui.Hex("#0EA5E9")
		t.AccentText = ui.Hex("#0B1220")
		t.Selection = ui.RGBA(0, 173, 216, 0.4)
		t.Focus = ui.RGBA(56, 189, 248, 0.6)
		return t
	}
	t := ui.LightTheme()
	t.Text = ui.Hex("#111827")
	t.TextMuted = ui.Hex("#64748B")
	t.Accent = ui.Hex("#0284C7")
	t.AccentHover = ui.Hex("#0369A1")
	t.AccentPressed = ui.Hex("#075985")
	t.Selection = ui.RGBA(2, 132, 199, 0.25)
	t.Focus = ui.RGBA(2, 132, 199, 0.55)
	return t
}
