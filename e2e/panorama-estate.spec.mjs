import { test, expect } from "@playwright/test";

// The estate altitude (ADR-0402) as its own view, which is what it is
// while the altitude is new: the shipped Starmap is not touched, so a defect here cannot take
// the landscape with it, and folding the two together is a later change.
//
// What these tests are about is not the shape of the picture but what it is allowed to claim.
// The estate is read by the same right that reads the landscape, and that is only honest while
// every domain on it says how wide the credential that drew it was. So the disclosure is
// tested as a feature, and so is the refusal to invent a way into a peer's landscape.

// The estate: this runtime, a peer that answered, a peer on an older build, and a peer nobody
// could reach. Every state ADR-0402 §3 distinguishes, on one picture.
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
  edges: [{ from: "domain:local", to: "domain:t-geneva", kind: "promotes", promoted: 3 }],
  restricted: 0, clustered: false, runtimeId: "rt-zurich",
};

// An estate of one: what every installation with no deployment target configured sees.
const alone = {
  nodes: [estate.nodes[0]],
  edges: [], restricted: 0, clustered: false, runtimeId: "rt-zurich",
};

// The landscape this domain expands into, so the one link on the picture can be followed to
// something real rather than asserted as a hash change.
const landscape = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Workplace", provenance: "derived", state: "unbound", severity: "unknown" },
  ],
  edges: [], restricted: 0, clustered: false, runtimeId: "rt-zurich",
};

function installMock(page, { domains = estate, fail = false } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (url.pathname === "/api/v1/panorama/estate") {
      page.__asked.push(url.pathname);
      if (fail) return route.fulfill({ status: 500, json: { error: "read deployment targets: disk gone" } });
      return route.fulfill({ json: domains });
    }
    if (url.pathname === "/api/v1/panorama/mesh") return route.fulfill({ json: landscape });
    if (url.pathname === "/api/v1/panorama/notations") return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__asked = [];
});

async function openEstate(page, options) {
  installMock(page, options);
  await page.goto("/index.html#/panorama/estate");
  await expect(page.locator(".estate-root h1")).toHaveText("Estate");
}

// One node per domain, this runtime included and first: an estate that drew its peers and
// omitted itself would be a picture of somebody else's estate (§2). And the number each stands
// for is on it, because a domain standing for 120 nodes and one standing for four are
// otherwise the same mark.
test("every domain is one node, and this runtime is one of them", async ({ page }) => {
  await openEstate(page);

  await expect(page.locator(".estate-node")).toHaveCount(4);
  await expect(page.locator('.estate-node[data-domain-id="domain:local"]')).toContainText("120");
  await expect(page.locator('.estate-node[data-domain-id="domain:t-geneva"]')).toContainText("80");
  for (const name of ["Zurich (prod)", "Geneva (prod)", "Bern", "Lugano"]) {
    await expect(page.locator(".estate-canvas")).toContainText(name);
  }
  // The one line is the recorded promotion, carrying how many applications travelled it.
  await expect(page.locator(".estate-edge")).toHaveCount(1);
  await expect(page.locator(".estate-edge-count")).toHaveText("3");
  expect(page.__errors).toEqual([]);
});

// §1's disclosure, which is the whole argument for letting a landscape reader open this view:
// the picture says whose credential drew each domain and how much of it that credential could
// not see, and it says the numbers are therefore not comparable.
test("the picture says whose credential drew each domain, and what it could not see", async ({ page }) => {
  await openEstate(page);

  const legend = page.locator(".estate-legend");
  await expect(legend).toContainText("as wide as the credential that drew it");
  await expect(legend).toContainText("14");
  await expect(legend).toContainText("not comparable across domains");

  // On the node, where a reader asks about one domain.
  const geneva = page.locator('.estate-node[data-domain-id="domain:t-geneva"] title');
  await expect(geneva).toContainText("credential configured for Geneva prod");
  await expect(geneva).toContainText("14 outside that credential's reach");
  await expect(page.locator('.estate-node[data-domain-id="domain:local"] title'))
    .toContainText("drawn with your own rights");

  // And in the table, which is where the words are read rather than hovered.
  const row = page.locator('.estate-table tr[data-domain-id="domain:t-geneva"]');
  await expect(row).toContainText("Geneva prod");
  await expect(row).toContainText("14");
  await expect(page.locator('.estate-table tr[data-domain-id="domain:local"]'))
    .toContainText("your own rights");
});

// §3's fifth state has to read as a version boundary rather than as a fault: unreachable sends
// an operator to look at a network, stale implies there was once an answer. So it is drawn
// neutrally, and the one that really was not reached is not.
test("a peer that does not serve this view is not drawn as broken", async ({ page }) => {
  await openEstate(page);

  const bern = page.locator('.estate-node[data-domain-id="domain:t-bern"]');
  await expect(bern).toHaveAttribute("data-tone", "unknown");
  await expect(bern.locator("title")).toContainText("does not serve this view");
  await expect(page.locator('.estate-node[data-domain-id="domain:t-lugano"]'))
    .toHaveAttribute("data-tone", "attention");
  // A domain that answered nothing carries no number rather than a zero: "nothing there" and
  // "nobody could ask" are different facts.
  await expect(bern).not.toContainText("0");
});

// The only domain with a way in is the one the reader is standing in, because that landscape is
// the one this server can draw. A link into a peer would promise a picture it does not have.
test("only the domain you are standing in can be opened", async ({ page }) => {
  await openEstate(page);

  await expect(page.locator(".estate-open")).toHaveCount(1);
  await expect(page.locator(".estate-open")).toHaveAttribute("data-domain-id", "domain:local");
  await page.locator('.estate-node[data-domain-id="domain:local"]').dblclick();
  await expect(page.locator("#mesh-root h1")).toHaveText("Starmap");
});

// An installation with no deployment target is an estate of one, and it must still say that
// the reader is standing in a domain — not show an empty picture.
test("an installation with no peers is an estate of one", async ({ page }) => {
  await openEstate(page, { domains: alone });

  await expect(page.locator(".estate-node")).toHaveCount(1);
  await expect(page.locator(".estate-edge")).toHaveCount(0);
  await expect(page.locator(".estate-table tbody tr")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

// The whole answer or none. A partial estate is a picture of a smaller estate and nothing on it
// would say so, which is why the server refuses rather than serving short — and the view has to
// show the refusal rather than an empty canvas that looks like an answer.
test("a refused read is said out loud rather than drawn as an empty estate", async ({ page }) => {
  await openEstate(page, { fail: true });

  await expect(page.locator(".estate-root .empty")).toContainText("deployment targets");
  await expect(page.locator(".estate-node")).toHaveCount(0);
});

// The altitude is reachable from the menu, beside the landscape it stands above.
test("the estate is one click from the Starmap", async ({ page }) => {
  await openEstate(page);

  await expect(page.locator("#topnav a", { hasText: "Estate" }))
    .toHaveAttribute("href", "#/panorama/estate");
  await expect(page.locator("#topnav a.active")).toHaveText("Estate");
});
