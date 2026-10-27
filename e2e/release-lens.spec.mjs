import { test, expect } from "@playwright/test";

// The release lens (ADR-0495 §4, slice F): Panorama's starmap drawing one release's
// recorded architecture instead of the live landscape. These tests drive the real
// view against a mocked lens answer — the live mesh's shape plus a `lens` object — so
// they cover what a reader actually sees: a header that cannot be mistaken for the
// live landscape, the kinds a release records grouped by layer, a drill link into the
// capability register, the release's own ArchiMate download, the overlay switch, and
// saved views that belong to the picture they were saved on.

const APP = "app-7";
const VERSION = 3;
const MESH = `/api/v1/applications/${APP}/releases/${VERSION}/mesh`;
const XML = `/api/v1/applications/${APP}/releases/${VERSION}/architecture.xml`;
const LENS = `/index.html#/panorama/starmap?release=${APP}/${VERSION}`;
const TAKEN_AT = 1792000000;
const ALL_LAYERS = ["strategy", "business", "application", "data", "technology"];

// One node of every kind the lens adds, around one process, with the ids the server
// gives them (api/releasemesh.go). A data store is a dataObject under its own prefix,
// and a Worker named by an expression is the unresolved kind.
const recordedNodes = [
  { id: `application:${APP}`, kind: "application", name: "Bestellwesen" },
  { id: "process:41", kind: "process", name: "Bestellung", processId: "bestellung", version: 3, application: `application:${APP}` },
  { id: "definition:versand", kind: "definition", name: "versand", processId: "versand" },
  { id: "worker:mailer", kind: "worker", name: "mailer", workerType: "mail" },
  { id: "unresolved:worker:=approver", kind: "unresolved", name: "=approver" },
  { id: "message:order-placed", kind: "message", name: "order-placed" },
  { id: "signal:stock-low", kind: "signal", name: "stock-low" },
  { id: "jobtype:invoice-render", kind: "jobType", name: "invoice-render" },
  { id: "feed:events", kind: "feed", name: "Event feed" },
  { id: "endpoint:smtp://mail.example.test:587", kind: "endpoint", name: "smtp://mail.example.test:587" },
  { id: "capability:order-handling", kind: "capability", name: "Order handling" },
  { id: "valuestream:order-to-cash", kind: "valueStream", name: "Order to cash" },
  { id: "role:candidateGroup:sales", kind: "role", name: "sales" },
  { id: "dataobject:bestellung:order", kind: "dataObject", name: "order" },
  { id: "datastore:orders", kind: "dataObject", name: "orders" },
  { id: "businessobject:Order", kind: "businessObject", name: "Order" },
];

const edges = [
  { from: `application:${APP}`, to: "process:41", kind: "contains" },
  { from: "process:41", to: "definition:versand", kind: "calls" },
  { from: "process:41", to: "worker:mailer", kind: "uses" },
  { from: "process:41", to: "unresolved:worker:=approver", kind: "uses" },
  { from: "process:41", to: "jobtype:invoice-render", kind: "uses" },
  { from: "message:order-placed", to: "process:41", kind: "receives" },
  { from: "process:41", to: "signal:stock-low", kind: "throws" },
  { from: `application:${APP}`, to: "feed:events", kind: "throws" },
  { from: "worker:mailer", to: "endpoint:smtp://mail.example.test:587", kind: "reaches" },
  { from: "process:41", to: "capability:order-handling", kind: "realizes" },
  { from: "capability:order-handling", to: "valuestream:order-to-cash", kind: "serves" },
  { from: "role:candidateGroup:sales", to: "process:41", kind: "assigned" },
  { from: "process:41", to: "dataobject:bestellung:order", kind: "accesses" },
  { from: "process:41", to: "datastore:orders", kind: "accesses" },
  { from: "dataobject:bestellung:order", to: "businessobject:Order", kind: "realizes" },
];

