package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/server"
	"github.com/VerifiedOrganic/onboard/internal/transport"
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
and network controls. Set --http-token or ONBOARD_HTTP_TOKEN to require a bearer token.
Setting ONBOARD_ALLOWED_ROOT (comma-separated paths) restricts tool roots in both stdio and HTTP modes.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var opts []server.Option
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory for http root policy: %w", err)
		}
		opts = append(opts, rootPolicyOptions(serveHTTP != "", os.Getenv("ONBOARD_ALLOWED_ROOT"), wd)...)
		if serveHTTP != "" {
			opts = append(opts, server.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))))
		}
		s := server.New(version, opts...)

		if serveHTTP != "" {
			token := serveHTTPToken
			if token == "" {
				token = os.Getenv("ONBOARD_HTTP_TOKEN")
			}
			return transport.ServeHTTP(ctx, s, transport.Config{
				Addr:         serveHTTP,
				Token:        token,
				MaxBodyBytes: serveHTTPMaxBodyMB * 1024 * 1024,
			})
		}

		return s.Run(ctx, &mcp.StdioTransport{})
	},
}

// rootPolicyOptions enforces the root allowlist: always in HTTP mode, and in
// stdio mode whenever ONBOARD_ALLOWED_ROOT is explicitly set.
func rootPolicyOptions(httpMode bool, allowedRootEnv, wd string) []server.Option {
	if !httpMode && strings.TrimSpace(allowedRootEnv) == "" {
		return nil
	}
	return []server.Option{server.WithRootPolicy(transport.RootPolicyFromEnv(wd))}
}

func init() {
	serveCmd.Flags().StringVar(&serveHTTP, "http", "", "serve over Streamable HTTP on this address (e.g. 127.0.0.1:8080) instead of stdio")
	serveCmd.Flags().StringVar(&serveHTTPToken, "http-token", "", "require this bearer token for Streamable HTTP (or set ONBOARD_HTTP_TOKEN)")
	serveCmd.Flags().Int64Var(&serveHTTPMaxBodyMB, "http-max-body-mb", 10, "maximum Streamable HTTP request body size in MiB")
	rootCmd.AddCommand(serveCmd)
}
