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
	nodesJSON, _ := json.Marshal(nodes)
	edgesJSON, _ := json.Marshal(edges)
	mermaidJSON, _ := json.Marshal(src.String())

	r := strings.NewReplacer(
		"__TOPIC__", htmlEscape(topic),
		"__MERMAID__", string(mermaidJSON),
		"__NODES__", string(nodesJSON),
		"__EDGES__", string(edgesJSON),
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

// mapHTMLTemplate is a self-contained interactive map with no network dependencies.
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
  header { display:flex; align-items:center; gap:10px; padding: 10px 14px 10px 18px; border-bottom: 1px solid #21262d; font-weight: 600; font-size: 15px; }
  header .spacer { flex:1; }
  button { border:1px solid #30363d; background:#161b22; color:#e6edf3; border-radius:6px; font:12px -apple-system, system-ui, sans-serif; padding:5px 9px; cursor:pointer; }
  button:hover { background:#21262d; }
  #wrap { display: flex; height: calc(100vh - 46px); }
  #diagram { flex: 1; overflow: hidden; background:#0d1117; cursor:grab; }
  #diagram.dragging { cursor:grabbing; }
  #diagram svg { width: 100%; height: 100%; display:block; }
  .edge { stroke:#6e7681; stroke-width:1.5; }
  .edge-label { fill:#9da7b3; font:11px ui-monospace, monospace; pointer-events:none; }
  .node rect { fill:#161b22; stroke:#58a6ff; stroke-width:1.2; rx:6; }
  .node text { fill:#e6edf3; font:13px -apple-system, system-ui, sans-serif; pointer-events:none; }
  .node:hover rect { fill:#1f2937; stroke:#79c0ff; }
  #panel { width: 340px; border-left: 1px solid #21262d; padding: 16px; overflow:auto; background:#0b0f14; }
  #panel h2 { font-size: 14px; margin: 0 0 8px; color:#58a6ff; word-break: break-all; }
  #panel .desc { color:#9da7b3; margin-bottom: 12px; }
  #panel .files { list-style: none; padding: 0; margin: 0; }
  #panel .files li { font-family: ui-monospace, monospace; font-size: 12px; padding: 3px 0; border-top: 1px solid #161b22; word-break: break-all; }
  #panel .hint { color:#6e7681; }
  #source { display:none; }
</style>
</head>
<body>
<header><span>__TOPIC__</span><span class="spacer"></span><button id="reset" type="button" title="Reset pan and zoom">Reset</button></header>
<div id="wrap">
  <div id="diagram">
    <svg id="svg" role="img" aria-label="__TOPIC__">
      <defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#6e7681"></path></marker></defs>
      <g id="viewport"></g>
    </svg>
  </div>
  <aside id="panel">
    <h2 id="ptitle">Codebase map</h2>
    <div id="pbody" class="hint">Click a node to see its description and files. Scroll to zoom, drag to pan.</div>
  </aside>
</div>
<script id="source" type="application/json">__MERMAID__</script>
<script>
const DETAILS = __DETAILS__;
const NODES = __NODES__;
const EDGES = __EDGES__;
const NS = 'http://www.w3.org/2000/svg';
const svg = document.getElementById('svg');
const diagram = document.getElementById('diagram');
const viewport = document.getElementById('viewport');

function el(name, attrs) {
  const node = document.createElementNS(NS, name);
  for (const [k, v] of Object.entries(attrs || {})) node.setAttribute(k, v);
  return node;
}

function trimLabel(s, max) {
  s = String(s || '');
  return s.length > max ? s.slice(0, max - 1) + '…' : s;
}

function layout(nodes, edges) {
  const ids = new Set(nodes.map(n => n.id));
  const indegree = new Map(nodes.map(n => [n.id, 0]));
  const outgoing = new Map(nodes.map(n => [n.id, []]));
  for (const e of edges) {
    if (!ids.has(e.from) || !ids.has(e.to)) continue;
    indegree.set(e.to, (indegree.get(e.to) || 0) + 1);
    outgoing.get(e.from).push(e.to);
  }
  const depth = new Map();
  const queue = nodes.filter(n => (indegree.get(n.id) || 0) === 0).map(n => n.id);
  for (const id of queue) depth.set(id, 0);
  for (let i = 0; i < queue.length; i++) {
    const id = queue[i];
    for (const to of outgoing.get(id) || []) {
      const next = (depth.get(id) || 0) + 1;
      if (!depth.has(to) || next > depth.get(to)) depth.set(to, next);
      indegree.set(to, indegree.get(to) - 1);
      if (indegree.get(to) === 0) queue.push(to);
    }
  }
  for (const n of nodes) if (!depth.has(n.id)) depth.set(n.id, 0);
  const groups = new Map();
  for (const n of nodes) {
    const d = depth.get(n.id) || 0;
    if (!groups.has(d)) groups.set(d, []);
    groups.get(d).push(n);
  }
  const pos = new Map();
  let maxX = 0, maxY = 0;
  for (const [d, group] of [...groups.entries()].sort((a, b) => a[0] - b[0])) {
    group.sort((a, b) => String(a.label || a.id).localeCompare(String(b.label || b.id)));
    group.forEach((n, row) => {
      const x = 70 + d * 260;
      const y = 70 + row * 105;
      pos.set(n.id, { x, y, w: 190, h: 56 });
      maxX = Math.max(maxX, x + 240);
      maxY = Math.max(maxY, y + 110);
    });
  }
  svg.setAttribute('viewBox', '0 0 ' + Math.max(maxX, 640) + ' ' + Math.max(maxY, 420));
  return pos;
}

function draw() {
  const pos = layout(NODES, EDGES);
  for (const e of EDGES) {
    const from = pos.get(e.from), to = pos.get(e.to);
    if (!from || !to) continue;
    const x1 = from.x + from.w, y1 = from.y + from.h / 2;
    const x2 = to.x, y2 = to.y + to.h / 2;
    viewport.appendChild(el('line', { class: 'edge', x1, y1, x2, y2, 'marker-end': 'url(#arrow)' }));
    if (e.label) {
      const text = el('text', { class: 'edge-label', x: (x1 + x2) / 2, y: (y1 + y2) / 2 - 6, 'text-anchor': 'middle' });
      text.textContent = trimLabel(e.label, 24);
      viewport.appendChild(text);
    }
  }
  for (const n of NODES) {
    const p = pos.get(n.id);
    const g = el('g', { class: 'node', tabindex: '0', role: 'button' });
    g.setAttribute('transform', 'translate(' + p.x + ' ' + p.y + ')');
    g.addEventListener('click', () => showNode(n.id));
    g.addEventListener('keydown', ev => { if (ev.key === 'Enter' || ev.key === ' ') showNode(n.id); });
    const title = el('title'); title.textContent = n.label || n.id; g.appendChild(title);
    g.appendChild(el('rect', { width: p.w, height: p.h }));
    const text = el('text', { x: 14, y: 33 });
    text.textContent = trimLabel(n.label || n.id, 26);
    g.appendChild(text);
    viewport.appendChild(g);
  }
}

function showNode(id) {
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
}

let scale = 1, tx = 0, ty = 0, dragging = false, lastX = 0, lastY = 0;
function applyTransform() { viewport.setAttribute('transform', 'translate(' + tx + ' ' + ty + ') scale(' + scale + ')'); }
function resetView() { scale = 1; tx = 0; ty = 0; applyTransform(); }
svg.addEventListener('wheel', ev => {
  ev.preventDefault();
  const rect = svg.getBoundingClientRect();
  const px = ev.clientX - rect.left;
  const py = ev.clientY - rect.top;
  const next = Math.min(6, Math.max(0.25, scale * (ev.deltaY < 0 ? 1.12 : 0.88)));
  const ratio = next / scale;
  tx = px - (px - tx) * ratio;
  ty = py - (py - ty) * ratio;
  scale = next;
  applyTransform();
}, { passive: false });
svg.addEventListener('pointerdown', ev => { dragging = true; lastX = ev.clientX; lastY = ev.clientY; diagram.classList.add('dragging'); svg.setPointerCapture(ev.pointerId); });
svg.addEventListener('pointermove', ev => {
  if (!dragging) return;
  tx += ev.clientX - lastX;
  ty += ev.clientY - lastY;
  lastX = ev.clientX;
  lastY = ev.clientY;
  applyTransform();
});
svg.addEventListener('pointerup', ev => { dragging = false; diagram.classList.remove('dragging'); svg.releasePointerCapture(ev.pointerId); });
svg.addEventListener('pointercancel', () => { dragging = false; diagram.classList.remove('dragging'); });
document.getElementById('reset').addEventListener('click', resetView);
draw();
applyTransform();
</script>
</body>
</html>
`