// lensAnswer is what the server answers: without the overlay every node is unbound and
// unknown and observedAt is the snapshot's moment; with it the process and the Worker
// carry a live state and observedAt is now. `withheld` is a reader who asked for the
// overlay and is not an operator (ADR-0495 §5).
function lensAnswer({ overlay, withheld = false, recorded = ALL_LAYERS, nodes = recordedNodes } = {}) {
  const live = overlay && !withheld;
  return {
    nodes: nodes.map((n) => {
      if (live && (n.kind === "process" || n.kind === "worker")) return { ...n, state: "healthy", severity: "ok" };
      if (live && n.kind === "application") return { ...n, state: "unbound", severity: "ok" };
      return { ...n, state: "unbound", severity: "unknown" };
    }),
    edges: edges.filter((e) => nodes.some((n) => n.id === e.from) && nodes.some((n) => n.id === e.to)),
    restricted: 0,
    clustered: false,
    modeled: 0,
    unmodeled: 0,
    observedAt: live ? Math.floor(Date.now() / 1000) : TAKEN_AT,
    status: { ok: 0, attention: 0, critical: 0, unknown: 0, unavailable: [] },
    lens: {
      applicationId: APP, application: "Bestellwesen", releaseId: "rel-3", version: VERSION,
      takenAt: TAKEN_AT, recorded, overlay: live,
      ...(withheld ? { overlayWithheld: true } : {}),
    },
  };
}

const liveMesh = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", processId: "invoice", version: 2 },
  ],
  edges: [{ from: "application:a1", to: "process:1", kind: "contains" }],
  restricted: 0,
  clustered: false,
};

// installMock answers the lens route, the live mesh and the notations, and records
// every lens read and every live-mesh read so a test can say what was asked.
function installMock(page, options = {}) {
  const server = { lensReads: [], liveReads: 0, xml: [] };
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === MESH) {
      server.lensReads.push(url.search);
      return route.fulfill({ json: lensAnswer({ ...options, overlay: url.searchParams.get("overlay") === "1" }) });
    }
    if (path === "/api/v1/panorama/mesh") {
      server.liveReads++;
      return route.fulfill({ json: liveMesh });
    }
    if (path === "/api/v1/panorama/notations") return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
  return server;
}

function collectErrors(page) {
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  return errors;
}

test("the lens names the release and when it was recorded, never the starmap", async ({ page }) => {
  const errors = collectErrors(page);
  const server = installMock(page);
  await page.goto(LENS);

  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator(".mesh-node")).toHaveCount(recordedNodes.length);
  // The overlay is asked for by default, and the header says which release this is.
  expect(server.lensReads).toEqual(["?overlay=1"]);
  await expect(page.locator("#mesh-root h1")).toHaveCount(1);
  await expect(page.locator("#mesh-root h1")).toHaveText(/^Release v3 of Bestellwesen, as recorded .*\d\d:\d\d/);
  await expect(page.locator("#mesh-root h1")).not.toContainText("Starmap");
  await expect(page.locator(".mesh-canvas")).toHaveAttribute("aria-label", "Recorded release");

  // With the overlay on: recorded and live state, side by side, in both places that
  // say what the picture is — and never "derived from this server's resources".
  await expect(page.locator("#mesh-observed")).toContainText(/^recorded .*, live state just now$/);
  await expect(page.locator(".mesh-meta")).toContainText("recorded");
  await expect(page.locator(".mesh-meta")).toContainText("live state");
  await expect(page.locator(".mesh-meta")).not.toContainText("derived");

  // The landscape's own controls that have no meaning for a record are not offered.
  await expect(page.locator("#mesh-drafts")).toHaveCount(0);
  await expect(page.locator(".mesh-live")).toHaveText("Live overlay");
  await expect(page.locator("#mesh-live")).toBeChecked();
  await expect(page.locator('#mesh-notation option[value="products"]')).toHaveCount(0);
  await expect(page.locator('#mesh-notation option[value="instances"]')).toHaveCount(1);

  // Two kinds the lens draws in the landscape's spelling say what they mean here.
  await expect(page.locator(".mesh-legend")).toContainText("Decided at run time — a Worker named by an expression");
  await expect(page.locator(".mesh-legend")).toContainText("Definition — a process with no deployment in this release");
  // And the lens's edges are in the key, in the words of the relationship.
  await expect(page.locator(".mesh-legend")).toContainText("receives: a message or signal sets a process going");
  await expect(page.locator(".mesh-legend")).toContainText("reaches: a Worker connects to a system outside Atlas");
  expect(server.liveReads).toBe(0);
  expect(errors).toEqual([]);
});

