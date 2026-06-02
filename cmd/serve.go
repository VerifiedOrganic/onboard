package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/server"
)

var serveHTTP string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the MCP server (stdio by default, or Streamable HTTP with --http)",
	Long: `Starts the onboard MCP server.

By default it speaks over stdin/stdout — this is what an agent launches. With
--http it serves the same tools, resources, and prompts over Streamable HTTP at
/mcp, for hosted or headless/CI use.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		s := server.New(version)

		if serveHTTP != "" {
			handler := mcp.NewStreamableHTTPHandler(
				func(*http.Request) *mcp.Server { return s },
				nil, // default options: stateful sessions, localhost protection on
			)
			mux := http.NewServeMux()
			mux.Handle("/mcp", handler)
			fmt.Fprintf(os.Stderr, "onboard MCP server listening on http://%s/mcp\n", serveHTTP)
			srv := &http.Server{
				Addr:              serveHTTP,
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
			}
			return srv.ListenAndServe()
		}

		return s.Run(context.Background(), &mcp.StdioTransport{})
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveHTTP, "http", "", "serve over Streamable HTTP on this address (e.g. :8080) instead of stdio")
	rootCmd.AddCommand(serveCmd)
}
