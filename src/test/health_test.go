package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"regicide/server"
)

// TestHealthEndpoint is the Phase 0 red test: the server package must expose a
// mounted handler with GET /api/health returning 200 and a JSON body containing
// status "ok" and a non-empty version.
func TestHealthEndpoint(t *testing.T) {
	srv, err := server.New(server.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.FlushWrites)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var got struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if got.Status != "ok" {
		t.Errorf("status field = %q, want \"ok\"", got.Status)
	}
	if got.Version == "" {
		t.Errorf("version field is empty")
	}
}

// TestHealthRejectsWrongMethod proves manual method dispatch is wired up.
func TestHealthRejectsWrongMethod(t *testing.T) {
	srv, err := server.New(server.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.FlushWrites)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