test("the type filter is grouped by layer, and a layer's box puts its whole band down", async ({ page }) => {
  const errors = collectErrors(page);
  installMock(page);
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await expect(page.locator("#mesh-kinds .mesh-kind-layer-head"))
    .toHaveText(["Strategy", "Business", "Application", "Data", "Technology"]);
  const band = (layer) => page.locator(`#mesh-kinds .mesh-kind-layer:has(input[data-layer="${layer}"])`);
  await expect(band("data").locator("input[data-kind]")).toHaveCount(2);
  await expect(band("strategy")).toContainText("Capability");
  await expect(band("strategy")).toContainText("Value stream");
  await expect(band("technology")).toContainText("Endpoint");
  // Every layer this release records holds something, so nothing is said missing.
  await expect(page.locator(".mesh-kinds-note")).toHaveCount(0);

  await page.locator('#mesh-kinds input[data-layer="data"]').uncheck();
  await expect(page.locator(".mesh-dataObject")).toHaveCount(0);
  await expect(page.locator(".mesh-businessObject")).toHaveCount(0);
  await expect(page.locator(".mesh-node")).toHaveCount(recordedNodes.length - 3);
  await expect(page.locator('#mesh-kinds input[data-kind="dataObject"]')).not.toBeChecked();
  await expect(page.locator('#mesh-kinds input[data-kind="businessObject"]')).not.toBeChecked();

  // One kind back is half the band: the heading is ticked again, and half-ticked.
  await page.locator('#mesh-kinds input[data-kind="businessObject"]').check();
  await expect(page.locator(".mesh-businessObject")).toHaveCount(1);
  await expect(page.locator('#mesh-kinds input[data-layer="data"]')).toBeChecked();
  expect(await page.locator('#mesh-kinds input[data-layer="data"]').evaluate((box) => box.indeterminate)).toBe(true);

  await page.locator('#mesh-kinds input[data-layer="data"]').uncheck();
  await page.locator('#mesh-kinds input[data-layer="data"]').check();
  await expect(page.locator(".mesh-node")).toHaveCount(recordedNodes.length);
  expect(errors).toEqual([]);
});

test("the layers a release did not record are named under the filter", async ({ page }) => {
  installMock(page, {
    recorded: ["application", "technology"],
    nodes: recordedNodes.filter((n) => ["application", "process", "worker", "endpoint"].includes(n.kind)),
  });
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-kinds .mesh-kind-layer-head")).toHaveText(["Application", "Technology"]);
  await expect(page.locator(".mesh-kinds-note")).toHaveText("Not recorded by this release: Strategy, Business, Data.");
});

test("a capability opens its record in the capability register", async ({ page }) => {
  installMock(page);
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="capability:order-handling"] .mesh-body').click();
  const link = page.getByRole("link", { name: "Open in Capabilities" });
  await expect(link).toHaveAttribute("href", "#/panorama/capabilities/order-handling");

  await page.locator('[data-node-id="valuestream:order-to-cash"] .mesh-body').click();
  await expect(page.getByRole("link", { name: "Open in Value streams" }))
    .toHaveAttribute("href", "#/panorama/value-streams/order-to-cash");

  // A process of the release still opens on its live view, by the key it was recorded
  // with — the same link the landscape gives it.
  await page.locator('[data-node-id="process:41"] .mesh-body').click();
  await expect(page.getByRole("link", { name: "Open in Operations" })).toHaveAttribute("href", "#/operations/p/41");
});

