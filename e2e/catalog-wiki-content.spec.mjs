// A product's content from the wiki
// (ADR-draft-service-content-from-an-external-source), as the three screens show it.
//
// The Go suite decides every rule — what refuses a page, when content is withheld,
// what a release exposes. This drives the REAL pages against a mocked /api/v1 and
// checks what only a browser can: that a catalogue without a source looks as it did,
// that a wiki page's words arrive as text even when they look like markup, that every
// refusal is on screen, that an unlink asks for its reason, and that the shop links to
// the page only as the release lets it.
import { test, expect } from "@playwright/test";

// The wiki's words are the attacker's here: a title and a body that would be markup
// if anything put them through innerHTML.
const EVIL_TITLE = '<img src=x onerror="window.__pwned=1">VPN';
const EVIL_TEXT = "Sicherer Zugang.\n\n<b>nicht fett</b><script>window.__pwned=2</script>";

const CATALOG = {
  id: "cat_1", revision: 3, rank: 1, languages: ["de", "fr"], texts: { de: "Verwaltung" },
  groups: ["grp_all"], items: ["vpn", "mail"], edges: [],
};

const unbound = { id: "vpn", homeCatalog: "cat_1", state: "active", revision: 7, texts: { de: "VPN" },
  approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" };
const mail = { id: "mail", homeCatalog: "cat_1", state: "active", revision: 1, texts: { de: "Mail" },
  approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d" };

const NS = 1_790_000_000_000_000_000;
const bound = {
  ...unbound,
  descriptions: { de: EVIL_TEXT, fr: "Accès sécurisé." },
  keywords: ["vpn", "<i>zugang</i>"],
  contentSource: {
    source: "wiki", primary: "de",
    pages: {
      de: { ref: "it_de", version: 4, fetchedAt: NS, url: "https://wiki.example.org/#/item/SVC-1", title: EVIL_TITLE,
        children: [{ ref: "it_de1", version: 2, title: "Details" }] },
      fr: { ref: "it_fr", version: 2, fetchedAt: NS, url: "javascript:alert(1)", title: "VPN FR" },
    },
  },
};

const SOURCES = [{ id: "wiki", name: "Firmenwiki", kind: "wikiwikibangbang" }];

// installMock answers the catalogue screen's reads, and records every write.
function installMock(page, { items, status, writes, catalogRefresh }) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    const method = req.method();
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/catalogs/cat_1")) return route.fulfill({ json: CATALOG });
    if (path.endsWith("/api/v1/catalog-products")) return route.fulfill({ json: items });
    if (path.endsWith("/content/search")) {
      return route.fulfill({ json: [{ id: "it_de", key: "SVC-1", title: EVIL_TITLE, space: "IT", state: "published",
        url: "https://wiki.example.org/#/item/SVC-1" }] });
    }
    if (path.endsWith("/content/preview")) {
      return route.fulfill({ json: {
        input: url.searchParams.get("page"), page: { ref: "it_de", key: "SVC-1", title: EVIL_TITLE, version: 4,
          url: "https://wiki.example.org/#/item/SVC-1" },
        space: "IT", state: "published", text: EVIL_TEXT,
        children: [{ ref: "it_de1", key: "SVC-2", title: "Details", version: 2 }],
        dropped: [{ page: "SVC-1", what: "passage" }, { page: "SVC-3", what: "page", reasons: ["SVC-3 carries a read restriction"] },
          { page: "SVC-1", what: "table" }],
        refusals: ["SVC-1 is in space HR, which is not released for the shop"],
        boundTo: "laptop",
      } });
    }
    if (method === "POST" && path.endsWith("/catalogs/cat_1/content/refresh")) {
      writes.push({ path, body: null });
      return route.fulfill({ json: catalogRefresh || [] });
    }
    if (method === "POST" && /\/catalog-products\/vpn\/content(\/(refresh|unlink))?$/.test(path)) {
      const body = JSON.parse(req.postData() || "{}");
      writes.push({ path, body });
      if (path.endsWith("/content")) {
        return route.fulfill({ status: 422, json: { error: "the pages may not supply the shop", problems: [
          { lang: "de", page: "SVC-1", message: "is in space HR, which is not released for the shop" },
          { lang: "fr", page: "SVC-9", message: "the account cannot read it" },
        ] } });
      }
      return route.fulfill({ json: bound });
    }
    if (/\/catalog-products\/[^/]+\/content$/.test(path)) return route.fulfill({ json: status });
    if (path.endsWith("/releases")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
}

async function openProduct(page, id) {
  await page.goto("/index.html?lang=en#/catalog/c/cat_1");
  const edit = page.locator(`[data-act="edit"][data-id="${id}"]`);
  await expect(edit).toBeVisible({ timeout: 15000 });
  await edit.click();
  await expect(page.locator(".product-form")).toBeVisible();
}

test.afterEach(async ({ page }) => {
  expect(await page.evaluate(() => window.__pwned || 0), "a wiki page's words ran as markup").toBe(0);
});

test("without a content source the catalogue screen is unchanged", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, { items: [unbound, mail], status: { sources: [], shown: true, languages: ["de", "fr"] }, writes: [] });
  await openProduct(page, "vpn");
  await page.waitForTimeout(300);
  await expect(page.locator(".content-panel")).toBeEmpty();
  await expect(page.locator(".content-catalog")).toBeEmpty();
  await expect(page.locator('textarea[name="d-de"]')).toBeEditable();
  await expect(page.locator('input[name="picture"]')).toBeEnabled();
  expect(errors).toEqual([]);
});

