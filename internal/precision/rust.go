package precision

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

const (
	rustPrecisionTimeout = 75 * time.Second
	maxRustPreciseSyms   = 300
	maxLSPBodyBytes      = 64 << 20
)

// EnrichRust augments g in place with rust-analyzer call-hierarchy edges when the
// rust-analyzer binary and a Cargo project are available. Like EnrichGo, it is strictly
// additive and non-fatal: any LSP, toolchain, indexing, or mapping failure leaves the
// syntactic graph intact.
func EnrichRust(ctx context.Context, root string, g *providers.Graph) (int, error) {
	if g == nil || len(g.Defs) == 0 || !providers.GraphHasLang(g, "rust") {
		return 0, nil
	}
	if ok, reason := rustAnalyzerStatus(root); !ok {
		g.AddPrecisionNote("Rust semantic precision unavailable: " + reason + ".")
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, rustPrecisionTimeout)
	defer cancel()

	edges, stats := rustAnalyzerEdges(ctx, root, g)
	if stats.Truncated {
		g.AddPrecisionNote(fmt.Sprintf("Rust semantic precision queried %d of %d callable symbols; remaining symbols stayed syntactic.", stats.Queried, stats.Total))
	}
	if stats.Failure != "" {
		g.AddPrecisionNote("Rust semantic precision failed: " + stats.Failure + ".")
		return 0, nil
	}
	if len(edges) == 0 {
		g.AddPrecisionNote("Rust semantic precision ran but returned zero call hierarchy edges.")
		return 0, nil
	}

	byPos := make(map[string]string, len(g.Defs))
	byFileLine := make(map[string][]string, len(g.Defs))
	for q, s := range g.Defs {
		if s == nil {
			continue
		}
		if !strings.EqualFold(s.Lang, "rust") {
			continue
		}
		f := filepath.ToSlash(s.File)
		byPos[providers.PosKey(f, s.Line, s.Name)] = q
		byFileLine[f+"\x00"+strconv.Itoa(s.Line)] = append(byFileLine[f+"\x00"+strconv.Itoa(s.Line)], q)
	}
	resolve := func(absFile string, line int, name string) (string, bool) {
		rel, err := filepath.Rel(root, absFile)
		if err != nil {
			return "", false
		}
		f := filepath.ToSlash(rel)
		if q, ok := byPos[providers.PosKey(f, line, name)]; ok {
			return q, true
		}
		if qs := byFileLine[f+"\x00"+strconv.Itoa(line)]; len(qs) == 1 {
			return qs[0], true
		}
		return "", false
	}

	if g.ProvenEdges == nil {
		g.ProvenEdges = map[string]bool{}
	}
	edgesSeen := providers.EdgeSetFromGraph(g)
	added := 0
	proved := false
	for _, e := range edges {
		caller, ok := resolve(e.callerFile, e.callerLine, e.callerName)
		if !ok {
			continue
		}
		callee, ok := resolve(e.calleeFile, e.calleeLine, e.calleeName)
		if !ok || caller == callee {
			continue
		}
		key := providers.EdgeKey(caller, callee)
		if g.ProvenEdges[key] {
			continue
		}
		proved = true
		g.ProvenEdges[key] = true
		if edgesSeen.Add(g, caller, callee) {
			added++
		}
	}
	if proved {
		g.MarkPrecision("rust-analyzer")
	} else {
		g.AddPrecisionNote("Rust semantic precision returned call hierarchy edges, but none mapped back to indexed symbols.")
	}
	return added, nil
}

type rustAnalyzerStats struct {
	Total     int
	Queried   int
	Truncated bool
	Failure   string
}

type rustPreciseEdge struct {
	callerFile string
	callerLine int
	callerName string
	calleeFile string
	calleeLine int
	calleeName string
}