test("the ArchiMate download is the whole release, with every layer it recorded", async ({ page }) => {
  installMock(page);
  const asked = [];
  await page.route(`**${XML}*`, (route) => {
    const url = new URL(route.request().url());
    asked.push(url.pathname + url.search);
    return route.fulfill({
      contentType: "application/xml",
      headers: { "content-disposition": 'attachment; filename="architecture-Bestellwesen-v3.xml"' },
      body: '<?xml version="1.0"?><model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"/>',
    });
  });
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const button = page.locator("#mesh-export-archimate");
  await expect(button).toHaveText("ArchiMate XML (release)");
  const [download] = await Promise.all([page.waitForEvent("download"), button.click()]);
  expect(download.suggestedFilename()).toBe("architecture-Bestellwesen-v3.xml");
  expect(asked).toEqual([`${XML}?layers=strategy%2Cbusiness%2Capplication%2Cdata%2Ctechnology`]);
});

test("the overlay switch asks for the record alone, and for the overlay again", async ({ page }) => {
  const errors = collectErrors(page);
  await page.clock.install();
  const server = installMock(page);
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  expect(server.lensReads).toEqual(["?overlay=1"]);
  await expect(page.locator(".mesh-node.mesh-sev-ok")).toHaveCount(3);

  // Off: the record alone, with nothing on it observed — no badge on any node, and the
  // neutral class in the key rather than "Unwatched".
  await page.locator("#mesh-live").uncheck();
  await expect.poll(() => server.lensReads).toEqual(["?overlay=1", ""]);
  await expect(page.locator(".mesh-node.mesh-sev-recorded")).toHaveCount(recordedNodes.length);
  await expect(page.locator(".mesh-canvas .mesh-badge")).toHaveCount(0);
  await expect(page.locator(".mesh-legend")).toContainText("Recorded — as the release was taken, not observed");
  await expect(page.locator(".mesh-legend")).not.toContainText("Unwatched");
  await expect(page.locator("#mesh-observed")).toHaveText(/^as recorded /);
  await expect(page.locator(".mesh-meta")).toContainText("as recorded");
  await expect(page.locator(".mesh-findings")).toContainText("Nothing here is observed");
  // Sizing by a live tally needs the live state, so the heat weightings go with it.
  await expect(page.locator('#mesh-notation option[value="instances"]')).toHaveCount(0);

  // And nothing is re-read: a record does not change.
  await page.clock.runFor(120_000);
  await expect.poll(() => server.lensReads.length).toBe(2);

  // On again: the overlay is asked for at once, and kept current from there.
  await page.locator("#mesh-live").check();
  await expect.poll(() => server.lensReads).toEqual(["?overlay=1", "", "?overlay=1"]);
  await expect(page.locator("#mesh-observed")).toContainText("live state");
  await expect(page.locator('#mesh-notation option[value="instances"]')).toHaveCount(1);
  await page.clock.runFor(45_000);
  await expect.poll(() => server.lensReads.length).toBe(4);
  expect(server.lensReads[3]).toBe("?overlay=1");
  expect(errors).toEqual([]);
});

test("an overlay the server withholds leaves the switch off, says why, and re-reads nothing", async ({ page }) => {
  const errors = collectErrors(page);
  await page.clock.install();
  const server = installMock(page, { withheld: true });
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const live = page.locator("#mesh-live");
  await expect(live).not.toBeChecked();
  await expect(live).toBeDisabled();
  await expect(page.locator("#mesh-overlay-note")).toBeVisible();
  await expect(page.locator("#mesh-overlay-note")).toHaveText("The live overlay is shown to operators.");
  await expect(page.locator("#mesh-observed")).toHaveText(/^as recorded /);
  await expect(page.locator("#mesh-root h1")).toHaveText(/as recorded/);
  await expect(page.locator(".mesh-node.mesh-sev-recorded")).toHaveCount(recordedNodes.length);

  await page.clock.runFor(5 * 60_000);
  await expect.poll(() => server.lensReads).toEqual(["?overlay=1"]);
  expect(errors).toEqual([]);
});