test("linking previews a page as text and shows every refusal", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const writes = [];
  installMock(page, { items: [unbound, mail], writes,
    status: { sources: SOURCES, shown: true, languages: ["de", "fr"] } });
  await openProduct(page, "vpn");

  const panel = page.locator(".content-panel");
  await expect(panel).toContainText("Content from the wiki");
  const slots = panel.locator(".content-slot");
  await expect(slots).toHaveCount(2);

  // Search, choose the hit, and read its preview.
  await slots.nth(0).locator('input[type="text"]').fill("vpn");
  await slots.nth(0).getByRole("button", { name: "Search" }).click();
  await expect(slots.nth(0).locator(".content-hit-list")).toContainText(EVIL_TITLE);
  await slots.nth(0).getByRole("button", { name: "Choose" }).click();
  const preview = slots.nth(0).locator(".content-preview");
  await expect(preview).toBeVisible();
  await expect(preview.locator(".content-text")).toHaveText(EVIL_TEXT);
  await expect(preview).toContainText("not released for the shop");
  await expect(preview).toContainText("already linked to product laptop");
  await expect(preview).toContainText("protected passage");
  await expect(preview).toContainText("child page, with everything below it — SVC-3 carries a read restriction");
  await expect(preview).toContainText('block "table"');
  await expect(panel.locator("img, script, b:text('nicht fett')")).toHaveCount(0);

  // Link posts every slot at once, and every problem of a refusal is shown.
  await slots.nth(1).locator('input[type="text"]').fill("SVC-9");
  await panel.getByRole("button", { name: "Link", exact: true }).click();
  const report = panel.locator(".content-report");
  await expect(report).toContainText("de: SVC-1 — is in space HR");
  await expect(report).toContainText("fr: SVC-9 — the account cannot read it");
  expect(writes).toHaveLength(1);
  expect(writes[0].body).toEqual({ source: "wiki", primary: "de", pages: { de: "it_de", fr: "SVC-9" }, revision: 7 });
  expect(errors).toEqual([]);
});

