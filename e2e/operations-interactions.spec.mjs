// End-to-end coverage for the Operations page *Interactions* (ADR-0559 §7, §8).
// Drives the REAL page against a mocked /api/v1: the graph opens with every
// participant collapsed, possible and observed exchanges are drawn apart, a process
// expands to its message ports and collapses again, the sends nobody received are
// named, the replay reads its history backwards page by page until it begins, plays
// one message at a time or bundled, and Live appends what came after. The Go suite
// decides what the routes answer and to whom; this checks the wiring.
import { test, expect } from "@playwright/test";

const GRAPH = {
  processId: "bestellung",
  participants: [
    { id: "process:bestellung", kind: "process", center: true, name: "Sekretariat", processId: "bestellung", version: 2 },
    { id: "process:versand", kind: "process", name: "Versand", processId: "versand", version: 1 },
    { id: "process:rechnung", kind: "process", name: "Rechnung", processId: "rechnung", version: 1 },
    { id: "inbound-watch:c1", kind: "inbound-watch", name: "Lager-Jira", connectorId: "c1", workerKind: "jira" },
    { id: "outside-view", kind: "outside-view" },
  ],
  exchanges: [
    { from: "process:bestellung", to: "process:versand", message: "freigabe", possible: true,
      observed: { received: 3, started: 3, lastAt: "2026-11-11T10:00:00Z" } },
    { from: "process:bestellung", to: "process:rechnung", message: "freigabe", possible: true },
    { from: "process:versand", to: "process:bestellung", message: "versandt", possible: true,
      observed: { received: 3, started: 0, lastAt: "2026-11-11T10:00:01Z" } },
    { from: "inbound-watch:c1", to: "process:bestellung", message: "versandt", possible: true },
    { from: "outside-view", to: "process:bestellung", message: "storno", possible: false,
      observed: { received: 1, started: 0, lastAt: "2026-11-11T09:00:00Z" } },
  ],
  unreceived: [{ from: "process:bestellung", message: "protokoll", count: 2, lastAt: "2026-11-11T10:00:00Z" }],
  ports: [
    { direction: "out", message: "freigabe", elements: ["release"], roles: ["throw"] },
    { direction: "out", message: "protokoll", elements: ["note"], roles: ["throw"] },
    { direction: "in", message: "versandt", elements: ["wait"], roles: ["catch"] },
  ],
  observed: { from: "2026-11-10T10:00:00Z", to: "2026-11-11T10:00:00Z", readFrom: "2026-11-10T10:00:00Z", rows: 40, partial: false },
  retention: "720h0m0s",
};

const ev = (position, at, kind, from, to, message, extra = {}) =>
  ({ position, at, kind, from, to, message, count: 1, ...extra });

// The history, newest page first: the route answers each page oldest first.
const NEWEST = {
  processId: "bestellung", participants: GRAPH.participants, more: true, next: "50", latest: "90", rows: 4,
  events: [
    ev("60", "2026-11-11T10:00:00Z", "unreceived", "process:bestellung", "", "protokoll"),
    ev("70", "2026-11-11T10:00:00Z", "started", "process:bestellung", "process:versand", "freigabe", { correlationKey: "A-3" }),
    ev("80", "2026-11-11T10:00:01Z", "received", "process:versand", "process:bestellung", "versandt", { correlationKey: "A-3" }),
    ev("90", "2026-11-11T10:00:02Z", "outside", "process:bestellung", "outside-view", "freigabe", { started: 1 }),
  ],
};
const OLDER = {
  processId: "bestellung", participants: GRAPH.participants, more: false, next: "10", latest: "40", rows: 3,
  events: [
    ev("10", "2026-11-11T09:00:00Z", "received", "outside-view", "process:bestellung", "storno"),
    ev("20", "2026-11-11T09:30:00Z", "started", "process:bestellung", "process:versand", "freigabe", { correlationKey: "A-1" }),
    ev("40", "2026-11-11T09:30:01Z", "received", "process:versand", "process:bestellung", "versandt", { correlationKey: "A-1" }),
  ],
};
const NEWER = {
  processId: "bestellung", participants: GRAPH.participants, more: false, next: "95", latest: "95", rows: 1,
  events: [ev("95", "2026-11-11T10:05:00Z", "started", "process:bestellung", "process:versand", "freigabe", { correlationKey: "A-4" })],
};

