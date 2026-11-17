#!/bin/sh
# Jalankan aplikasi Karta (native mygo/ui). GUI berjalan di foreground.
# Catatan: unset GOROOT/GOPATH — environment gvm di shell (default go1.25.14)
# membuat binary go1.27.4 mencari tool di GOROOT lama ("no such tool compile").
# Build dulu lalu exec: satu proses, tanpa pembungkus `go run` (hemat ~39MB
# toolchain dan membuat pengukuran memori lebih jujur).
set -e
GVM_GO="$HOME/.gvm/gos/go1.27.4"
export PATH="$GVM_GO/bin:$PATH"
unset GOROOT GOPATH GOBIN GOTOOLDIR GOFLAGS GVM_OVERLAY_PREFIX GVM_NAME gvm_go_name gvm_pkgset_name
cd "$(dirname "$0")"
export CGO_ENABLED=0
BIN=/tmp/karta-run-bin
go build -o "$BIN" ./cmd/karta
exec "$BIN"