test("saved views belong to the picture they were saved on", async ({ page }) => {
  const errors = collectErrors(page);
  // A view stored before there was a second source: it carries no source field, and
  // the live landscape is the only picture it can be of.
  await page.addInitScript(() => {
    if (sessionStorage.getItem("seeded")) return;
    sessionStorage.setItem("seeded", "1");
    localStorage.setItem("atlas.panorama.views", JSON.stringify({
      version: 1,
      views: [{ id: "view-old", name: "Old live watch", term: "", direction: "dependents", depth: "2", notation: "atlas", zoom: 1, pins: [] }],
    }));
  });
  const server = installMock(page);
  await page.goto(LENS);
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  // The live view is not offered on the release.
  await expect(page.locator(".mesh-view-open")).toHaveCount(0);
  await page.fill("#mesh-view-name", "Release watch");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator(".mesh-view-open")).toHaveText(["Release watch"]);
  const stored = await page.evaluate(() => JSON.parse(localStorage.getItem("atlas.panorama.views")).views);
  expect(stored.map((v) => [v.name, v.source ?? null])).toEqual([
    ["Old live watch", null],
    ["Release watch", `release:${APP}/${VERSION}`],
  ]);

  // Opening it restores the view against the release, and asks nothing of the live
  // landscape.
  const readsBefore = server.lensReads.length;
  await page.locator(".mesh-view-open").click();
  await expect(page.locator(".mesh-node")).toHaveCount(recordedNodes.length);
  expect(server.liveReads).toBe(0);
  expect(server.lensReads.length).toBe(readsBefore);

  // On the live landscape it is the other way round.
  await page.evaluate(() => { location.hash = "#/panorama/starmap"; });
  await expect(page.locator("#mesh-root h1")).toHaveText("Starmap");
  await expect(page.locator(".mesh-view-open")).toHaveText(["Old live watch"]);
  // A name is per picture: the same name saved here is a second view, not a
  // replacement of the release's.
  await page.fill("#mesh-view-name", "Release watch");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator(".mesh-view-open")).toHaveText(["Old live watch", "Release watch"]);
  const both = await page.evaluate(() => JSON.parse(localStorage.getItem("atlas.panorama.views")).views);
  expect(both.map((v) => v.source ?? null)).toEqual([null, `release:${APP}/${VERSION}`, null]);

  // And another release has views of its own: none of these.
  await page.evaluate(() => { location.hash = "#/panorama/starmap?release=app-7/2"; });
  await expect(page.locator(".mesh-view-open")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("the architecture dialog opens the release in Panorama", async ({ page }) => {
  const errors = collectErrors(page);
  const server = installMock(page);
  // The application's release history, as the architecture dialog is reached from it.
  await page.route("**/api/v1/applications**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: APP, name: "Bestellwesen", visibility: "private" }] });
    if (path === `/api/v1/applications/${APP}/deployments`) {
      return route.fulfill({ json: { name: "Bestellwesen", version: 3, processes: 1, running: 0, finished: 0, definitions: [] } });
    }
    if (path === `/api/v1/applications/${APP}/releases`) {
      return route.fulfill({ json: [{ id: "rel-3", version: 3, publishedAt: TAKEN_AT, members: [], note: "" }] });
    }
    if (path === `/api/v1/applications/${APP}/releases/3/architecture`) {
      return route.fulfill({ json: { version: 3, layers: { recorded: ALL_LAYERS, shown: ["application", "technology"] } } });
    }
    if (path === `/api/v1/applications/${APP}/releases/3/architecture.svg`) {
      return route.fulfill({ contentType: "image/svg+xml", body: '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>' });
    }
    return route.fallback();
  });
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await page.evaluate((id) => { location.hash = "#/modeler/p/" + id; }, APP);
  await page.locator('#pd-tabs button[data-pane="deployments"]').click();
  await page.locator("button[data-architecture]").click();

  const open = page.locator(".modal [data-panorama]");
  await expect(open).toHaveText("In Panorama öffnen");
  await expect(open).toHaveAttribute("href", `#/panorama/starmap?release=${APP}/3`);
  await open.click();
  await expect(page).toHaveURL(new RegExp(`#/panorama/starmap\\?release=${APP}/3$`));
  // The dialog is the page's, so it goes with the click rather than staying over the
  // picture it opened.
  await expect(page.locator(".modal-ov")).toHaveCount(0);
  await expect(page.locator("#mesh-root h1")).toHaveText(/^Release v3 of Bestellwesen/);
  expect(server.lensReads).toEqual(["?overlay=1"]);
  expect(errors).toEqual([]);
});

