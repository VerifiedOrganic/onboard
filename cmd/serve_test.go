package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/transport"
)

func TestHardenHTTPHandlerRequiresBearer(t *testing.T) {
	called := false
	handler := transport.HardenHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), "secret", 1024)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if called {
		t.Fatal("handler called without bearer token")
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestHardenHTTPHandlerCapsBody(t *testing.T) {
	handler := transport.HardenHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), "", 4)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("12345"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestRootPolicyOptions(t *testing.T) {
	t.Parallel()

	wd := t.TempDir()
	tests := []struct {
		name           string
		httpMode       bool
		allowedRootEnv string
		wantLen        int
	}{
		{name: "stdio env unset", httpMode: false, allowedRootEnv: "", wantLen: 0},
		{name: "stdio env blank", httpMode: false, allowedRootEnv: "  ", wantLen: 0},
		{name: "stdio env set", httpMode: false, allowedRootEnv: "/tmp/x", wantLen: 1},
		{name: "http env unset", httpMode: true, allowedRootEnv: "", wantLen: 1},
		{name: "http env set", httpMode: true, allowedRootEnv: "/tmp/x", wantLen: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := len(rootPolicyOptions(tt.httpMode, tt.allowedRootEnv, wd)); got != tt.wantLen {
				t.Fatalf("len(rootPolicyOptions(%v, %q, %q)) = %d, want %d", tt.httpMode, tt.allowedRootEnv, wd, got, tt.wantLen)
			}
		})
	}
}
