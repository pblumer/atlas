from pathlib import Path
import re


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"expected patch anchor not found in {path}: {old[:80]!r}")
    p.write_text(text.replace(old, new, 1))


replace_once(
    "api/panorama/query_types.go",
    '\tMaxTokens        int `json:"maxTokens"`\n\tMaxPathDepth     int `json:"maxPathDepth"`\n\tMaxVisitedEdges  int `json:"maxVisitedEdges"`',
    '\tMaxTokens        int `json:"maxTokens"`\n\tMaxASTNodes      int `json:"maxASTNodes"`\n\tMaxPathDepth     int `json:"maxPathDepth"`\n\tMaxVisitedNodes  int `json:"maxVisitedNodes"`\n\tMaxVisitedEdges  int `json:"maxVisitedEdges"`',
)
replace_once(
    "api/panorama/query_types.go",
    "\t\tMaxTokens:        512,\n\t\tMaxPathDepth:     8,\n\t\tMaxVisitedEdges:  50_000,",
    "\t\tMaxTokens:        512,\n\t\tMaxASTNodes:      256,\n\t\tMaxPathDepth:     8,\n\t\tMaxVisitedNodes:  50_000,\n\t\tMaxVisitedEdges:  50_000,",
)

replace_once(
    "api/panorama/query_parse.go",
    "\tp := queryParser{source: source, tokens: tokens}\n\treturn p.parse()\n}\n\nfunc validatePlan",
    '''\tp := queryParser{source: source, tokens: tokens}
\tplan, err := p.parse()
\tif err != nil {
\t\treturn queryPlan{}, err
\t}
\tif limits.MaxASTNodes > 0 && queryPlanComplexity(plan) > limits.MaxASTNodes {
\t\treturn queryPlan{}, newQueryError("ast_limit", "query exceeds maximum AST complexity", 0, source)
\t}
\treturn plan, nil
}

func queryPlanComplexity(plan queryPlan) int {
\tn := 2 + len(plan.returns)
\tif plan.edge != nil {
\t\tn += 2
\t}
\tif plan.where != nil {
\t\tn += queryExprComplexity(plan.where)
\t}
\tif plan.order != nil {
\t\tn++
\t}
\tif plan.limit >= 0 {
\t\tn++
\t}
\treturn n
}

func queryExprComplexity(e expr) int {
\tswitch v := e.(type) {
\tcase boolExpr:
\t\treturn 1 + queryExprComplexity(v.left) + queryExprComplexity(v.right)
\tcase notExpr:
\t\treturn 1 + queryExprComplexity(v.inner)
\tcase compareExpr:
\t\treturn 1
\tdefault:
\t\treturn 1
\t}
}

func validatePlan''',
)

replace_once(
    "api/panorama/query_eval.go",
    "\tmatches := make([]match, 0)\n\tif plan.edge == nil {\n\t\tfor i := range graph.Nodes {",
    '''\tmatches := make([]match, 0)
\tvisitedNodes := 0
\tif plan.edge == nil {
\t\tfor i := range graph.Nodes {
\t\t\tvisitedNodes++
\t\t\tif limits.MaxVisitedNodes > 0 && visitedNodes > limits.MaxVisitedNodes {
\t\t\t\treturn nil, newQueryError("visited_node_limit", "query exceeded maximum visited nodes", 0, source)
\t\t\t}''',
)
replace_once(
    "api/panorama/query_eval.go",
    "\t\twalk = func(current, depth int) error {\n\t\t\tif err := checkQueryContext(ctx, source); err != nil {\n\t\t\t\treturn err\n\t\t\t}",
    '''\t\twalk = func(current, depth int) error {
\t\t\tif err := checkQueryContext(ctx, source); err != nil {
\t\t\t\treturn err
\t\t\t}
\t\t\tvisitedNodes++
\t\t\tif limits.MaxVisitedNodes > 0 && visitedNodes > limits.MaxVisitedNodes {
\t\t\t\treturn newQueryError("visited_node_limit", "query exceeded maximum visited nodes", 0, source)
\t\t\t}''',
)
replace_once(
    "api/panorama/query_eval.go",
    "\tout := Graph{ObservedAt: graph.ObservedAt}",
    "\tout := Graph{ObservedAt: graph.ObservedAt, Clustered: graph.Clustered}",
)