// The Panorama menus are offered to modelers, but the router gates no route by role:
// the menus offer, the server refuses. So a reader who may read the release and holds
// no modeler role opens the lens from the dialog all the same, and loses only what is
// a modeler's — here the notation table, which the server refuses them.
test("a reader who is not a modeler opens the lens all the same", async ({ page }) => {
  const errors = collectErrors(page);
  const server = installMock(page);
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: true, user: { username: "vera", roles: ["user"] } } }));
  await page.route("**/api/v1/panorama/notations", (route) =>
    route.fulfill({ status: 403, json: { error: "forbidden" } }));
  await page.goto(LENS);

  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-root h1")).toHaveText(/^Release v3 of Bestellwesen/);
  await expect(page.locator(".mesh-node")).toHaveCount(recordedNodes.length);
  expect(server.lensReads).toEqual(["?overlay=1"]);
  // The menus still offer nothing of Panorama to this reader.
  await expect(page.locator("#topnav a", { hasText: "Starmap" })).toHaveCount(0);
  expect(errors).toEqual([]);
});

// A file of a release has left the app, and with it the header that said which release
// it is. So the stamp names the release rather than the starmap, and dates it by when it
// was recorded — beside the live state's moment only when the overlay was on — and the
// file is named after the release.
test("an exported picture of a release names the release and dates its recording", async ({ page }) => {
  await page.goto("/panorama-export-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
  const { recorded, overlaid, file } = await page.evaluate((takenAt) => {
    const release = { application: "Bestellwesen", version: 3, takenAt };
    const base = { source: "atlas.example.test", scope: { kind: "all" }, drawn: { nodes: 16 }, total: 16 };
    return {
      recorded: window.exporter.stampLines({ ...base, observedAt: takenAt, release: { ...release, live: false } }),
      overlaid: window.exporter.stampLines({ ...base, observedAt: takenAt + 3600, release: { ...release, live: true } }),
      file: window.exporter.exportName("svg", new Date(2026, 9, 27, 9, 5), "release-Bestellwesen-v3"),
    };
  }, TAKEN_AT);
  expect(recorded[0].text).toBe("Release v3 of Bestellwesen — the whole release");
  expect(recorded[1].text).toMatch(/^As recorded \d{4}-\d\d-\d\d \d\d:\d\d UTC[+-]\d\d:\d\d {2}· {2}Source atlas\.example\.test/);
  expect(recorded.map((l) => l.text).join("\n")).not.toContain("Observed");
  expect(overlaid[1].text).toMatch(/^Recorded \d{4}-.*, live state \d{4}-/);
  expect(file).toBe("atlas-release-Bestellwesen-v3-20261027-0905.svg");
});
