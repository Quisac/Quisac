package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"quisac/src/config"
	"quisac/src/store"
)

func newTestServer(t *testing.T) (*Server, *store.DatadogStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dd.json")
	ds, err := store.NewDatadogStore(path)
	if err != nil {
		t.Fatalf("NewDatadogStore: %v", err)
	}
	cfg := &config.Config{}
	cfg.Server.SessionSecret = "test-secret-that-is-long-enough-32b"
	s := NewWithStore(cfg, ds)
	return s, ds
}

// --- handleDatadogWebhook ---

func TestDatadogWebhook_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/datadog/webhook", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 got %d", w.Code)
	}
}

func TestDatadogWebhook_MissingSecret(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/datadog/webhook", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}

func TestDatadogWebhook_UnknownSecret(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/datadog/webhook", bytes.NewBufferString(`{}`))
	req.Header.Set("X-Quisac-Secret", "not-a-real-secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}

func TestDatadogWebhook_InvalidJSON(t *testing.T) {
	s, ds := newTestServer(t)
	secret, _ := ds.Provision("install-1")
	req := httptest.NewRequest(http.MethodPost, "/datadog/webhook", bytes.NewBufferString(`not json`))
	req.Header.Set("X-Quisac-Secret", secret)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", w.Code)
	}
}

func TestDatadogWebhook_ValidRequest(t *testing.T) {
	s, ds := newTestServer(t)
	secret, _ := ds.Provision("install-1")
	body, _ := json.Marshal(map[string]string{
		"id": "alert-123", "title": "High CPU", "priority": "P1",
	})
	req := httptest.NewRequest(http.MethodPost, "/datadog/webhook", bytes.NewBuffer(body))
	req.Header.Set("X-Quisac-Secret", secret)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
}

// --- handleDatadogProvision ---

func TestDatadogProvision_NotConnected(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/datadog/provision", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}

func TestDatadogProvision_Connected(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	if err := s.setSession(rr, sessionData{InstallationID: "install-1", Login: "testuser"}); err != nil {
		t.Fatalf("setSession: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/datadog/provision", nil)
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["secret"] == "" {
		t.Fatal("expected non-empty secret in response")
	}
	if resp["webhook_url"] == "" {
		t.Fatal("expected non-empty webhook_url in response")
	}
}

func TestDatadogProvision_Idempotent(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.setSession(rr, sessionData{InstallationID: "install-1", Login: "testuser"})
	cookies := rr.Result().Cookies()

	call := func() string {
		req := httptest.NewRequest(http.MethodPost, "/api/datadog/provision", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		return resp["secret"]
	}

	s1 := call()
	s2 := call()
	if s1 != s2 {
		t.Fatalf("provision should be idempotent: got %q and %q", s1, s2)
	}
}

// --- handleDatadogRotate ---

func TestDatadogRotate_NotConnected(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/datadog/rotate", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", w.Code)
	}
}

func TestDatadogRotate_ChangesSecret(t *testing.T) {
	s, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.setSession(rr, sessionData{InstallationID: "install-1", Login: "testuser"})
	cookies := rr.Result().Cookies()

	makeReq := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return req
	}

	w1 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w1, makeReq("/api/datadog/provision"))
	var r1 map[string]string
	json.NewDecoder(w1.Body).Decode(&r1)

	w2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w2, makeReq("/api/datadog/rotate"))
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w2.Code)
	}
	var r2 map[string]string
	json.NewDecoder(w2.Body).Decode(&r2)
	if r1["secret"] == r2["secret"] {
		t.Fatal("expected secret to change after rotation")
	}
}