function installMock(page) {
  const calls = [];
  let followed = false;
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname, q = url.searchParams;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "u1", username: "op", roles: ["operator"] } } });
    }
    if (path.endsWith("/process-interactions/replay")) {
      calls.push("replay" + url.search);
      if (q.get("before") === "50") return route.fulfill({ json: OLDER });
      if (q.get("after")) {
        if (!followed && q.get("after") === "90") { followed = true; return route.fulfill({ json: NEWER }); }
        return route.fulfill({ json: { ...NEWER, events: [], latest: q.get("after") } });
      }
      return route.fulfill({ json: NEWEST });
    }
    if (path.endsWith("/process-interactions")) {
      calls.push("graph" + url.search);
      if (q.get("processId") === "versand") {
        return route.fulfill({ json: { ...GRAPH, processId: "versand", ports: [
          { direction: "in", message: "freigabe", elements: ["s"], roles: ["start"] },
          { direction: "out", message: "versandt", elements: ["report"], roles: ["throw"] }] } });
      }
      if (q.get("processId") !== "bestellung") {
        return route.fulfill({ json: { ...GRAPH, processId: q.get("processId"), ports: [] } });
      }
      return route.fulfill({ json: GRAPH });
    }
    return route.fulfill({ json: [] });
  });
  return calls;
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

const box = (page, id) => page.locator(`.ix-box[data-id="${id}"]`);
const edge = (page, from, to, message) =>
  page.locator(`.ix-edge[data-from="${from}"][data-to="${to}"][data-message="${message}"]`);

test("the graph opens collapsed, with possible and observed drawn apart", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  await expect(page.locator(".ix-box")).toHaveCount(5);
  await expect(page.locator("#ix-title")).toHaveText("Zusammenspiel: Sekretariat");
  await expect(box(page, "process:bestellung")).toHaveClass(/center/);
  // Collapsed: no box shows its ports.
  await expect(page.locator(".ix-port")).toHaveCount(0);
  // The center sits between who sends to it and who it sends to.
  const x = async (id) => (await box(page, id).boundingBox()).x;
  expect(await x("inbound-watch:c1")).toBeLessThan(await x("process:bestellung"));
  expect(await x("process:versand")).toBeGreaterThan(await x("process:bestellung"));

  await expect(edge(page, "process:bestellung", "process:versand", "freigabe")).toHaveClass(/observed/);
  await expect(edge(page, "process:bestellung", "process:versand", "freigabe")).toContainText("freigabe · 3× · 3 gestartet");
  await expect(edge(page, "process:bestellung", "process:rechnung", "freigabe")).toHaveClass(/possible-only/);
  await expect(edge(page, "process:bestellung", "process:rechnung", "freigabe")).toContainText("freigabe · möglich");
  // Observed, but no longer possible: marked.
  await expect(edge(page, "outside-view", "process:bestellung", "storno")).toHaveClass(/not-possible/);

  // What the reader may not see has no name, and a black box no expander.
  await expect(box(page, "outside-view")).toContainText("Ausserhalb deiner Sicht");
  await expect(box(page, "inbound-watch:c1").locator(".ix-box-toggle")).toHaveCount(0);
  await expect(page.locator("#ix-unreceived")).toContainText("Ohne Empfänger");
  await expect(page.locator("#ix-unreceived")).toContainText("protokoll: 2×");

  // The window defaults to a day and travels to both routes.
  expect(calls[0]).toContain("window=24h");
  expect(calls.find((c) => c.startsWith("replay"))).toContain("window=24h");
  expect(page.__errors).toEqual([]);
});

