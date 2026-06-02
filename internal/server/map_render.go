package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Presentation layer for render_map: turning the abstract node/edge model into committable
// Mermaid or a self-contained interactive HTML page, plus the identifier/label sanitizers
// that keep both safe. The tool handler and the graph-derivation that produce the model live
// in tools_map.go.

var idUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]`)

func sanitizeID(s string) string {
	out := idUnsafe.ReplaceAllString(s, "_")
	if out == "" {
		out = "n"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "n" + out
	}
	return out
}

// mermaidLabel makes a string safe to embed in a Mermaid node label. The map is
// rendered with securityLevel 'loose' (required for click callbacks), so strip the
// HTML/markup metacharacters that could otherwise inject into the rendered SVG.
// Node labels are concept names and file paths, so this loses nothing meaningful.
func mermaidLabel(s string) string {
	return strings.NewReplacer(
		`"`, "'",
		"<", "",
		">", "",
		"`", "'",
		"\r", " ",
		"\n", " ",
	).Replace(s)
}

func renderMermaid(topic string, nodes []mapNode, edges []mapEdge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%%%% %s\nflowchart LR\n", topic)
	for _, n := range nodes {
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", n.ID, mermaidLabel(n.Label))
	}
	for _, e := range edges {
		if e.Label != "" {
			fmt.Fprintf(&b, "  %s -->|\"%s\"| %s\n", e.From, mermaidLabel(e.Label), e.To)
		} else {
			fmt.Fprintf(&b, "  %s --> %s\n", e.From, e.To)
		}
	}
	// Legend mapping node ids to files, as comments (safe to keep in source).
	b.WriteString("\n%% legend:\n")
	for _, n := range nodes {
		if len(n.Files) > 0 {
			fmt.Fprintf(&b, "%%%%   %s = %s\n", n.ID, strings.Join(n.Files, ", "))
		}
	}
	return b.String()
}

func renderMapHTML(topic string, nodes []mapNode, edges []mapEdge) string {
	var src strings.Builder
	src.WriteString("flowchart LR\n")
	for _, n := range nodes {
		fmt.Fprintf(&src, "  %s[\"%s\"]\n", n.ID, mermaidLabel(n.Label))
	}
	for _, e := range edges {
		if e.Label != "" {
			fmt.Fprintf(&src, "  %s -->|\"%s\"| %s\n", e.From, mermaidLabel(e.Label), e.To)
		} else {
			fmt.Fprintf(&src, "  %s --> %s\n", e.From, e.To)
		}
	}
	for _, n := range nodes {
		fmt.Fprintf(&src, "  click %s call onNode(\"%s\")\n", n.ID, n.ID)
	}

	details := map[string]mapNode{}
	for _, n := range nodes {
		details[n.ID] = n
	}
	detailsJSON, _ := json.Marshal(details)

	r := strings.NewReplacer(
		"__TOPIC__", htmlEscape(topic),
		"__MERMAID__", src.String(),
		"__DETAILS__", string(detailsJSON),
	)
	return r.Replace(mapHTMLTemplate)
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// mapHTMLTemplate is a self-contained interactive map. CDN deps only; no build step.
const mapHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TOPIC__</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; font: 14px/1.5 -apple-system, system-ui, sans-serif; background:#0d1117; color:#e6edf3; }
  header { padding: 12px 18px; border-bottom: 1px solid #21262d; font-weight: 600; font-size: 15px; }
  #wrap { display: flex; height: calc(100vh - 46px); }
  #diagram { flex: 1; overflow: hidden; }
  #diagram svg { width: 100%; height: 100%; }
  #panel { width: 340px; border-left: 1px solid #21262d; padding: 16px; overflow:auto; background:#0b0f14; }
  #panel h2 { font-size: 14px; margin: 0 0 8px; color:#58a6ff; word-break: break-all; }
  #panel .desc { color:#9da7b3; margin-bottom: 12px; }
  #panel .files { list-style: none; padding: 0; margin: 0; }
  #panel .files li { font-family: ui-monospace, monospace; font-size: 12px; padding: 3px 0; border-top: 1px solid #161b22; word-break: break-all; }
  #panel .hint { color:#6e7681; }
</style>
</head>
<body>
<header>__TOPIC__</header>
<div id="wrap">
  <div id="diagram"><pre class="mermaid">__MERMAID__</pre></div>
  <aside id="panel">
    <h2 id="ptitle">Codebase map</h2>
    <div id="pbody" class="hint">Click a node to see its description and files. Scroll to zoom, drag to pan.</div>
  </aside>
</div>
<script src="https://cdn.jsdelivr.net/npm/svg-pan-zoom@3.6.1/dist/svg-pan-zoom.min.js"></script>
<script type="module">
import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';
const DETAILS = __DETAILS__;
window.onNode = function (id) {
  const d = DETAILS[id];
  const title = document.getElementById('ptitle');
  const body = document.getElementById('pbody');
  body.className = '';
  if (!d) { title.textContent = id; body.textContent = ''; return; }
  title.textContent = d.label || id;
  body.innerHTML = '';
  if (d.description) {
    const p = document.createElement('div'); p.className = 'desc'; p.textContent = d.description; body.appendChild(p);
  }
  const files = d.files || [];
  if (files.length) {
    const ul = document.createElement('ul'); ul.className = 'files';
    for (const f of files) { const li = document.createElement('li'); li.textContent = f; ul.appendChild(li); }
    body.appendChild(ul);
  }
};
mermaid.initialize({ startOnLoad: false, securityLevel: 'loose', theme: 'dark' });
await mermaid.run({ querySelector: '.mermaid' });
const svg = document.querySelector('#diagram svg');
if (svg && window.svgPanZoom) {
  svg.removeAttribute('style'); svg.style.width = '100%'; svg.style.height = '100%';
  svgPanZoom(svg, { controlIconsEnabled: true, fit: true, center: true, minZoom: 0.2, maxZoom: 20 });
}
</script>
</body>
</html>
`
