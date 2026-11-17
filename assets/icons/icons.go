// Package icons menyediakan logo Karta ter-embed (SVG master + PNG
// render 256px untuk ikon window/Dock), dipakai internal/ui.
package icons

import _ "embed"

//go:embed karta.svg
var SVG []byte

//go:embed karta_256.png
var PNG256 []byte