test("a bound product shows its pages and quarantine, and an unlink asks for its reason", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const writes = [];
  installMock(page, { items: [bound, mail], writes,
    status: { sources: SOURCES, binding: bound.contentSource, shown: false, languages: ["de", "fr"],
      state: { itemId: "vpn", source: "wiki", lastChecked: NS + 2e12, lastPassed: NS,
        quarantine: { since: NS + 1e12, reason: "SVC-1 carries a read restriction" } } },
    catalogRefresh: [
      { item: "vpn", status: "refused", message: "the pages may not supply the shop",
        problems: [{ lang: "de", page: "SVC-1", message: "carries a read restriction" }] },
      { item: "mail", status: "skipped", message: "its home catalogue is maintained by somebody else" },
    ] });
  await openProduct(page, "vpn");

  const panel = page.locator(".content-panel");
  await expect(panel).toContainText("Linked to Firmenwiki.");
  await expect(panel).toContainText("Withheld from the shop");
  await expect(panel).toContainText("Quarantined since");
  await expect(panel).toContainText("SVC-1 carries a read restriction");
  // The page's title is text, its link opens a new tab with no opener, and an address
  // that is not https is not a link at all.
  const link = panel.locator(".content-pages a");
  await expect(link).toHaveCount(1);
  await expect(link).toHaveText(EVIL_TITLE);
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(link).toHaveAttribute("rel", /noopener/);
  await expect(link).toHaveAttribute("href", "https://wiki.example.org/#/item/SVC-1");
  await expect(panel).toContainText("Details");

  // The supplied fields are read-only in the form, and drawn as text.
  const form = page.locator(".product-form");
  await expect(form.locator('[data-bound-desc="de"]')).toHaveText(EVIL_TEXT);
  await expect(form.locator('textarea[name="d-de"]')).toHaveCount(0);
  await expect(form.locator('input[name="keywords"]')).toHaveCount(0);
  await expect(form.locator("[data-bound-keywords]")).toHaveText("vpn, <i>zugang</i>");
  await expect(form.locator('input[name="picture"]')).toBeDisabled();
  await expect(form.locator('input[name="dropPicture"]')).toBeDisabled();

  // The primary page goes only with the whole binding.
  const unlinks = panel.locator(".content-pages button");
  await expect(unlinks.nth(0)).toBeDisabled();

  // Unlinking the whole binding asks for a reason and says it is audited.
  await panel.getByRole("button", { name: "Unlink the whole binding" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("recorded in the audit log");
  await dialog.getByRole("button", { name: "Unlink", exact: true }).click();
  await expect(dialog).toContainText("Nothing is unlinked without a reason.");
  expect(writes).toHaveLength(0);
  await dialog.locator("textarea").fill("Service retired");
  await dialog.getByRole("button", { name: "Unlink", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0].path).toMatch(/\/content\/unlink$/);
  expect(writes[0].body).toEqual({ reason: "Service retired", revision: 7 });
  expect(errors).toEqual([]);
});

test("the catalogue's refresh lists every product with its outcome", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const writes = [];
  installMock(page, { items: [bound, mail], writes,
    status: { sources: SOURCES, binding: bound.contentSource, shown: true, languages: ["de", "fr"] },
    catalogRefresh: [
      { item: "vpn", status: "refused", message: "the pages may not supply the shop",
        problems: [{ lang: "de", page: "SVC-1", message: "carries a read restriction" }] },
      { item: "mail", status: "skipped", message: "its home catalogue is maintained by somebody else" },
    ] });
  await page.goto("/index.html?lang=en#/catalog/c/cat_1");
  const go = page.getByRole("button", { name: "Refresh all wiki content" });
  await expect(go).toBeVisible({ timeout: 15000 });
  await go.click();
  const report = page.locator(".content-catalog-report");
  await expect(report).toContainText("0 refreshed, 1 refused, 1 skipped.");
  await expect(report).toContainText("de: SVC-1 — carries a read restriction");
  await expect(report).toContainText("its home catalogue is maintained by somebody else");
  expect(writes.map((w) => w.path)).toEqual(["/api/v1/catalogs/cat_1/content/refresh"]);
  expect(errors).toEqual([]);
});

