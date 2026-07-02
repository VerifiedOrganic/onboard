# CI Recipes

`onboard serve --http` lets a CI job drive the same MCP tools an agent uses locally. The
example below calls `explain_diff` against a pull request base ref and records the raw JSON
result as a build artifact or job summary.

## Raw Streamable HTTP in 4 curl calls

Start the server against the repository you want to inspect:

```sh
./onboard serve --http 127.0.0.1:8080 --http-token demo
```

Use the MCP Streamable HTTP endpoint at `/mcp`. Every request needs the bearer token and
the Streamable HTTP `Accept` header.

```sh
BASE_URL=http://127.0.0.1:8080/mcp
TOKEN=demo
REPO=/path/to/repo

curl -sS -D /tmp/onboard-init.headers -o /tmp/onboard-init.body \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/json, text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0.1.0"}}}' \
  "$BASE_URL"

SESSION_ID=$(awk 'BEGIN{IGNORECASE=1} /^Mcp-Session-Id:/ {gsub("\r", "", $2); print $2}' /tmp/onboard-init.headers)
test -n "$SESSION_ID"

curl -sS -o /tmp/onboard-initialized.body \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H "Accept: application/json, text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
  "$BASE_URL"

curl -sS -D /tmp/onboard-explain.headers -o /tmp/onboard-explain.body \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H "Accept: application/json, text/event-stream" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"explain_diff\",\"arguments\":{\"root\":\"$REPO\",\"base\":\"origin/main\"}}}" \
  "$BASE_URL"
```

What you should see, from a local run against this repository:

```text
HTTP/1.1 200 OK
Content-Type: text/event-stream
Mcp-Session-Id: D26PEODPKVR7MAAFDZ7OTYSVBP

event: message
data: {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"at_risk_tests\":[\"cmd/serve_test.go::(top-level)\",\"internal/git/git_test.go::TestDiffNameStatus\",...],\"base\":\"origin/main\",\"changed_files\":[{\"hunks\":1,\"path\":\".github/workflows/ci.yml\",\"status\":\"M\"},{\"hunks\":1,\"path\":\"CHANGELOG.md\",\"status\":\"A\"},...],\"provider\":\"builtin\",\"truncated\":true}"}]}}
```

The response body is Server-Sent Events. The JSON-RPC result is inside the `data:` line; the
tool's structured payload is the JSON string in `result.content[0].text`.

## GitHub Actions example

```yaml
name: onboard explain diff

on:
  pull_request:

permissions:
  contents: read

jobs:
  explain-diff:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
      - name: Build onboard
        run: go build -o onboard .
      - name: Run explain_diff over Streamable HTTP
        env:
          TOKEN: ${{ github.token }}
          BASE_REF: ${{ github.event.pull_request.base.sha }}
        run: |
          ./onboard serve --http 127.0.0.1:8080 --http-token "$TOKEN" >onboard-http.log 2>&1 &
          ONBOARD_PID=$!
          trap 'kill "$ONBOARD_PID"' EXIT

          for _ in $(seq 1 40); do
            if curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/metrics >/dev/null; then
              break
            fi
            sleep 0.25
          done

          BASE_URL=http://127.0.0.1:8080/mcp
          curl -sS -D init.headers -o init.body \
            -H "Authorization: Bearer $TOKEN" \
            -H "Accept: application/json, text/event-stream" \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"github-actions","version":"1"}}}' \
            "$BASE_URL"

          SESSION_ID=$(awk 'BEGIN{IGNORECASE=1} /^Mcp-Session-Id:/ {gsub("\r", "", $2); print $2}' init.headers)
          test -n "$SESSION_ID"

          curl -sS -o initialized.body \
            -H "Authorization: Bearer $TOKEN" \
            -H "Mcp-Session-Id: $SESSION_ID" \
            -H "Accept: application/json, text/event-stream" \
            -H "Content-Type: application/json" \
            -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
            "$BASE_URL"

          cat > explain-request.json <<JSON
          {"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"explain_diff","arguments":{"root":"$GITHUB_WORKSPACE","base":"$BASE_REF"}}}
          JSON

          curl -sS -D explain.headers -o explain.body \
            -H "Authorization: Bearer $TOKEN" \
            -H "Mcp-Session-Id: $SESSION_ID" \
            -H "Accept: application/json, text/event-stream" \
            -H "Content-Type: application/json" \
            --data @explain-request.json \
            "$BASE_URL"

          {
            echo "## onboard explain_diff"
            echo
            echo '```text'
            cat explain.body
            echo '```'
          } >> "$GITHUB_STEP_SUMMARY"
```