func rustAnalyzerEdges(ctx context.Context, root string, g *providers.Graph) (out []rustPreciseEdge, stats rustAnalyzerStats) {
	defer func() {
		if v := recover(); v != nil {
			out = nil
			stats.Failure = fmt.Sprint(v)
		}
	}()
	client, err := newRustAnalyzerClient(ctx, root)
	if err != nil {
		stats.Failure = rustPrecisionFailure(err)
		return nil, stats
	}
	defer client.close()

	qnames, total, truncated := rustCallableQNames(g)
	stats.Total = total
	stats.Queried = len(qnames)
	stats.Truncated = truncated

	opened := map[string]bool{}
	for _, q := range qnames {
		s := g.Defs[q]
		if s == nil {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(s.File))
		if !opened[abs] {
			client.openDocument(abs)
			opened[abs] = true
		}
		item, ok := client.prepareCallHierarchy(ctx, abs, s.Line, s.Column)
		if !ok {
			if err := ctx.Err(); err != nil {
				stats.Failure = rustPrecisionFailure(err)
				return out, stats
			}
			continue
		}
		calls, ok := client.outgoingCalls(ctx, item)
		if !ok {
			if err := ctx.Err(); err != nil {
				stats.Failure = rustPrecisionFailure(err)
				return out, stats
			}
			continue
		}
		for _, call := range calls {
			calleeFile := filePathFromURI(call.To.URI)
			if calleeFile == "" {
				continue
			}
			out = append(out, rustPreciseEdge{
				callerFile: abs,
				callerLine: s.Line,
				callerName: s.Name,
				calleeFile: calleeFile,
				calleeLine: call.To.SelectionRange.Start.Line + 1,
				calleeName: call.To.Name,
			})
		}
	}
	if err := ctx.Err(); err != nil {
		stats.Failure = rustPrecisionFailure(err)
	}
	return out, stats
}

func rustPrecisionFailure(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "rust-analyzer timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "rust-analyzer request was canceled"
	}
	return err.Error()
}

func rustCallableQNames(g *providers.Graph) ([]string, int, bool) {
	type candidate struct {
		qname   string
		rank    float64
		impact  int
		callers int
	}
	var candidates []candidate
	if g != nil {
		pr := g.PageRank(nil)
		for q, s := range g.Defs {
			if s == nil {
				continue
			}
			if strings.EqualFold(s.Lang, "rust") && (s.Kind == "function" || s.Kind == "method") {
				candidates = append(candidates, candidate{
					qname:   q,
					rank:    pr[q],
					impact:  len(g.Impact(q)),
					callers: len(g.Reverse[q]),
				})
			}
		}
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		if c := compareFloatDesc(a.rank, b.rank); c != 0 {
			return c
		}
		if c := cmp.Compare(b.impact, a.impact); c != 0 {
			return c
		}
		if c := cmp.Compare(b.callers, a.callers); c != 0 {
			return c
		}
		return cmp.Compare(a.qname, b.qname)
	})
	total := len(candidates)
	if len(candidates) > maxRustPreciseSyms {
		candidates = candidates[:maxRustPreciseSyms]
	}
	qnames := make([]string, 0, len(candidates))
	for _, c := range candidates {
		qnames = append(qnames, c.qname)
	}
	truncated := total > len(qnames)
	return qnames, total, truncated
}

func compareFloatDesc(a, b float64) int {
	switch {
	case a > b:
		return -1
	case b > a:
		return 1
	default:
		return 0
	}
}

type rustAnalyzerClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

func newRustAnalyzerClient(ctx context.Context, root string) (*rustAnalyzerClient, error) {
	cmd := exec.CommandContext(ctx, "rust-analyzer")
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &rustAnalyzerClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	params := map[string]any{
		"processId": nil,
		"rootUri":   fileURI(root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"callHierarchy": map[string]any{"dynamicRegistration": false},
			},
		},
		"initializationOptions": map[string]any{
			"cargo":       map[string]any{"noDeps": true},
			"checkOnSave": false,
		},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		c.close()
		return nil, err
	}
	_ = c.notify("initialized", map[string]any{})
	return c, nil
}

func (c *rustAnalyzerClient) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.nextID++
	_ = c.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  "shutdown",
	})
	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-ctx.Done():
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-done
	}
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type callHierarchyItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	URI            string   `json:"uri"`
	Range          lspRange `json:"range"`
	SelectionRange lspRange `json:"selectionRange"`
}

type outgoingCall struct {
	To         callHierarchyItem `json:"to"`
	FromRanges []lspRange        `json:"fromRanges"`
}

