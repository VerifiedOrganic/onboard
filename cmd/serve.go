package cmd

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/server"
)

var (
	serveHTTP          string
	serveHTTPToken     string
	serveHTTPMaxBodyMB int64
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the MCP server (stdio by default, or Streamable HTTP with --http)",
	Long: `Starts the onboard MCP server.

By default it speaks over stdin/stdout — this is what an agent launches. With
--http it serves the same tools, resources, and prompts over Streamable HTTP at
/mcp, for local headless/CI use. For hosted/shared use, put it behind auth, TLS,
and network controls. Set --http-token or ONBOARD_HTTP_TOKEN to require a bearer token.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		s := server.New(version)

		if serveHTTP != "" {
			mcpHandler := mcp.NewStreamableHTTPHandler(
				func(*http.Request) *mcp.Server { return s },
				nil, // default options: stateful sessions, localhost protection on
			)
			token := serveHTTPToken
			if token == "" {
				token = os.Getenv("ONBOARD_HTTP_TOKEN")
			}
			metrics := &httpMetrics{}
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			handler := observedHTTPHandler(mcpHandler, token, serveHTTPMaxBodyMB*1024*1024, metrics, logger)
			mux := http.NewServeMux()
			mux.Handle("/mcp", handler)
			mux.Handle("/metrics", metricsHTTPHandler(metrics, token))
			fmt.Fprintf(os.Stderr, "onboard MCP server listening on http://%s/mcp (metrics: http://%s/metrics)\n", serveHTTP, serveHTTP)
			srv := &http.Server{
				Addr:              serveHTTP,
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

		return s.Run(ctx, &mcp.StdioTransport{})
	},
}

func hardenHTTPHandler(next http.Handler, bearerToken string, maxBodyBytes int64) http.Handler {
	return observedHTTPHandler(next, bearerToken, maxBodyBytes, nil, nil)
}

type httpMetrics struct {
	requestsTotal      atomic.Int64
	unauthorizedTotal  atomic.Int64
	durationNanosTotal atomic.Int64
}

func observedHTTPHandler(next http.Handler, bearerToken string, maxBodyBytes int64, metrics *httpMetrics, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if metrics != nil {
			metrics.requestsTotal.Add(1)
		}
		defer func() {
			elapsed := time.Since(start)
			if metrics != nil {
				metrics.durationNanosTotal.Add(elapsed.Nanoseconds())
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
		if bearerToken != "" && !validBearer(r, bearerToken) {
			if metrics != nil {
				metrics.unauthorizedTotal.Add(1)
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

func metricsHTTPHandler(metrics *httpMetrics, bearerToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearerToken != "" && !validBearer(r, bearerToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "onboard_http_requests_total %d\n", metrics.requestsTotal.Load())
		fmt.Fprintf(w, "onboard_http_unauthorized_total %d\n", metrics.unauthorizedTotal.Load())
		fmt.Fprintf(w, "onboard_http_request_duration_seconds_total %.6f\n", float64(metrics.durationNanosTotal.Load())/float64(time.Second))
	})
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

func init() {
	serveCmd.Flags().StringVar(&serveHTTP, "http", "", "serve over Streamable HTTP on this address (e.g. 127.0.0.1:8080) instead of stdio")
	serveCmd.Flags().StringVar(&serveHTTPToken, "http-token", "", "require this bearer token for Streamable HTTP (or set ONBOARD_HTTP_TOKEN)")
	serveCmd.Flags().Int64Var(&serveHTTPMaxBodyMB, "http-max-body-mb", 10, "maximum Streamable HTTP request body size in MiB")
	rootCmd.AddCommand(serveCmd)
}