with Path("api/panorama/query_test.go").open("a") as f:
    f.write(r'''

func TestGraphQueryCycleStaysBounded(t *testing.T) {
	graph := Graph{
		Nodes: []Node{
			{ID: "application:a", Kind: KindApplication, Name: "A", Provenance: ProvenanceDerived},
			{ID: "process:p", Kind: KindProcess, Name: "P", Provenance: ProvenanceDerived},
		},
		Edges: []Edge{
			{From: "application:a", To: "process:p", Kind: EdgeCalls},
			{From: "process:p", To: "application:a", Kind: EdgeCalls},
		},
	}
	got, err := executeGraphQuery(graph, QueryRequest{Query: `MATCH p = (a:application)-[:calls*1..8]->(x) RETURN p`}, DefaultQueryLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Graph.Nodes) != 2 || len(got.Graph.Edges) != 2 {
		t.Fatalf("cycle result = %d nodes/%d edges, want 2/2", len(got.Graph.Nodes), len(got.Graph.Edges))
	}
}

func TestGraphQueryASTAndVisitedNodeLimitsFailClosed(t *testing.T) {
	limits := DefaultQueryLimits()
	limits.MaxASTNodes = 3
	if _, err := executeGraphQuery(queryFixture(), QueryRequest{Query: `MATCH (p:process) WHERE p.name = "Invoice" AND p.version = 3 RETURN p`}, limits); err == nil || !strings.Contains(err.Error(), "AST") {
		t.Fatalf("AST limit err = %v", err)
	}

	limits = DefaultQueryLimits()
	limits.MaxVisitedNodes = 1
	if _, err := executeGraphQuery(queryFixture(), QueryRequest{Query: `MATCH p = (a:application)-[*1..4]->(x) RETURN p`}, limits); err == nil || !strings.Contains(err.Error(), "visited nodes") {
		t.Fatalf("visited-node limit err = %v", err)
	}
}

func TestGraphQueryProjectionIsDeterministicAndPreservesClusteredInput(t *testing.T) {
	graph := queryFixture()
	graph.Clustered = true
	req := QueryRequest{Query: `MATCH p = (a:application)-[*1..3]->(x) RETURN p ORDER BY x.name ASC LIMIT 10`}
	first, err := executeGraphQuery(graph, req, DefaultQueryLimits())
	if err != nil {
		t.Fatal(err)
	}
	second, err := executeGraphQuery(graph, req, DefaultQueryLimits())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatalf("same query produced different bytes:\n%s\n%s", a, b)
	}
	if !first.Graph.Clustered {
		t.Fatal("query result lost clustered=true from its authorized source mesh")
	}
}
''')

openapi = Path("api/openapi.go")
text = openapi.read_text()
anchor = '''\t\t{"GET", "/api/v1/panorama/mesh", s.panoramaMesh.HandleGraph, apiOp{
\t\t\tsummary: "Derive the landscape mesh from this server's resources with severity, filtered for the caller (ADR-0211). Pass drafts=1 to include saved-but-not-deployed diagrams, which are left out by default so the size budget is spent on what this server actually runs", tag: "Panorama", role: RoleModeler,
\t\t\tresp: jsonBody("Derived landscape graph", tObject())}},
'''
if anchor not in text:
    raise SystemExit("Panorama mesh route anchor not found")
routes = anchor + '''\t\t{"GET", "/api/v1/panorama/query/schema", s.panoramaMesh.HandleQuerySchema, apiOp{
\t\t\tsummary: "Describe Panorama Graph Query v1: queryable node and relationship kinds, properties, operators, clauses, and enforced limits", tag: "Panorama", role: RoleModeler,
\t\t\tresp: jsonBody("Panorama graph query schema", tObject())}},
\t\t{"POST", "/api/v1/panorama/query", s.panoramaMesh.HandleQuery, apiOp{
\t\t\tsummary: "Evaluate a bounded read-only Panorama Graph Query v1 expression against the caller-authorized derived landscape", tag: "Panorama", role: RoleModeler,
\t\t\treq: jsonBody("Graph query and typed parameters", schemaObj(map[string]any{
\t\t\t\t"query": tString(), "parameters": tObject(),
\t\t\t}, "query")),
\t\t\tresp: jsonBody("Renderable result graph and enforced limits", tObject())}},
'''
openapi.write_text(text.replace(anchor, routes, 1))

