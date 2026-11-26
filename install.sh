#!/usr/bin/env bash
# Installer Karta dari rilis GitHub terbaru (Linux & macOS).
# Pakai: ./install.sh [versi]   (contoh: ./install.sh v0.3.1)
# Tanpa argumen = rilis terbaru. Env KARTA_VERSION juga didukung.
set -euo pipefail

REPO="dchya24/karta"
VER="${1:-${KARTA_VERSION:-}}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	else
		wget -qO "$2" "$1"
	fi
}

download() {
	fetch "https://github.com/$REPO/releases/download/$VER/$1" "$TMP/$1"
}

if [ -z "$VER" ]; then
	VER=$(fetch "https://api.github.com/repos/$REPO/releases/latest" /dev/stdout |
		grep -m1 '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
fi
echo "Karta $VER"
# Tag selalu berprefix v; aset rilis dinamai tanpa prefix (…-0.3.1.tar.gz).
case "$VER" in v*) ;; *) VER="v$VER" ;; esac
NUM="${VER#v}"

case "$(uname -s)" in
Linux)
	case "$(uname -m)" in
	arm64 | aarch64) ARCH=arm64 ;;
	*) ARCH=amd64 ;;
	esac

	if command -v dpkg >/dev/null 2>&1; then
		PKG="karta-linux-$ARCH-$NUM.deb"
		download "$PKG"
		echo "Memasang $PKG (butuh sudo)…"
		sudo dpkg -i "$TMP/$PKG" || sudo apt-get -f install -y
	elif command -v rpm >/dev/null 2>&1; then
		PKG="karta-linux-$ARCH-$NUM.rpm"
		download "$PKG"
		echo "Memasang $PKG (butuh sudo)…"
		if command -v dnf >/dev/null 2>&1; then
			sudo dnf install -y "$TMP/$PKG"
		else
			sudo rpm -Uvh "$TMP/$PKG"
		fi
	else
		# Portabel: binary ke ~/.local/bin + entri menu + ikon.
		PKG="karta-linux-$ARCH-$NUM.tar.gz"
		download "$PKG"
		tar -xzf "$TMP/$PKG" -C "$TMP"
		BIN="$HOME/.local/bin"
		mkdir -p "$BIN"
		install -m755 "$TMP/dist/karta" "$BIN/karta"
		ICON_DIR="$HOME/.local/share/icons/hicolor/512x512/apps"
		DESKTOP_DIR="$HOME/.local/share/applications"
		mkdir -p "$ICON_DIR" "$DESKTOP_DIR"
		fetch "https://raw.githubusercontent.com/$REPO/$VER/assets/icons/karta_512.png" "$ICON_DIR/karta.png"
		fetch "https://raw.githubusercontent.com/$REPO/$VER/packaging/karta.desktop" "$TMP/karta.desktop"
		sed "s|Exec=karta|Exec=$BIN/karta|" "$TMP/karta.desktop" > "$DESKTOP_DIR/karta.desktop"
		update-desktop-database "$DESKTOP_DIR" 2>/dev/null || true
		echo "Terpasang di $BIN/karta (pastikan $BIN ada di PATH)."
	fi
	;;
Darwin)
	case "$(uname -m)" in
	arm64) ARCH=arm64 ;;
	*) ARCH=amd64 ;;
	esac
	PKG="Karta-macos-$ARCH-$NUM.dmg"
	download "$PKG"
	echo "Memasang Karta.app ke /Applications (butuh izin admin bila perlu)…"
	hdiutil attach -nobrowse -quiet "$TMP/$PKG"
	VOL=$(ls /Volumes | grep -i "^Karta" | head -1)
	cp -R "/Volumes/$VOL/Karta.app" /Applications/
	hdiutil detach "/Volumes/$VOL" -quiet
	# Unsigned: hapus quarantine agar langsung bisa dibuka.
	xattr -cr /Applications/Karta.app 2>/dev/null || true
	;;
*)
	echo "OS tidak didukung installer ini: $(uname -s)" >&2
	echo "Unduh manual: https://github.com/$REPO/releases/latest" >&2
	exit 1
	;;
esac

echo "Selesai. Jalankan 'karta' dari menu aplikasi."
