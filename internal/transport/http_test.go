package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObservedHandlerMetrics(t *testing.T) {
	metrics := &Metrics{}
	handler := observedHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "secret", 1024, metrics, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	metricsHandler := metricsHandler(metrics, "secret")
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	metricsHandler.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		"onboard_http_requests_total 2",
		"onboard_http_unauthorized_total 1",
		"onboard_http_request_duration_seconds_total",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsHandlerRequiresBearer(t *testing.T) {
	handler := metricsHandler(&Metrics{}, "secret")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
