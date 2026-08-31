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