func (c *rustAnalyzerClient) prepareCallHierarchy(ctx context.Context, absFile string, line, col int) (callHierarchyItem, bool) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": fileURI(absFile)},
		"position":     lspPosition{Line: line - 1, Character: utf16ColumnForFile(absFile, line, col)},
	}
	var items []callHierarchyItem
	if err := c.call(ctx, "textDocument/prepareCallHierarchy", params, &items); err != nil || len(items) == 0 {
		return callHierarchyItem{}, false
	}
	return items[0], true
}

func utf16ColumnForFile(absFile string, line, byteCol int) int {
	data, err := os.ReadFile(absFile)
	if err != nil || line <= 0 || byteCol <= 0 {
		return byteCol
	}
	lineStart := 0
	currentLine := 1
	for i, b := range data {
		if currentLine == line {
			lineStart = i
			break
		}
		if b == '\n' {
			currentLine++
		}
	}
	if currentLine != line {
		return byteCol
	}
	lineEnd := len(data)
	if next := bytes.IndexByte(data[lineStart:], '\n'); next >= 0 {
		lineEnd = lineStart + next
	}
	if byteCol > lineEnd-lineStart {
		byteCol = lineEnd - lineStart
	}
	units := 0
	for _, r := range string(data[lineStart : lineStart+byteCol]) {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func (c *rustAnalyzerClient) outgoingCalls(ctx context.Context, item callHierarchyItem) ([]outgoingCall, bool) {
	var calls []outgoingCall
	if err := c.call(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": item}, &calls); err != nil {
		return nil, false
	}
	return calls, true
}

func (c *rustAnalyzerClient) openDocument(absFile string) {
	data, err := os.ReadFile(absFile)
	if err != nil {
		return
	}
	_ = c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI(absFile),
			"languageId": "rust",
			"version":    1,
			"text":       string(data),
		},
	})
}

func (c *rustAnalyzerClient) call(ctx context.Context, method string, params any, result any) error {
	c.nextID++
	id := c.nextID
	if err := c.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, err := c.read()
		if err != nil {
			return err
		}
		if msg.ID == nil || *msg.ID != id {
			continue // notification or another response; this client is serial, so ignore it.
		}
		if msg.Error != nil {
			return errors.New(msg.Error.Message)
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

func (c *rustAnalyzerClient) notify(method string, params any) error {
	return c.send(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *rustAnalyzerClient) send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)))
	if _, err := c.stdin.Write(header); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

type lspMessage struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *rustAnalyzerClient) read() (lspMessage, error) {
	length := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return lspMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return lspMessage{}, err
			}
			length = n
		}
	}
	if length < 0 {
		return lspMessage{}, errors.New("missing lsp content-length")
	}
	if length <= 0 || length > maxLSPBodyBytes {
		return lspMessage{}, fmt.Errorf("lsp message content-length %d out of bounds", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.stdout, body); err != nil {
		return lspMessage{}, err
	}
	var raw struct {
		ID     json.RawMessage `json:"id,omitempty"`
		Method string          `json:"method,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return lspMessage{}, err
	}
	var id *int
	if len(raw.ID) > 0 && !bytes.Equal(raw.ID, []byte("null")) {
		var n int
		if err := json.Unmarshal(raw.ID, &n); err == nil {
			id = &n
		}
	}
	return lspMessage{ID: id, Method: raw.Method, Result: raw.Result, Error: raw.Error}, nil
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

func filePathFromURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.Clean(u.Path)
}

// RustAnalyzerAvailable reports whether rust-analyzer can run for root.
func RustAnalyzerAvailable(root string) bool {
	ok, _ := rustAnalyzerStatus(root)
	return ok
}

func rustAnalyzerStatus(root string) (bool, string) {
	if _, err := exec.LookPath("rust-analyzer"); err != nil {
		return false, "rust-analyzer binary was not found on PATH"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "rust-analyzer", "--version").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return false, "`rust-analyzer --version` failed: " + msg
	}
	dir := root
	for {
		if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
			return true, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, "no Cargo.toml was found at or above the repo root"
		}
		dir = parent
	}
}
