# Relay OAuth Karta

Layanan relay Google OAuth untuk aplikasi desktop [Karta](https://github.com/dchya24/karta). Berjalan stateless (tanpa database, tanpa state), menyimpan `client_id` + `client_secret` Google sehingga aplikasi desktop tidak perlu meng-embed secret. Modul Go terpisah (`karta/server`), stdlib saja, tanpa dependency eksternal.

## API

Kontrak ini dipakai bersama `internal/app/oauthrelay.go` di repo utama.

### `GET /oauth/config`

```json
{"client_id": "<GOOGLE_OAUTH_CLIENT_ID>"}
```

### `POST /oauth/exchange`

Request JSON:

```json
{"grant_type":"authorization_code","code":"...","code_verifier":"...","redirect_uri":"http://127.0.0.1:8699"}
```

atau

```json
{"grant_type":"refresh_token","refresh_token":"..."}
```

Server menambahkan `client_id` dan `client_secret`, lalu mem-proxy request (form urlencoded) ke `https://oauth2.googleapis.com/token`. Respons Google — status code dan body — diteruskan apa adanya (mis. `invalid_grant` tetap 400). Server **tidak** menyimpan token, code, atau state apa pun.

## Konfigurasi (environment)

| Variabel | Wajib | Default | Keterangan |
|---|---|---|---|
| `PORT` | tidak | `8080` | Port mendengarkan |
| `GOOGLE_OAUTH_CLIENT_ID` | **ya** | — | Kosong → fail fast saat start |
| `GOOGLE_OAUTH_CLIENT_SECRET` | **ya** | — | Kosong → fail fast saat start |

## Perlindungan bawaan

- Rate limit fixed window per IP: ±10 request/menit, `429` saat lampaui (hormati `X-Forwarded-For` hop pertama bila di belakang reverse proxy).
- Hanya method yang benar (`405` untuk lainnya), `Content-Type: application/json` divalidasi ringan.
- Batas ukuran body 64 KB (`413`), timeout baca/tulis/idle pada `http.Server`.
- Logging ringan (method, path, status, durasi) — tanpa mencatat isi body/token.

## Menjalankan

```sh
cd server
GOOGLE_OAUTH_CLIENT_ID=xxx.apps.googleusercontent.com \
GOOGLE_OAUTH_CLIENT_SECRET=xxx \
PORT=8080 \
go run .
```

Unit test: `go test ./...` · `go vet ./...` · build statik: `CGO_ENABLED=0 go build .`

## Docker

```sh
docker build -t karta-oauth-relay .
docker run -p 8080:8080 \
  -e GOOGLE_OAUTH_CLIENT_ID=xxx.apps.googleusercontent.com \
  -e GOOGLE_OAUTH_CLIENT_SECRET=xxx \
  karta-oauth-relay
```

Image final memakai distroless/static, non-root.

## Deploy (placeholder)

Rencana rilis: satu instance kecil di belakang reverse proxy TLS (`https://karta.devchya.id` — sudah jadi default di `internal/app/oauthrelay.go`), mis. di VPS dengan Caddy/nginx sebagai terminator TLS. Reverse proxy diharapkan meneruskan `X-Forwarded-For` agar rate limit per IP berfungsi. Layanan ini stateless dan single-binary, sehingga cukup dijalankan sebagai systemd unit atau container (compose):

```yaml
services:
  relay:
    build: .
    restart: unless-stopped
    environment:
      GOOGLE_OAUTH_CLIENT_ID: xxx
      GOOGLE_OAUTH_CLIENT_SECRET: xxx
    ports: ["127.0.0.1:8080:8080"]
```

Catatan: endpoint ini terbuka publik; pastikan hanya `POST /oauth/exchange` dan `GET /oauth/config` yang terekspos, dan pertimbangkan rate limit tambahan di edge proxy.

## Lisensi

Relay ini bagian dari proyek [Karta](https://github.com/dchya24/karta) dan berlisensi MIT.
