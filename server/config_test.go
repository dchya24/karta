package main

import (
	"errors"
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Run("credentials lengkap", func(t *testing.T) {
		t.Setenv("PORT", "9090")
		t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "id")
		t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "sec")
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("err = %v, mau nil", err)
		}
		if cfg.Port != "9090" || cfg.ClientID != "id" || cfg.Secret != "sec" {
			t.Fatalf("cfg = %+v", cfg)
		}
		if cfg.TokenURL != defaultTokenURL {
			t.Fatalf("TokenURL = %q, mau %q", cfg.TokenURL, defaultTokenURL)
		}
	})

	t.Run("credentials kosong fail fast", func(t *testing.T) {
		t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "")
		t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "")
		if _, err := loadConfig(); !errors.Is(err, errMissingCredentials) {
			t.Fatalf("err = %v, mau errMissingCredentials", err)
		}
	})

	t.Run("port default", func(t *testing.T) {
		t.Setenv("PORT", "")
		t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "id")
		t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "sec")
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("err = %v, mau nil", err)
		}
		if cfg.Port != "8080" {
			t.Fatalf("Port = %q, mau 8080", cfg.Port)
		}
	})
}

func TestLoadConfigFromFiles(t *testing.T) {
	dir := t.TempDir()
	idPath, secretPath := dir+"/id", dir+"/secret"
	os.WriteFile(idPath, []byte("id-from-file\n"), 0o600)
	os.WriteFile(secretPath, []byte("  sec-from-file  "), 0o600)
	t.Setenv("PORT", "")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID_FILE", idPath)
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET_FILE", secretPath)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("err = %v, mau nil", err)
	}
	if cfg.ClientID != "id-from-file" || cfg.Secret != "sec-from-file" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// File yang tidak ada diperlakukan kosong → fail fast.
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET_FILE", dir+"/tidak-ada")
	if _, err := loadConfig(); !errors.Is(err, errMissingCredentials) {
		t.Fatalf("err = %v, mau errMissingCredentials", err)
	}
}