test("an administrator checks a source and is told which products block its removal", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => d.accept());
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/secrets")) return route.fulfill({ json: [{ name: "wiki_token" }] });
    if (path.endsWith("/content-sources/wiki/check")) {
      return route.fulfill({ json: { reachable: true, scope: "write", spaces: ["IT"],
        problems: ["the token's scope is write, not read", "the account holds space admin in HR"] } });
    }
    if (req.method() === "DELETE" && path.endsWith("/content-sources/wiki")) {
      return route.fulfill({ status: 409, json: { error: "content source wiki still supplies vpn, laptop; unlink them first" } });
    }
    if (path.endsWith("/api/v1/content-sources")) {
      return route.fulfill({ json: [{ id: "wiki", name: "Firmenwiki", kind: "wikiwikibangbang",
        baseURL: "https://wiki.example.org", credentialsRef: "wiki_token", excludedLabels: ["withdrawn"] }] });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html?lang=en#/console/content-sources");
  await expect(page.locator("#view h2").first()).toHaveText("Content sources", { timeout: 15000 });
  await expect(page.locator("#view table")).toContainText("Firmenwiki");
  await page.getByRole("button", { name: "Check" }).click();
  const check = page.locator(".content-check");
  await expect(check).toContainText("Token scope: write");
  await expect(check).toContainText("Released for the shop (viewer): IT");
  await expect(check).toContainText("the token's scope is write, not read");
  await expect(check).toContainText("the account holds space admin in HR");
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page.locator(".content-source-report")).toContainText("still supplies vpn, laptop");
  await page.getByRole("button", { name: "New source" }).click();
  await expect(page.locator(".content-source-form input[name=baseURL]")).toBeVisible();
  expect(errors).toEqual([]);
});

// installShop is the shop's fetch for one release carrying one bound product, with or
// without the binding — which is the server's whole answer about whether to link.
function installShop(withBinding) {
  const item = {
    id: "vpn", homeCatalog: "cat_s", state: "active", texts: { de: "VPN", en: "VPN" },
    descriptions: { de: "Sicherer Zugang.", en: "Secure access." },
    approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
  };
  if (withBinding) {
    item.contentSource = { source: "wiki", primary: "de", pages: {
      de: { ref: "it_de", url: "https://wiki.example.org/#/item/SVC-1" },
      en: { ref: "it_en", url: "https://wiki.example.org/#/item/SVC-2" } } };
  }
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": { id: "cat_s", rank: 1, languages: ["de", "en"], texts: { de: "Katalog", en: "Catalogue" }, items: ["vpn"] },
    "/api/v1/catalogs/cat_s/releases": [{ id: "rel_s", catalogId: "cat_s", items: [item], waves: [["vpn"]], withoutApproval: ["vpn"] }],
    "/api/v1/orders": [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
  };
  window.fetch = (input) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) {
      return Promise.resolve(new Response(JSON.stringify(ROUTES[path]), { status: 200, headers: { "content-type": "application/json" } }));
    }
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

for (const withBinding of [true, false]) {
  test(`the shop links to the wiki only when the release carries the binding (${withBinding})`, async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.addInitScript(installShop, withBinding);
    await page.goto("/shop.html?lang=en");
    const info = page.getByRole("button", { name: "About this service" }).first();
    await expect(info).toBeVisible({ timeout: 10000 });
    await info.click();
    await expect(page.locator(".product-description")).toHaveText("Secure access.");
    const link = page.locator(".product-wiki a");
    if (withBinding) {
      await expect(link).toHaveText("More in the wiki");
      await expect(link).toHaveAttribute("href", "https://wiki.example.org/#/item/SVC-2");
      await expect(link).toHaveAttribute("target", "_blank");
      await expect(link).toHaveAttribute("rel", /noopener/);
    } else {
      await expect(link).toHaveCount(0);
    }
    expect(errors).toEqual([]);
  });
}
