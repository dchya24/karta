package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed landing.html
var landingHTML []byte

//go:embed main-light.png
var mainLightPNG []byte

//go:embed main-dark.png
var mainDarkPNG []byte

//go:embed fonts/*.woff2
var fontsEmbed embed.FS

//go:embed composer.png
var composerPNG []byte

// Konstanta endpoint relay.
const (
	pathLanding   = "/"
	pathMainLight = "/main-light.png"
	pathMainDark  = "/main-dark.png"
	pathComposer  = "/composer.png"
	pathFonts     = "/fonts/"
	pathConfig    = "/oauth/config"
	pathExchange  = "/oauth/exchange"

	// maxExchangeBytes adalah batas ukuran body /oauth/exchange.
	maxExchangeBytes = 64 << 10

	// maxConfigBytes adalah batas ukuran body /oauth/config (mustahil dipakai,
	// tetap dibatasi untuk method-metod 405 / body tak terduga).
	maxConfigBytes = 4 << 10
)

// errMissingCredentials dilaporkan saat GOOGLE_OAUTH_CLIENT_ID atau
// GOOGLE_OAUTH_CLIENT_SECRET kosong.
var errMissingCredentials = errors.New("GOOGLE_OAUTH_CLIENT_ID dan GOOGLE_OAUTH_CLIENT_SECRET wajib diisi")

// config memuat konfigurasi layanan relay.
type config struct {
	Port     string
	ClientID string
	Secret   string
	TokenURL string
}

// newOAuthHandler menyusun handler HTTP relay. tokenURL diambil dari cfg
// agar test dapat menyuntikkan fake Google.
func newOAuthHandler(cfg config, limiter *rateLimiter) *oauthHandler {
	return &oauthHandler{
		clientID: cfg.ClientID,
		secret:   cfg.Secret,
		tokenURL: cfg.TokenURL,
		limiter:  limiter,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// oauthHandler adalah handler HTTP relay OAuth. Stateless: tidak menyimpan
// token, code, atau state apa pun.
type oauthHandler struct {
	clientID string
	secret   string
	tokenURL string
	limiter  *rateLimiter
	http     *http.Client
}

// routes menyusun mux endpoint relay.
func (h *oauthHandler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(pathLanding, h.handleLanding)
	mux.HandleFunc(pathMainLight, serveStatic("image/png", mainLightPNG))
	mux.HandleFunc(pathMainDark, serveStatic("image/png", mainDarkPNG))
	mux.HandleFunc(pathComposer, serveStatic("image/png", composerPNG))
	mux.Handle(pathFonts, fontsHandler())
	mux.HandleFunc(pathConfig, h.handleConfig)
	mux.HandleFunc(pathExchange, h.handleExchange)
	return mux
}

// fontsHandler melayani font woff2 yang di-embed, dengan cache panjang.
func fontsHandler() http.Handler {
	sub, err := fs.Sub(fontsEmbed, "fonts")
	if err != nil {
		panic(fmt.Errorf("sub fonts embed: %w", err))
	}
	files := http.StripPrefix(pathFonts, http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}

func serveStatic(contentType string, data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeErrorJSON(w, http.StatusMethodNotAllowed, "method tidak diizinkan")
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			w.Write(data)
		}
	}
}

// handleLanding melayani GET / dengan halaman landing HTML Karta.
func (h *oauthHandler) handleLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != pathLanding {
		http.NotFound(w, r)
		return
	}
	serveStatic("text/html; charset=utf-8", landingHTML)(w, r)
}

// writeJSON menulis JSON dengan status code yang diberikan.
func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func writeErrorJSON(w http.ResponseWriter, status int, err string) {
	body, _ := json.Marshal(map[string]string{"error": err})
	writeJSON(w, status, body)
}

// handleConfig melayani GET /oauth/config: mengembalikan client_id publik.
func (h *oauthHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method tidak diizinkan")
		return
	}
	body, _ := json.Marshal(map[string]string{"client_id": h.clientID})
	writeJSON(w, http.StatusOK, body)
}

// exchangeRequest adalah body JSON yang dikirim klien Karta.
type exchangeRequest struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
	RefreshToken string `json:"refresh_token"`
}

// handleExchange melayani POST /oauth/exchange: menambahkan client_id dan
// client_secret lalu mem-proxy request ke endpoint token Google. Respons
// Google diteruskan apa adanya beserta status code-nya.
func (h *oauthHandler) handleExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method tidak diizinkan")
		return
	}
	if !h.limiter.allow(clientIP(r)) {
		writeErrorJSON(w, http.StatusTooManyRequests, "terlalu banyak permintaan")
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeErrorJSON(w, http.StatusUnsupportedMediaType, "Content-Type harus application/json")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxExchangeBytes))
	if err != nil {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "body terlalu besar")
		return
	}
	var req exchangeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "body JSON tidak valid")
		return
	}

	form, err := h.buildForm(&req)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	h.proxyGoogle(w, r, form.Encode())
}

// buildForm menyusun form urlencoded untuk endpoint token Google. client_id
// dan client_secret SELALU disertakan.
func (h *oauthHandler) buildForm(req *exchangeRequest) (url.Values, error) {
	form := url.Values{}
	form.Set("grant_type", req.GrantType)
	switch req.GrantType {
	case "authorization_code":
		if req.Code == "" || req.CodeVerifier == "" {
			return nil, fmt.Errorf("grant_type authorization_code membutuhkan code dan code_verifier")
		}
		form.Set("code", req.Code)
		form.Set("code_verifier", req.CodeVerifier)
		form.Set("redirect_uri", req.RedirectURI)
	case "refresh_token":
		if req.RefreshToken == "" {
			return nil, fmt.Errorf("grant_type refresh_token membutuhkan refresh_token")
		}
		form.Set("refresh_token", req.RefreshToken)
	default:
		return nil, fmt.Errorf("grant_type tidak didukung: %q", req.GrantType)
	}
	form.Set("client_id", h.clientID)
	form.Set("client_secret", h.secret)
	return form, nil
}

// proxyGoogle mengirim form ke endpoint token Google dan meneruskan respons
// (status code + body) kembali ke klien.
func (h *oauthHandler) proxyGoogle(w http.ResponseWriter, r *http.Request, form string) {
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.tokenURL, strings.NewReader(form))
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "gagal menyiapkan request Google")
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.http.Do(req)
	if err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "gagal menghubungi endpoint token Google")
		return
	}
	defer resp.Body.Close()

	gbody, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "gagal membaca respons Google")
		return
	}
	// Passthrough: status code dan body Google diteruskan apa adanya
	// (error seperti invalid_grant pun tetap diteruskan).
	writeJSON(w, resp.StatusCode, gbody)
}

// clientIP menentukan IP klien: X-Forwarded-For hop pertama bila ada
// (di belakang reverse proxy), selain itu RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
