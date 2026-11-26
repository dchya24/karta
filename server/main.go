// Command server adalah layanan relay OAuth Karta.
//
// Layanan stateless ini menyimpan Google OAuth client_id dan client_secret
// sehingga aplikasi desktop Karta tidak perlu meng-embed secret. Kontrak API
// dipakai bersama internal/app/oauthrelay.go di repo utama.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultTokenURL adalah endpoint token Google (konstanta).
const defaultTokenURL = "https://oauth2.googleapis.com/token"

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// envFile membaca nilai dari file (satu baris pertama), untuk pola
// *_FILE bila kredensial disediakan systemd sebagai kredensial terenkripsi.
func envFile(name string) string {
	path := os.Getenv(name)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// loadConfig membaca konfigurasi dari environment dan gagal cepat bila
// kredensial Google tidak tersedia. Kredensial boleh lewat env langsung
// atau env *_FILE yang menunjuk file berisi nilai (systemd credential).
func loadConfig() (cfg config, err error) {
	cfg = config{
		Port:     env("PORT", "8080"),
		ClientID: env("GOOGLE_OAUTH_CLIENT_ID", ""),
		Secret:   env("GOOGLE_OAUTH_CLIENT_SECRET", ""),
		TokenURL: defaultTokenURL,
	}
	if cfg.ClientID == "" {
		cfg.ClientID = envFile("GOOGLE_OAUTH_CLIENT_ID_FILE")
	}
	if cfg.Secret == "" {
		cfg.Secret = envFile("GOOGLE_OAUTH_CLIENT_SECRET_FILE")
	}
	if cfg.ClientID == "" || cfg.Secret == "" {
		return cfg, errMissingCredentials
	}
	return cfg, nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("relay oauth: %v", err)
	}

	h := newOAuthHandler(cfg, newRateLimiter(10, time.Minute))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           logRequests(h.routes()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("relay oauth: mendengarkan di :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("relay oauth: %v", err)
	}
}
