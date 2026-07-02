// Package transport serves the onboard MCP server over Streamable HTTP.
package transport

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config holds Streamable HTTP server settings.
type Config struct {
	Addr         string
	Token        string
	MaxBodyBytes int64
	Logger       *slog.Logger
}

// Metrics exposes basic HTTP request counters for the /metrics endpoint.
type Metrics struct {
	RequestsTotal      atomic.Int64
	UnauthorizedTotal  atomic.Int64
	DurationNanosTotal atomic.Int64
}

// ServeHTTP runs the MCP server on Streamable HTTP until ctx is cancelled.
func ServeHTTP(ctx context.Context, mcpServer *mcp.Server, cfg Config) error {
	if cfg.Token == "" {
		return fmt.Errorf("http mode requires a bearer token")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		nil,
	)
	metrics := &Metrics{}
	handler := observedHandler(mcpHandler, cfg.Token, cfg.MaxBodyBytes, metrics, cfg.Logger)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/metrics", metricsHandler(metrics, cfg.Token))
	fmt.Fprintf(os.Stderr, "onboard MCP server listening on http://%s/mcp (metrics: http://%s/metrics)\n", cfg.Addr, cfg.Addr)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// HardenHandler wraps next with bearer auth and optional body size limits (for tests).
func HardenHandler(next http.Handler, bearerToken string, maxBodyBytes int64) http.Handler {
	return observedHandler(next, bearerToken, maxBodyBytes, nil, nil)
}

func observedHandler(next http.Handler, bearerToken string, maxBodyBytes int64, metrics *Metrics, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if metrics != nil {
			metrics.RequestsTotal.Add(1)
		}
		defer func() {
			elapsed := time.Since(start)
			if metrics != nil {
				metrics.DurationNanosTotal.Add(elapsed.Nanoseconds())
			}
			if logger != nil {
				logger.Info("http request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", rec.status,
					"duration_ms", elapsed.Milliseconds(),
					"remote", r.RemoteAddr,
				)
			}
		}()
		if o := r.Header.Get("Origin"); o != "" && !allowedLocalOrigin(o) {
			rec.WriteHeader(http.StatusForbidden)
			return
		}
		if bearerToken != "" && !validBearer(r, bearerToken) {
			if metrics != nil {
				metrics.UnauthorizedTotal.Add(1)
			}
			if logger != nil {
				logger.Warn("http unauthorized", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(rec, "unauthorized", http.StatusUnauthorized)
			return
		}
		if maxBodyBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(rec, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func metricsHandler(metrics *Metrics, bearerToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && !allowedLocalOrigin(o) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if bearerToken != "" && !validBearer(r, bearerToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w, "onboard_http_requests_total %d\n", metrics.RequestsTotal.Load())
		_, _ = fmt.Fprintf(w, "onboard_http_unauthorized_total %d\n", metrics.UnauthorizedTotal.Load())
		_, _ = fmt.Fprintf(w, "onboard_http_request_duration_seconds_total %.6f\n", float64(metrics.DurationNanosTotal.Load())/float64(time.Second))
	})
}

// allowedLocalOrigin accepts browser origins that can only be same-machine.
func allowedLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func validBearer(r *http.Request, want string) bool {
	const prefix = "Bearer "
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, prefix) {
		return false
	}
	got = strings.TrimSpace(strings.TrimPrefix(got, prefix))
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
