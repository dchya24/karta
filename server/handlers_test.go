package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestHandler menyusun handler relay dengan fake Google lokal. fake
// menangkap form yang diteruskan agar test bisa memverifikasinya.
func newTestHandler(t *testing.T, fakeGoogle http.HandlerFunc) (*oauthHandler, *capturedForm) {
	t.Helper()
	captured := &capturedForm{}
	if fakeGoogle == nil {
		fakeGoogle = func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			captured.values, _ = parseForm(body)
			captured.contentType = r.Header.Get("Content-Type")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3599,"scope":"https://mail.google.com/","token_type":"Bearer"}`))
		}
	}
	google := httptest.NewServer(fakeGoogle)
	t.Cleanup(google.Close)

	h := newOAuthHandler(config{
		Port:     "0",
		ClientID: "test-client-id",
		Secret:   "test-client-secret",
		TokenURL: google.URL,
	}, newRateLimiter(10, time.Minute))
	return h, captured
}

// capturedForm menyimpan form yang diterima fake Google.
type capturedForm struct {
	values      map[string]string
	contentType string
}

// parseForm membaca body form urlencoded menjadi peta sederhana.
func parseForm(body []byte) (map[string]string, error) {
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, vs := range vals {
		out[k] = strings.Join(vs, ",")
	}
	return out, nil
}

func postJSON(t *testing.T, h *oauthHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/oauth/exchange", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleExchange(rec, req)
	return rec
}

// --- /oauth/config ---

func TestConfigEndpoint(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/oauth/config", nil)
	rec := httptest.NewRecorder()
	h.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, mau 200", rec.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body bukan JSON: %v", err)
	}
	if got["client_id"] != "test-client-id" {
		t.Fatalf("client_id = %q, mau %q", got["client_id"], "test-client-id")
	}
}

func TestConfigWrongMethod(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/oauth/config", nil)
	rec := httptest.NewRecorder()
	h.handleConfig(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, mau 405", rec.Code)
	}
}

func TestExchangeWrongMethod(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/oauth/exchange", nil)
	rec := httptest.NewRecorder()
	h.handleExchange(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, mau 405", rec.Code)
	}
}

// --- /oauth/exchange: authorization_code ---

func TestExchangeCodeForwardsForm(t *testing.T) {
	h, captured := newTestHandler(t, nil)

	body := `{"grant_type":"authorization_code","code":"abc def","code_verifier":"verifier+123","redirect_uri":"http://127.0.0.1:8699"}`
	rec := postJSON(t, h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, mau 200; body = %s", rec.Code, rec.Body.String())
	}
	c := *captured
	if c.contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type ke Google = %q", c.contentType)
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "abc def",      // ter-escape lalu di-decode fake
		"code_verifier": "verifier+123", // ter-escape lalu di-decode fake
		"redirect_uri":  "http://127.0.0.1:8699",
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret",
	}
	for k, v := range want {
		if c.values[k] != v {
			t.Fatalf("form[%q] = %q, mau %q (form penuh: %v)", k, c.values[k], v, c.values)
		}
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("respons bukan JSON: %v", err)
	}
	if resp["access_token"] != "at" {
		t.Fatalf("respons passthrough tidak sesuai: %s", rec.Body.String())
	}
}

// --- /oauth/exchange: refresh_token ---

func TestExchangeRefreshForwardsForm(t *testing.T) {
	h, captured := newTestHandler(t, nil)

	rec := postJSON(t, h, `{"grant_type":"refresh_token","refresh_token":"rt-old"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, mau 200; body = %s", rec.Code, rec.Body.String())
	}
	c := *captured
	want := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "rt-old",
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret",
	}
	for k, v := range want {
		if c.values[k] != v {
			t.Fatalf("form[%q] = %q, mau %q (form penuh: %v)", k, c.values[k], v, c.values)
		}
	}
	if _, ok := c.values["code"]; ok {
		t.Fatalf("form refresh tidak boleh memuat code: %v", c.values)
	}
}

// --- validasi request ---

