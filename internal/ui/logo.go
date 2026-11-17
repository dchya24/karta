package ui

import (
	"github.com/egoist/mygo/ui"

	kartaicons "karta/assets/icons"
)

// kartaLogo di-parse sekali di sini (bukan di view yang jalan tiap frame).
var kartaLogo = ui.MustParseSVG(kartaicons.SVG)
