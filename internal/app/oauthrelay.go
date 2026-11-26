package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// oauthRelayDefault adalah alamat BE relay OAuth yang menyimpan
// client_secret. Ganti (atau set env KARTA_OAUTH_RELAY) sebelum rilis
// publik bila BE dipasang di domain lain.
const oauthRelayDefault = "https://auth.karta.dchya24.dev"

func relayBase() string {
	if u := strings.TrimSpace(os.Getenv("KARTA_OAUTH_RELAY")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return oauthRelayDefault
}

var relayHTTP = &http.Client{Timeout: 20 * time.Second}

// relayConfigCache menyimpan client_id hasil fetch relay agar aplikasi
// tidak menembak jaringan setiap kali dibuka.
func relayConfigCachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "karta", "oauth-client.json"), nil
}

// relayClientID mengambil client_id dari relay, dengan cache lokal.
func relayClientID(ctx context.Context) (string, error) {
	if path, err := relayConfigCachePath(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			var cached struct {
				ClientID string `json:"client_id"`
			}
			if json.Unmarshal(data, &cached) == nil && strings.TrimSpace(cached.ClientID) != "" {
				return strings.TrimSpace(cached.ClientID), nil
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relayBase()+"/oauth/config", nil)
	if err != nil {
		return "", err
	}
	resp, err := relayHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("hubungi relay OAuth %s: %w", relayBase(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("relay OAuth config: HTTP %d: %s", resp.StatusCode, body)
	}
	var cfg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", fmt.Errorf("respons relay config tidak valid: %w", err)
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	if cfg.ClientID == "" {
		return "", fmt.Errorf("relay config tanpa client_id")
	}
	if path, err := relayConfigCachePath(); err == nil {
		if dir := filepath.Dir(path); os.MkdirAll(dir, 0o700) == nil {
			data, _ := json.Marshal(cfg)
			os.WriteFile(path, data, 0o600) // cache best-effort
		}
	}
	return cfg.ClientID, nil
}

// parseGoogleToken membaca respons token Google (passthrough relay):
// access_token, refresh_token, id_token, scope, expires_in.
func parseGoogleToken(body []byte) (*oauth2.Token, error) {
	var resp struct {
		oauth2.Token
		Scope   string `json:"scope"`
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("respons token tidak valid: %w", err)
	}
	if resp.AccessToken == "" {
		return nil, fmt.Errorf("respons token tanpa access_token: %s", body)
	}
	extra := url.Values{"scope": []string{resp.Scope}, "id_token": []string{resp.IDToken}}
	return resp.Token.WithExtra(extra), nil
}

// relayTokenError mengubah respons error relay (passthrough Google:
// {"error": "...", "error_description": "..."}) menjadi error.
func relayTokenError(status int, body []byte) error {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		msg = e.Error + ": " + e.Description
	}
	return fmt.Errorf("token OAuth ditolak (HTTP %d): %s", status, msg)
}

// relayPostToken POST payload ke relay /oauth/exchange dan membaca
// respons token Google.
func relayPostToken(ctx context.Context, payload any) (*oauth2.Token, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayBase()+"/oauth/exchange", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := relayHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hubungi relay OAuth: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, relayTokenError(resp.StatusCode, body)
	}
	return parseGoogleToken(body)
}

// relayExchangeCode menukar code OAuth dengan token lewat relay.
func relayExchangeCode(ctx context.Context, code, verifier, redirectURI string) (*oauth2.Token, error) {
	return relayPostToken(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
	})
}

// relayRefreshToken memperbarui access token lewat relay. Respons tanpa
// refresh_token memakai refresh token lama (perilaku Google).
func relayRefreshToken(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	return relayPostToken(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	})
}
