from pathlib import Path


# ADR numbers are assigned on main by `make adr-number`, never on a feature branch.
numbered = Path("docs/adr/0301-panorama-read-only-graph-queries.md")
draft = Path("docs/adr/draft-panorama-read-only-graph-queries.md")
if numbered.exists():
    text = numbered.read_text()
    text = text.replace("# ADR-0301: Read-only graph queries in Panorama", "# ADR-DRAFT: Read-only graph queries in Panorama", 1)
    text = text.replace("- **Implementation:** Landed in PR #911", "- **Implementation:** Landed", 1)
    draft.write_text(text)
    numbered.unlink()

roadmap = Path("ROADMAP.md")
rtext = roadmap.read_text()
rtext = rtext.replace(
    "([ADR-0301](docs/adr/0301-panorama-read-only-graph-queries.md))",
    "([ADR-draft-panorama-read-only-graph-queries](docs/adr/draft-panorama-read-only-graph-queries.md))",
)
roadmap.write_text(rtext)

# Every HTTP operation is deliberately classified against MCP. Query v1 is an
# interactive Panorama/API contract first; exposing the free-form language as an
# MCP tool is deferred until the versioned query profile itself is stable.
mcp = Path("mcp/tool_registry_drift_test.go")
text = mcp.read_text()
anchor = '\t"GET /api/v1/panorama/mesh": "landscape mesh payload is still gaining node kinds across P2.5; exposing it now would freeze a shape that is about to change",\n'
if anchor not in text:
    raise SystemExit("Panorama MCP omission anchor not found")
addition = anchor + '''\t// Panorama Graph Query v1 is intentionally an interactive API/UI surface first.\n\t// Its server-published schema exists so the editor never hard-codes the language,\n\t// while an MCP tool would itself freeze that still-versioned language as an agent\n\t// contract. Add the tool once the query profile is stable enough to support it.\n\t"GET /api/v1/panorama/query/schema": "editor introspection for the versioned Panorama query language; there is no MCP query tool consuming it yet",\n\t"POST /api/v1/panorama/query":       "free-form Panorama query language is API/UI-first in v1; defer an MCP contract until the profile is stable",\n'''
mcp.write_text(text.replace(anchor, addition, 1))
