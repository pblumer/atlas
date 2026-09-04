import { test, expect } from "@playwright/test";

// Impact analysis over the derived mesh (ADR-0211 §6): "what breaks if this
// worker is down". These drive the traversal directly through a harness, so each
// rule is one case rather than a click sequence over a rendered graph.

// A small landscape with every shape the traversal has to respect:
//
//   app ──contains──> invoice ──calls──> dunning ──uses──> mail
//    │                   │
//    │                   └──uses──> restricted (a worker outside our access)
//    └──contains──> orphan          (in the application, depends on nothing)
//
//   loopA <──calls──> loopB         (a legal BPMN cycle)
const graph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", processId: "invoice", version: 1 },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", processId: "dunning", version: 1 },
    { id: "process:9", kind: "process", name: "Orphan", provenance: "derived", processId: "orphan", version: 1 },
    { id: "worker:c1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
    { id: "restricted:1", kind: "restricted", provenance: "derived" },
    { id: "process:5", kind: "process", name: "LoopA", provenance: "derived", processId: "loopa", version: 1 },
    { id: "process:6", kind: "process", name: "LoopB", provenance: "derived", processId: "loopb", version: 1 },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:9", kind: "contains" },
    { from: "process:1", to: "process:2", kind: "calls" },
    { from: "process:2", to: "worker:c1", kind: "uses" },
    { from: "process:1", to: "restricted:1", kind: "uses" },
    { from: "process:5", to: "process:6", kind: "calls" },
    { from: "process:6", to: "process:5", kind: "calls" },
  ],
  restricted: 1,
  clustered: false,
};

async function impact(page, id, opts) {
  return page.evaluate(([g, i, o]) => window.impactFrom(g, i, o), [graph, id, opts]);
}