qjs = r'''// Panorama Graph Query v1 — declarative selection over the derived Starmap.
// Query results stay in memory and are handed to the existing Starmap renderer.
const nativeFetch = globalThis.fetch.bind(globalThis);
const state = {
  result: null,
  query: 'MATCH p = (a:application)-[*1..3]->(x) RETURN p LIMIT 200',
  parameters: '{}',
  schema: null,
};

const escQuery = (value) => String(value ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

function isMeshRead(input, init) {
  const method = String(init?.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
  if (method !== 'GET') return false;
  const raw = input instanceof Request ? input.url : String(input);
  const url = new URL(raw, location.href);
  return url.origin === location.origin && url.pathname === '/api/v1/panorama/mesh';
}

globalThis.fetch = async (input, init) => {
  if (state.result && isMeshRead(input, init)) {
    return new Response(JSON.stringify(state.result.graph), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }
  return nativeFetch(input, init);
};

async function requestJSON(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.body = JSON.stringify(body);
    opts.headers['Content-Type'] = 'application/json';
  }
  const res = await nativeFetch(path, opts);
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
  if (!res.ok) {
    const diagnostic = data?.error;
    const message = typeof diagnostic === 'object'
      ? `${diagnostic.message || res.statusText}${diagnostic.line ? ` (line ${diagnostic.line}, column ${diagnostic.column})` : ''}`
      : String(diagnostic || res.statusText);
    throw new Error(message);
  }
  return data;
}

function schemaHTML(schema) {
  if (!schema) return '<p class="muted">Schema unavailable.</p>';
  const kinds = (schema.nodeKinds || []).map((k) => k.name).join(', ');
  const rels = (schema.relationshipKinds || []).map((r) => r.name).join(', ');
  const limits = schema.limits || {};
  return `<div class="muted" style="font-size:12px; line-height:1.55">
    <b>${escQuery(schema.profile)}</b><br>
    Clauses: ${escQuery((schema.clauses || []).join(', '))}<br>
    Node kinds: ${escQuery(kinds)}<br>
    Relationships: ${escQuery(rels)}<br>
    Bounded paths: max ${escQuery(limits.maxPathDepth ?? '—')} hops ·
    result max ${escQuery(limits.maxReturnedNodes ?? '—')} nodes / ${escQuery(limits.maxReturnedEdges ?? '—')} edges
  </div>`;
}

function rerenderStarmap() {
  window.dispatchEvent(new HashChangeEvent('hashchange'));
}

function injectQueryPanel() {
  if (location.hash !== '#/panorama/starmap') return;
  const view = document.getElementById('view');
  if (!view || view.querySelector('[data-panorama-query]')) return;

  if (state.result && state.result.graph && state.result.graph.nodes?.length === 0) {
    const empty = view.querySelector('.card.empty p');
    if (empty) empty.textContent = 'This query returned no nodes. Change the query or reset to show the full landscape.';
  }

  const panel = document.createElement('section');
  panel.className = 'card';
  panel.dataset.panoramaQuery = '1';
  panel.style.marginBottom = '12px';
  panel.innerHTML = `
    <div class="row" style="align-items:center; justify-content:space-between; gap:12px">
      <div><h2 style="margin:0">Query</h2>
        <p class="muted" style="margin:4px 0 0">Panorama Graph Query v1 · read-only · Cypher/GQL-inspired</p></div>
      <span data-query-status class="muted" aria-live="polite">${state.result
        ? `${state.result.graph.nodes?.length || 0} nodes · ${state.result.graph.edges?.length || 0} edges`
        : 'Full landscape'}</span>
    </div>
    <form data-query-form style="margin-top:12px">
      <label class="field">Graph query
        <textarea data-query-text rows="4" spellcheck="false" style="font-family:ui-monospace, SFMono-Regular, Menlo, monospace; width:100%">${escQuery(state.query)}</textarea></label>
      <label class="field">Parameters (JSON object)
        <textarea data-query-params rows="2" spellcheck="false" style="font-family:ui-monospace, SFMono-Regular, Menlo, monospace; width:100%">${escQuery(state.parameters)}</textarea></label>
      <div class="row" style="gap:8px; align-items:center">
        <button class="btn" type="submit">Run query</button>
        <button class="btn secondary" type="button" data-query-reset ${state.result ? '' : 'disabled'}>Reset</button>
        <button class="btn secondary" type="button" data-query-help>Schema &amp; limits</button>
        <span data-query-error class="muted" role="alert"></span>
      </div>
      <div data-query-schema hidden style="margin-top:10px"></div>
    </form>`;
  view.prepend(panel);

  const form = panel.querySelector('[data-query-form]');
  const query = panel.querySelector('[data-query-text]');
  const params = panel.querySelector('[data-query-params]');
  const error = panel.querySelector('[data-query-error]');
  const status = panel.querySelector('[data-query-status]');

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    error.textContent = '';
    let parameters;
    try {
      parameters = JSON.parse(params.value || '{}');
      if (!parameters || Array.isArray(parameters) || typeof parameters !== 'object') throw new Error('Parameters must be a JSON object.');
    } catch (e) {
      error.textContent = e.message;
      return;
    }
    const button = form.querySelector('button[type="submit"]');
    button.disabled = true;
    status.textContent = 'Running…';
    try {
      const result = await requestJSON('POST', '/api/v1/panorama/query', { query: query.value, parameters });
      state.query = query.value;
      state.parameters = params.value || '{}';
      state.result = result;
      rerenderStarmap();
    } catch (e) {
      status.textContent = state.result ? `${state.result.graph.nodes?.length || 0} nodes · ${state.result.graph.edges?.length || 0} edges` : 'Full landscape';
      error.textContent = e.message;
    } finally {
      button.disabled = false;
    }
  });

  panel.querySelector('[data-query-reset]').addEventListener('click', () => {
    state.result = null;
    rerenderStarmap();
  });
  panel.querySelector('[data-query-help]').addEventListener('click', async () => {
    const host = panel.querySelector('[data-query-schema]');
    host.hidden = !host.hidden;
    if (host.hidden || host.dataset.loaded) return;
    host.textContent = 'Loading schema…';
    try {
      state.schema ||= await requestJSON('GET', '/api/v1/panorama/query/schema');
      host.innerHTML = schemaHTML(state.schema);
      host.dataset.loaded = '1';
    } catch (e) {
      host.textContent = e.message;
    }
  });
}

const observer = new MutationObserver(injectQueryPanel);
const start = () => {
  const view = document.getElementById('view');
  if (view) observer.observe(view, { childList: true, subtree: false });
  injectQueryPanel();
};
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
else start();
'''
Path("api/web/panorama-query.js").write_text(qjs)