test("a process expands to its ports, collapses again, and opens as a center", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  await expect(page.locator(".ix-box")).toHaveCount(5);

  await box(page, "process:bestellung").click();
  await expect(box(page, "process:bestellung")).toHaveClass(/expanded/);
  await expect(box(page, "process:bestellung").locator(".ix-port")).toHaveText([
    "→ freigabe (release)", "→ protokoll (note)", "← versandt (wait)"]);

  // A neighbour's ports come from its own graph, read with a window that reads no rows.
  await box(page, "process:versand").click();
  await expect(box(page, "process:versand").locator(".ix-port")).toHaveText(["← freigabe (s)", "→ versandt (report)"]);
  expect(calls.some((c) => c.includes("processId=versand") && c.includes("window=1ns"))).toBe(true);

  await page.locator("#ix-collapse-all").click();
  await expect(page.locator(".ix-port")).toHaveCount(0);
  await page.locator("#ix-expand-all").click();
  await expect(page.locator(".ix-box.expanded")).toHaveCount(3);

  await box(page, "process:versand").locator(".ix-box-open").click();
  await expect(page).toHaveURL(/#\/operations\/interactions\/versand$/);
  expect(page.__errors).toEqual([]);
});

test("the replay reads its history until it begins, and plays one by one or bundled", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  // Two pages, the older read before the first page's cursor.
  await expect(page.locator("#ix-now")).toContainText("Ereignis 0 von 7");
  expect(calls.filter((c) => c.startsWith("replay")).some((c) => c.includes("before=50"))).toBe(true);
  await expect(page.locator("#ix-history")).toContainText("vollständig");

  await page.locator("#ix-step").click();
  await expect(page.locator("#ix-feed li")).toHaveCount(1);
  await expect(page.locator("#ix-feed li").first()).toContainText("storno");
  await expect(page.locator("#ix-feed li").first()).toContainText("Ausserhalb deiner Sicht → Sekretariat");
  await expect(page.locator("#ix-mode")).toContainText("einzeln");
  await expect(page.locator("#ix-now")).toContainText("Ereignis 1 von 7");

  await page.locator("#ix-speed").selectOption("5");
  await page.locator("#ix-step").click();
  await expect(page.locator("#ix-mode")).toContainText("gebündelt");
  await expect(page.locator("#ix-now")).toContainText("Ereignis 6 von 7");
  await expect(page.locator("#ix-feed li.kind-unreceived")).toContainText("niemand hat sie empfangen");
  await expect(page.locator("#ix-feed li").first()).toContainText("A-3");

  await page.locator("#ix-speed").selectOption("1");
  await page.locator("#ix-step").click();
  await expect(page.locator("#ix-feed li").first()).toContainText("1× ausserhalb deiner Sicht empfangen");

  await page.locator("#ix-rewind").click();
  await expect(page.locator("#ix-now")).toContainText("Ereignis 0 von 7");
  await page.locator("#ix-play").click();
  await expect(page.locator("#ix-play")).toHaveText("Anhalten");
  await expect(page.locator("#ix-now")).toContainText("Ereignis 2 von 7", { timeout: 5000 });
  await page.locator("#ix-play").click();
  await expect(page.locator("#ix-play")).toHaveText("Abspielen");
  expect(page.__errors).toEqual([]);
});

test("Live appends what came after the newest event", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  await expect(page.locator("#ix-now")).toContainText("von 7");
  await page.locator("#ix-live").check();
  await expect(page.locator("#ix-now")).toContainText("Ereignis 8 von 8", { timeout: 10000 });
  await expect(page.locator("#ix-feed li").first()).toContainText("A-4");
  expect(calls.some((c) => c.startsWith("replay") && c.includes("after=90"))).toBe(true);
  expect(page.__errors).toEqual([]);
});