func TestExchangeInvalidBody(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	cases := []struct {
		name string
		body string
		ct   string
		want int
	}{
		{"json rusak", "{bukan json", "application/json", http.StatusBadRequest},
		{"content-type salah", `{"grant_type":"refresh_token","refresh_token":"rt"}`, "text/plain", http.StatusUnsupportedMediaType},
		{"grant_type tak dikenal", `{"grant_type":"password","username":"x"}`, "application/json", http.StatusBadRequest},
		{"code tanpa verifier", `{"grant_type":"authorization_code","code":"abc"}`, "application/json", http.StatusBadRequest},
		{"refresh tanpa token", `{"grant_type":"refresh_token"}`, "application/json", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/oauth/exchange", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.ct)
			rec := httptest.NewRecorder()
			h.handleExchange(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, mau %d; body = %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestExchangeBodyTooLarge(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	big := `{"grant_type":"refresh_token","refresh_token":"` + strings.Repeat("a", maxExchangeBytes) + `"}`
	rec := postJSON(t, h, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, mau 413", rec.Code)
	}
}

// --- passthrough error Google ---

func TestExchangeGoogleErrorPassthrough(t *testing.T) {
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
	})

	rec := postJSON(t, h, `{"grant_type":"refresh_token","refresh_token":"revoked"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, mau 400 (passthrough status Google)", rec.Code)
	}
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("respons bukan JSON: %v", err)
	}
	if e.Error != "invalid_grant" {
		t.Fatalf("error = %q, mau invalid_grant", e.Error)
	}
}

func TestGoogleDown(t *testing.T) {
	h := newOAuthHandler(config{
		Port:     "0",
		ClientID: "id",
		Secret:   "sec",
		TokenURL: "http://127.0.0.1:1", // tidak ada yang mendengarkan
	}, newRateLimiter(10, time.Minute))
	rec := postJSON(t, h, `{"grant_type":"refresh_token","refresh_token":"rt"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, mau 502", rec.Code)
	}
}

// --- rate limit ---

func TestRateLimit(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	// 10 permintaan pertama lewat.
	for i := 0; i < 10; i++ {
		rec := postJSON(t, h, `{"grant_type":"refresh_token","refresh_token":"rt"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("permintaan ke-%d: status = %d, mau 200", i+1, rec.Code)
		}
	}
	// Permintaan ke-11 dari IP yang sama ditolak.
	rec := postJSON(t, h, `{"grant_type":"refresh_token","refresh_token":"rt"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("permintaan ke-11: status = %d, mau 429", rec.Code)
	}
}

func TestRateLimitWindowResets(t *testing.T) {
	limiter := newRateLimiter(2, 10*time.Millisecond)
	if !limiter.allow("1.2.3.4") || !limiter.allow("1.2.3.4") {
		t.Fatal("dua permintaan pertama harus diizinkan")
	}
	if limiter.allow("1.2.3.4") {
		t.Fatal("permintaan ketiga harus ditolak")
	}
	time.Sleep(15 * time.Millisecond) // window reset
	if !limiter.allow("1.2.3.4") {
		t.Fatal("setelah window lewat, permintaan harus diizinkan lagi")
	}
}

// --- routes & unknown path ---

func TestLandingPage(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, mau 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, mau text/html", ct)
	}
	if !strings.Contains(string(body), "Karta") || !strings.Contains(string(body), "main-light.png") || strings.Contains(string(body), "/oauth/exchange") {
		t.Fatalf("landing page tidak memuat konten Karta yang diharapkan")
	}

	for _, imgPath := range []string{"/main-light.png", "/main-dark.png", "/composer.png"} {
		imgResp, err := http.Get(srv.URL + imgPath)
		if err != nil {
			t.Fatal(err)
		}
		imgBytes, _ := io.ReadAll(imgResp.Body)
		imgResp.Body.Close()
		if imgResp.StatusCode != http.StatusOK || imgResp.Header.Get("Content-Type") != "image/png" || len(imgBytes) == 0 {
			t.Fatalf("%s: status=%d ct=%q len=%d", imgPath, imgResp.StatusCode, imgResp.Header.Get("Content-Type"), len(imgBytes))
		}
	}

	if strings.Contains(string(body), "fonts.googleapis.com") {
		t.Fatalf("landing page tidak boleh memuat font dari pihak ketiga")
	}
	for _, name := range []string{"bricolage-grotesque", "ibm-plex-sans", "jetbrains-mono"} {
		fontResp, err := http.Get(srv.URL + "/fonts/" + name + ".woff2")
		if err != nil {
			t.Fatal(err)
		}
		fontBytes, _ := io.ReadAll(fontResp.Body)
		fontResp.Body.Close()
		if fontResp.StatusCode != http.StatusOK || len(fontBytes) == 0 || !strings.Contains(fontResp.Header.Get("Cache-Control"), "immutable") {
			t.Fatalf("font %s: status=%d len=%d cc=%q", name, fontResp.StatusCode, len(fontBytes), fontResp.Header.Get("Cache-Control"))
		}
	}

	postResp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, mau 405", postResp.StatusCode)
	}
}

func TestUnknownPath(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/lain")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, mau 404", resp.StatusCode)
	}
}

// TestEndToEndConfigAndExchange menguji full mux dari sisi klien HTTP,
// meniru pola pemakaian internal/app/oauthrelay.go.
func TestEndToEndConfigAndExchange(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	srv := httptest.NewServer(logRequests(h.routes()))
	t.Cleanup(srv.Close)

	// 1. GET /oauth/config
	resp, err := http.Get(srv.URL + "/oauth/config")
	if err != nil {
		t.Fatal(err)
	}
	cfgBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config status = %d", resp.StatusCode)
	}
	var cfg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(cfgBody, &cfg); err != nil || cfg.ClientID != "test-client-id" {
		t.Fatalf("config = %s (%v)", cfgBody, err)
	}

	// 2. POST /oauth/exchange
	payload, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          "abc",
		"code_verifier": "ver",
		"redirect_uri":  "http://127.0.0.1:8699",
	})
	resp, err = http.Post(srv.URL+"/oauth/exchange", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange status = %d; body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"access_token":"at"`) {
		t.Fatalf("passthrough token hilang: %s", body)
	}
}