replace_once(
    "api/web/app.js",
    'import { renderKeyFeatures, paintKeyFeatures } from "./key-features.js";\n',
    'import { renderKeyFeatures, paintKeyFeatures } from "./key-features.js";\nimport "./panorama-query.js";\n',
)

adr = Path("docs/adr/draft-panorama-read-only-graph-queries.md")
text = adr.read_text()
text = text.replace("# ADR-DRAFT: Read-only graph queries in Panorama", "# ADR-0301: Read-only graph queries in Panorama", 1)
text = text.replace("- **Status:** Proposed", "- **Status:** Accepted", 1)
text = text.replace("- **Implementation:** Not started", "- **Implementation:** Landed in PR #911", 1)
text += """

## Implementation note

The landed v1 surface is `GET /api/v1/panorama/query/schema` plus `POST /api/v1/panorama/query`. The evaluator receives only the per-principal ADR-0211 mesh, enforces explicit token, AST, path-depth, visited-node, visited-edge, match, returned-node, returned-edge, and deadline budgets, and treats `restricted` nodes as traversal terminals. The response reuses the Starmap graph DTO and preserves whether its source projection was already clustered. Panorama's query editor keeps the query result in memory and routes it through the existing Starmap renderer; it neither stores result graphs nor introduces a second renderer.
"""
landed = Path("docs/adr/0301-panorama-read-only-graph-queries.md")
landed.write_text(text)
adr.unlink()

roadmap = Path("ROADMAP.md")
rtext = roadmap.read_text()
if "P6 — Read-only landscape graph queries" not in rtext:
    m = re.search(r"(## Milestone P —.*?)(?=\n## Milestone )", rtext, flags=re.S)
    if not m:
        raise SystemExit("Milestone P section not found")
    addition = """
- ✅ **P6 — Read-only landscape graph queries** ([ADR-0301](docs/adr/0301-panorama-read-only-graph-queries.md)): Panorama can run bounded, read-only, Cypher/GQL-inspired graph selections directly over ADR-0211's caller-authorized derived landscape. The server publishes its query schema and hard execution budgets; restricted placeholders are opaque traversal terminals; results reuse the Starmap graph contract and renderer. No graph database, second persisted graph, processor command, WAL record, or replay path is introduced. Saved queries/views remain a follow-up rather than persisting result graphs.
"""
    section = m.group(1).rstrip() + "\n" + addition
    rtext = rtext[:m.start(1)] + section + rtext[m.end(1):]
    roadmap.write_text(rtext)