test.beforeEach(async ({ page }) => {
  await page.goto("/panorama-impact-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
});

// The blast-radius question, and the reason the mesh is a graph rather than a
// diagram: everything that would notice if this worker stopped answering.
test("dependents answer what breaks if a worker goes down", async ({ page }) => {
  const r = await impact(page, "worker:c1", { direction: "dependents", depth: Infinity });

  expect(new Set(r.nodes)).toEqual(new Set(["worker:c1", "process:2", "process:1"]));
  expect(r.edges).toHaveLength(2);
});

// The other direction: what this process needs in order to work at all.
test("dependencies answer what a process needs", async ({ page }) => {
  const r = await impact(page, "process:1", { direction: "dependencies", depth: Infinity });

  expect(new Set(r.nodes)).toEqual(
    new Set(["process:1", "process:2", "worker:c1", "restricted:1"]),
  );
});

// Containment is structure, not dependency. Traversing it would drag in every
// sibling process through the application and make an impact set meaningless —
// "Orphan depends on nothing" must survive its neighbour having dependencies.
test("containment is not a dependency edge", async ({ page }) => {
  const r = await impact(page, "process:9", { direction: "both", depth: Infinity });

  expect(r.nodes).toEqual(["process:9"]);
  expect(r.edges).toEqual([]);
});

// A restricted placeholder ends the walk: we may not see past it. The answer is
// therefore incomplete, and must say so rather than reading as complete — the same
// rule the mesh applies to the picture, applied to the analysis over it.
test("a restricted node truncates the answer and the answer says so", async ({ page }) => {
  const r = await impact(page, "process:1", { direction: "dependencies", depth: Infinity });

  expect(r.nodes).toContain("restricted:1");
  expect(r.truncatedBy).toEqual(["restricted:1"]);
  expect(r.complete).toBe(false);
});

test("an answer that hits no restricted node reports itself complete", async ({ page }) => {
  const r = await impact(page, "worker:c1", { direction: "dependents", depth: Infinity });

  expect(r.truncatedBy).toEqual([]);
  expect(r.complete).toBe(true);
});

// A process calling a process that calls it back is legal BPMN. The traversal must
// terminate, and must not report the seed twice.
test("a cycle terminates and is not double counted", async ({ page }) => {
  const r = await impact(page, "process:5", { direction: "both", depth: Infinity });

  expect(new Set(r.nodes)).toEqual(new Set(["process:5", "process:6"]));
  expect(r.nodes).toHaveLength(2);
});

// Depth is the "chosen depth" ADR-0211 §6 asks for: one hop shows the immediate
// blast radius, more hops show the reach.
test("depth bounds the walk", async ({ page }) => {
  const one = await impact(page, "worker:c1", { direction: "dependents", depth: 1 });
  expect(new Set(one.nodes)).toEqual(new Set(["worker:c1", "process:2"]));

  const two = await impact(page, "worker:c1", { direction: "dependents", depth: 2 });
  expect(new Set(two.nodes)).toEqual(new Set(["worker:c1", "process:2", "process:1"]));
});

// A node id that is not in the graph is a caller error, not an empty landscape:
// answering with an empty set would read as "nothing depends on this".
test("an unknown node is refused rather than answered emptily", async ({ page }) => {
  const r = await impact(page, "process:404", { direction: "both", depth: Infinity });
  expect(r).toBeNull();
});

// A second landscape, for the three answers built over the walk. Severity matters
// here, so every node carries one, and one worker is deliberately the thing four
// separate processes reach:
//
//   invoice ──calls──> dunning ──uses──> credit
//      │                  │
//      └──uses──┐         └──uses──┐
//   reminder ───┼──uses──> mail <──┘
//   signup ─────┘
//
//   orphan ──calls──> restricted        (a boundary, asked about directly)
const wider = {
  nodes: [
    { id: "process:1", kind: "process", name: "Invoice", severity: "ok", state: "healthy" },
    { id: "process:2", kind: "process", name: "Dunning", severity: "critical", state: "degraded" },
    { id: "process:3", kind: "process", name: "Reminder", severity: "ok", state: "healthy" },
    { id: "process:4", kind: "process", name: "Signup", severity: "attention", state: "degraded" },
    { id: "process:7", kind: "process", name: "Orphan", severity: "unknown", state: "unbound" },
    { id: "worker:mail", kind: "worker", name: "ops-mail", severity: "critical", state: "not_ready" },
    { id: "decision:credit", kind: "decision", name: "Credit score", severity: "ok", state: "healthy" },
    { id: "restricted:1", kind: "restricted", severity: "unknown", state: "unbound" },
  ],
  edges: [
    { from: "process:1", to: "process:2", kind: "calls" },
    { from: "process:1", to: "worker:mail", kind: "uses" },
    { from: "process:2", to: "worker:mail", kind: "uses" },
    { from: "process:3", to: "worker:mail", kind: "uses" },
    { from: "process:4", to: "worker:mail", kind: "uses" },
    { from: "process:2", to: "decision:credit", kind: "uses" },
    { from: "process:7", to: "restricted:1", kind: "calls" },
  ],
  restricted: 1,
};

const all = { direction: "dependents", depth: Infinity };

// A count says how many; it does not say how bad. Three of a worker's four
// dependents already failing is a different morning from four quiet ones, and the
// count is identical in both.
test("the answer says how bad the radius is, without claiming it caused it", async ({ page }) => {
  const s = await page.evaluate(([g, o]) => window.impactSummary(g, "worker:mail", o), [wider, all]);

  expect(s.total).toBe(4);
  expect(s.bySeverity).toEqual({ critical: 1, attention: 1, ok: 2, unknown: 0 });
  // Every one of the four reaches the worker by its own edge, so nothing is further
  // out — the distinction is real and this landscape happens to have none of it.
  expect(s.direct).toBe(4);
  expect(s.indirect).toBe(0);
  expect(s.complete).toBe(true);
});

// Direct and transitive are different facts: the direct dependents are the ones
// whose owners get told, the rest are the reach.
test("direct dependents are told apart from the ones further out", async ({ page }) => {
  const s = await page.evaluate(([g, o]) => window.impactSummary(g, "decision:credit", o), [wider, all]);
  expect(s.total).toBe(2);
  expect(s.direct).toBe(1);      // Dunning uses it
  expect(s.indirect).toBe(1);    // Invoice only reaches it through Dunning

  const list = await page.evaluate(([g, o]) => window.impactList(g, "decision:credit", o), [wider, all]);
  expect(list.map((n) => [n.name, n.direct])).toEqual([["Dunning", true], ["Invoice", false]]);
});

// The count and the highlight still leave the reader hunting for the three that
// matter among four hundred circles. The list is the same nodes as an index, worst
// first — and direct before transitive inside a class, because those are the ones
// somebody has to be told about first.
test("the impacted nodes are named, worst first", async ({ page }) => {
  const list = await page.evaluate(([g, o]) => window.impactList(g, "worker:mail", o), [wider, all]);

  expect(list.map((n) => n.name)).toEqual(["Dunning", "Signup", "Invoice", "Reminder"]);
  expect(list[0].severity).toBe("critical");
  // It is a bounded list: a sidebar is not a page.
  const short = await page.evaluate(([g, o]) =>
    window.impactList(g, "worker:mail", o, { limit: 2 }), [wider, all]);
  expect(short.map((n) => n.name)).toEqual(["Dunning", "Signup"]);
});

// The question the reader actually arrives with, and the one that needed a
// selection until now: which of these would hurt most. Ranking it means asking it
// of every node rather than of the one somebody already suspected.
test("the landscape can be asked where the risk is, with nothing selected", async ({ page }) => {
  const rows = await page.evaluate(([g, o]) => window.blastRanking(g, o), [wider, all]);

  expect(rows.map((r) => [r.name ?? r.kind, r.total, r.direct])).toEqual([
    ["ops-mail", 4, 4],
    ["Credit score", 2, 1],
    ["Dunning", 1, 1],
    // The placeholder carries no name — that is the whole of what it withholds —
    // and it is still ranked, because "one of your processes depends on something
    // you cannot see" is a finding rather than an absence.
    ["restricted", 1, 1],
  ]);
  // Nodes nothing depends on are left out rather than listed with a zero: a ranking
  // of the whole landscape by a number most of it does not have is a list of noise.
  expect(rows.some((r) => r.name === "Invoice")).toBe(false);
});

// A walk that stopped at a permission boundary produces a floor, and in a ranking
// that matters twice: the order is a claim about the rows as well as the numbers.
test("a ranked row whose walk hit a boundary says its number is a floor", async ({ page }) => {
  const rows = await page.evaluate(([g, o]) => window.blastRanking(g, o), [wider, all]);
  const boundary = rows.find((r) => r.kind === "restricted");

  expect(boundary).toBeTruthy();
  expect(boundary.total).toBe(1);
  expect(boundary.complete).toBe(false);
});

// Asking about the boundary itself is not the same as arriving at one. The nodes
// that point at a placeholder are in this caller's own picture, so they are walked
// — answering "nothing depends on this" about a hidden resource is the single claim
// a boundary must never be allowed to make.
test("a placeholder asked about directly reports its dependents, as a floor", async ({ page }) => {
  const r = await page.evaluate(([g, o]) =>
    window.impactFrom(g, "restricted:1", o), [wider, all]);

  expect(new Set(r.nodes)).toEqual(new Set(["restricted:1", "process:7"]));
  expect(r.complete).toBe(false);
  expect(r.truncatedBy).toEqual(["restricted:1"]);
});

// The ranking follows the controls rather than fixing its own question, so it and
// the panel can never be measured differently.
test("the ranking answers the direction and depth it is given", async ({ page }) => {
  const needs = await page.evaluate((g) =>
    window.blastRanking(g, { direction: "dependencies", depth: Infinity }), wider);
  // Invoice needs the most: dunning, mail, credit.
  expect(needs[0].name).toBe("Invoice");
  expect(needs[0].total).toBe(3);

  const oneHop = await page.evaluate((g) =>
    window.blastRanking(g, { direction: "dependents", depth: 1 }), wider);
  expect(oneHop[0].name).toBe("ops-mail");
  expect(oneHop[0].total).toBe(4);
  // Credit score's second dependent is two hops away, so at depth 1 it is not there.
  expect(oneHop.find((r) => r.name === "Credit score").total).toBe(1);
});

// A maintenance window: several nodes going down together (ADR-0211 §6 asked of a
// set rather than of a node).
//
// This landscape exists to make the overlap provable rather than plausible:
//
//   shared ──calls──> p1        shared ──calls──> p2
//   onlyA  ──calls──> p1        onlyB  ──calls──> p2
//   p1     ──uses──>  mail      p2     ──uses──>  mail
//
// Take p1 down and onlyA and shared stop. Take p2 down and onlyB and shared stop.
// Take both down together and three things stop, not four: `shared` is in both radii
// and can only stop once.
const window2 = {
  nodes: [
    { id: "process:p1", kind: "process", name: "P1", provenance: "derived", processId: "p1", version: 1 },
    { id: "process:p2", kind: "process", name: "P2", provenance: "derived", processId: "p2", version: 1 },
    { id: "process:onlyA", kind: "process", name: "OnlyA", provenance: "derived", processId: "onlya", version: 1 },
    { id: "process:onlyB", kind: "process", name: "OnlyB", provenance: "derived", processId: "onlyb", version: 1 },
    { id: "process:shared", kind: "process", name: "Shared", provenance: "derived", processId: "shared", version: 1, severity: "critical" },
    { id: "worker:mail", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
  ],
  edges: [
    { from: "process:shared", to: "process:p1", kind: "calls" },
    { from: "process:shared", to: "process:p2", kind: "calls" },
    { from: "process:onlyA", to: "process:p1", kind: "calls" },
    { from: "process:onlyB", to: "process:p2", kind: "calls" },
    { from: "process:p1", to: "worker:mail", kind: "uses" },
    { from: "process:p2", to: "worker:mail", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};
const deep = { direction: "dependents", depth: Infinity };

// The claim the whole feature rests on: the union is walked, not added up.
test("a window of two costs less than the two on their own", async ({ page }) => {
  const o = await page.evaluate(([g, ids, opts]) => window.windowOverlap(g, ids, opts),
    [window2, ["process:p1", "process:p2"], deep]);

  // Three things stop, not four.
  expect(o.total).toBe(3);
  expect(o.sum).toBe(4);
  // And the panel can say why: one node is in both radii.
  expect(o.shared).toBe(1);
  expect(o.each.map((row) => [row.name, row.total])).toEqual([["P1", 2], ["P2", 2]]);
});

// The other half of the same rule: a set whose radii do not touch costs the sum, and
// the panel must not imply a saving that is not there.
test("a window over unrelated nodes costs each of them in full", async ({ page }) => {
  const o = await page.evaluate(([g, ids, opts]) => window.windowOverlap(g, ids, opts),
    [window2, ["process:onlyA", "process:onlyB"], deep]);
  expect(o.total).toBe(0);
  expect(o.sum).toBe(0);
  expect(o.shared).toBe(0);
});

// A node the window already takes down is not collateral: it is part of the plan,
// and counting it would inflate the answer with the plan itself.
test("the window's own members are never counted as what it breaks", async ({ page }) => {
  const alone = await page.evaluate(([g, id, opts]) => window.impactSummary(g, id, opts),
    [window2, "process:p1", deep]);
  // On its own p1 stops two: Shared and OnlyA.
  expect(alone.total).toBe(2);

  // Put Shared in the window as well and it stops being collateral — one thing is
  // left outside the plan.
  const withShared = await page.evaluate(([g, ids, opts]) => window.windowSummary(g, ids, opts),
    [window2, ["process:p1", "process:shared"], deep]);
  expect(withShared.total).toBe(1);

  // And the panel can say that Shared was going down anyway, which is what stops it
  // being written into a change request as though it changed something.
  const o = await page.evaluate(([g, ids, opts]) => window.windowOverlap(g, ids, opts),
    [window2, ["process:p1", "process:shared"], deep]);
  expect(o.covered).toEqual(["process:shared"]);
});

// The severity mix and the named list are the same two answers, over the set.
test("a window reports the shape of its radius, not only the size", async ({ page }) => {
  const s = await page.evaluate(([g, ids, opts]) => window.windowSummary(g, ids, opts),
    [window2, ["process:p1", "process:p2"], deep]);
  expect(s.total).toBe(3);
  expect(s.direct).toBe(3);        // every one of them has an edge to p1 or p2
  expect(s.bySeverity.critical).toBe(1);

  const list = await page.evaluate(([g, ids, opts]) => window.windowList(g, ids, opts),
    [window2, ["process:p1", "process:p2"], deep]);
  // Worst first, as everywhere else.
  expect(list[0].name).toBe("Shared");
  // The worker is what p1 and p2 *use*; nothing about it depends on them, so it is
  // not in an answer about what stops.
  expect(list.map((n) => n.name).sort()).toEqual(["OnlyA", "OnlyB", "Shared"]);
});

// A window is a claim about a named set. Planning around two of the three nodes
// somebody asked about would be worse than refusing to answer.
test("a window naming a node that is not here is refused rather than trimmed", async ({ page }) => {
  const o = await page.evaluate(([g, ids, opts]) => window.impactOf(g, ids, opts),
    [window2, ["process:p1", "process:gone"], deep]);
  expect(o).toBeNull();
});

// The permission boundary carries through the union: one incomplete walk makes the
// window's total a floor, and a floor presented as a total is the failure §6 exists
// to prevent.
test("a window that reaches a boundary reports a floor", async ({ page }) => {
  const withRestricted = {
    ...window2,
    nodes: [...window2.nodes, { id: "restricted:1", kind: "restricted", provenance: "derived" }],
    edges: [...window2.edges, { from: "restricted:1", to: "process:p2", kind: "calls" }],
    restricted: 1,
  };
  const o = await page.evaluate(([g, ids, opts]) => window.windowOverlap(g, ids, opts),
    [withRestricted, ["process:p1", "process:p2"], deep]);
  expect(o.complete).toBe(false);
  // p1's own walk never reaches it, so only p2's row is a floor.
  expect(o.each.find((r) => r.name === "P1").complete).toBe(true);
  expect(o.each.find((r) => r.name === "P2").complete).toBe(false);
});
