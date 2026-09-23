import { test, expect } from "@playwright/test";

// The estate altitude (ADR-0402): one node per domain, drawn by the
// picker's third subject and read from a route of its own.
//
// What these tests are about is not the shape of the picture but what it is allowed to
// claim. The estate is read by the same right that reads the landscape — the posture the
// credential-reach record made available — and that is only honest while every domain on it
// says how wide the credential that drew it was. So the disclosure is tested as a feature,
// beside the two controls that must not offer a landscape answer to an estate question.

const notations = [
  { id: "atlas", label: "Atlas (derived)", short: "Atlas", projection: false, mappingVersion: 1, types: {}, relations: {}, loss: [] },
];

// The landscape this server derives for the reader: what the picker starts on.
const landscape = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Workplace", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", application: "application:a1", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
  ],
  edges: [{ from: "application:a1", to: "process:1", kind: "contains" }],
  restricted: 0, clustered: false, runtimeId: "rt-zurich",
};

// The estate: this runtime, a peer that answered, a peer on an older build, and a peer
// nobody could reach. Every state ADR-0402 §3 distinguishes, on one picture.
const estate = {
  nodes: [
    {
      id: "domain:local", kind: "domain", name: "Zurich (prod)", provenance: "derived",
      runtimeId: "rt-zurich", holds: 120, state: "healthy", severity: "ok",
      reason: "This is the domain you are reading from.",
    },
    {
      id: "domain:t-geneva", kind: "domain", name: "Geneva (prod)", provenance: "derived",
      runtimeId: "rt-geneva", holds: 80, restricted: 14, drawnBy: "Geneva prod",
      state: "healthy", severity: "ok",
      reason: "This peer answered and its landscape was read.",
    },
    {
      id: "domain:t-bern", kind: "domain", name: "Bern", provenance: "derived",
      drawnBy: "Bern", state: "unserved", severity: "unknown",
      reason: "This peer answered and does not serve a starmap read, so its landscape is a version boundary rather than a fault.",
    },
    {
      id: "domain:t-lugano", kind: "domain", name: "Lugano", provenance: "derived",
      drawnBy: "Lugano", state: "unreachable", severity: "attention",
      reason: "This peer refused the connection.",
    },
  ],
  edges: [
    { from: "domain:local", to: "domain:t-geneva", kind: "promotes", promoted: 3 },
  ],
  restricted: 0, clustered: false, runtimeId: "rt-zurich",
};

function installMock(page, { mesh = landscape, domains = estate } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (url.pathname === "/api/v1/panorama/estate") {
      page.__asked.push(url.pathname);
      return route.fulfill({ json: domains });
    }
    if (url.pathname === "/api/v1/panorama/mesh") {
      page.__asked.push(url.pathname + url.search);
      return route.fulfill({ json: mesh });
    }
    if (url.pathname === "/api/v1/panorama/notations") return route.fulfill({ json: notations });
    return route.fulfill({ json: [] });
  });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__asked = [];
  installMock(page);
});

// openEstate picks the altitude, which re-asks the server on a route of its own rather than
// redrawing what is on screen.
async function openEstate(page) {
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await page.locator("#mesh-notation").selectOption("estate");
  await expect(page.locator('[data-node-id="domain:local"]')).toBeVisible();
}

// The altitude is a different question, so it is asked of a different route — not of the
// landscape's with a parameter on it. A picture that came back from the landscape route
// would be the fan-out running on the route a reader opens by default.
test("the estate is asked for on its own route", async ({ page }) => {
  await openEstate(page);

  expect(page.__asked).toContain("/api/v1/panorama/estate");
  expect(page.__asked.filter((asked) => asked.startsWith("/api/v1/panorama/estate"))).toHaveLength(1);
  expect(page.__errors).toEqual([]);
});

// One node per domain, this runtime included: an estate that drew its peers and omitted
// itself would be a picture of somebody else's estate (§2).
test("every domain is one node, and this runtime is one of them", async ({ page }) => {
  await openEstate(page);

  await expect(page.locator(".mesh-node")).toHaveCount(4);
  for (const name of ["Zurich (prod)", "Geneva (prod)", "Bern", "Lugano"]) {
    await expect(page.locator(".mesh-canvas")).toContainText(name);
  }
  // The size each one stands for is on the node, because a domain standing for 120 nodes and
  // one standing for four are otherwise the same mark (§2).
  await expect(page.locator('[data-node-id="domain:local"]')).toContainText("120");
  await expect(page.locator('[data-node-id="domain:t-geneva"]')).toContainText("80");
});

// §1's disclosure, which is the whole argument for letting a landscape reader open this
// view: the picture says whose credential drew each domain and how much of it that
// credential could not see. A number without those is one a reader would compare across
// domains, which is the one comparison it does not support.
test("the picture says whose credential drew each domain, and what it could not see", async ({ page }) => {
  await openEstate(page);

  // The legend note carries both halves: the rule, and the count behind the peers.
  const note = page.locator(".mesh-note", { hasText: "as wide as the credential" }).first();
  await expect(note).toContainText("as wide as the credential that drew it");
  await expect(note).toContainText("14");
  await expect(note).toContainText("not comparable across domains");

  // And the node itself says it where a reader asks about one domain.
  const geneva = page.locator('[data-node-id="domain:t-geneva"] title');
  await expect(geneva).toContainText("credential configured for Geneva prod");
  await expect(geneva).toContainText("14 outside that credential's reach");
  await expect(geneva).toContainText("80 node(s) in its own landscape");
  // The domain the reader is standing in was drawn by their own rights, not by a credential.
  await expect(page.locator('[data-node-id="domain:local"] title'))
    .toContainText("drawn with your own rights");
});

// §3's fifth state has to read as a version boundary rather than as a fault: unreachable
// sends an operator to look at a network, stale implies there was once an answer.
test("a peer that does not serve this view is not drawn as broken", async ({ page }) => {
  await openEstate(page);

  await expect(page.locator('[data-node-id="domain:t-bern"] title'))
    .toContainText("does not serve this view");
  // Neutral, not a finding: the severity glyph an attention node carries is not on it.
  await expect(page.locator('[data-node-id="domain:t-bern"] .mesh-badge-glyph')).toHaveText("?");
  await expect(page.locator('[data-node-id="domain:t-lugano"] .mesh-badge-glyph')).toHaveText("•");
});

// The two controls that belong to the landscape must not answer an estate question. The
// drafts switch draws diagrams and an estate has none; the model export would hand back the
// landscape's ArchiMate document, which is two answers to one question.
test("the landscape's own controls are not offered on the estate", async ({ page }) => {
  await openEstate(page);

  await expect(page.locator("#mesh-drafts")).toBeDisabled();
  await expect(page.locator("#mesh-export-archimate")).toBeDisabled();

  // Back on the landscape both are available again, and the landscape is re-asked.
  await page.locator("#mesh-notation").selectOption("atlas");
  await expect(page.locator('[data-node-id="application:a1"]')).toBeVisible();
  await expect(page.locator("#mesh-drafts")).toBeEnabled();
  await expect(page.locator("#mesh-export-archimate")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});
