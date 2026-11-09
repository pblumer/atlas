// Browser-level contract for the atlas hub Console application-Blueprint module: only
// operator-verified API releases are shown, never imported on preview/GET, and
// a matching If-Match digest and explicit confirmation precede ADR-0467 import.
import { test, expect } from "@playwright/test";
import zlib from "node:zlib";

const release = {
  blueprintId: "org.example.purchase", publisherId: "org.example", version: "1.0.0",
  title: "Purchase approvals", description: '<img src=x onerror="alert(1)">', license: "Apache-2.0",
  needs: { workerTypes: [], serverConstants: [] },
};
const digest = "sha256:" + "a".repeat(64);
const index = { schemaVersion: 1, registryId: "org.example.community", releases: [release] };
const preview = {
  ...release, applicationKey: "purchase", applicationName: "Purchase",
  digest, files: [{ Path: "atlas.json", SizeBytes: 80 }],
  catalogues: [{ id: "cat-requests", items: ["badge", "access"] }, { id: "cat-department", items: ["badge"] }],
  products: [{ id: "badge", homeCatalog: "cat-requests" }, { id: "access", homeCatalog: "cat-requests" }],
};

function tarGz(files) {
  const blocks = [];
  for (const [name, content] of files) {
    const data = Buffer.from(content);
    const h = Buffer.alloc(512);
    h.write(name, 0, 100);
    h.write("0000644\0", 100);
    h.write(data.length.toString(8).padStart(11, "0") + "\0", 124);
    h.write("0", 156);
    h.write("ustar\0", 257);
    blocks.push(h, data, Buffer.alloc((512 - data.length % 512) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return zlib.gzipSync(Buffer.concat(blocks));
}
const pkg = tarGz([["atlas.json", JSON.stringify({
  formatVersion: 1, key: "purchase", name: "Purchase", processes: [], forms: [], decisions: [],
})]]);

// healthyWay is the stream GET /api/v1/blueprints/connection answers when every
// station is passed: a start line, one line per station, a done line with the index.
const healthyWay = (indexBody, extra = {}) => [
  { type: "start", host: "registry.example.org", atlasVersion: "0.24.0", stations: ["resolve", "connect", "tls", "index", "signature"] },
  { type: "step", stage: "resolve", ok: true, ms: 12, host: "registry.example.org", addresses: ["203.0.113.7", "203.0.113.8"] },
  { type: "step", stage: "connect", ok: true, ms: 31, address: "203.0.113.7:443" },
  { type: "step", stage: "tls", ok: true, ms: 44, tlsVersion: "TLS 1.3", issuer: "R11 (Let's Encrypt)" },
  { type: "step", stage: "index", ok: true, ms: 20, status: 200, bytes: 2048 },
  { type: "step", stage: "signature", ok: true, ms: 9, key: "sha256:3f9a0c1d2e4b5a6978" },
  { type: "done", ok: true, ms: 116, atlasVersion: "0.24.0", index: indexBody,
    releases: indexBody.releases.length, compatible: indexBody.releases.length, ...extra },
];

async function stubRegistry(page, { status = 200, downloadStatus = 200, downloadBody = null, previewBody = preview, previewFailure = null, indexBody = index, way = null } = {}) {
  const requests = [];
  await page.route("**/api/v1/blueprints**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requests.push({ path, ifMatch: request.headers()["if-match"] });
    if (path === "/api/v1/blueprints/connection") {
      await route.fulfill(status === 200
        ? { status: 200, contentType: "application/x-ndjson", body: (way || healthyWay(indexBody)).map((l) => JSON.stringify(l)).join("\n") + "\n" }
        : { status, contentType: "application/json", body: JSON.stringify({ error: "Registry not configured" }) });
    } else if (path === "/api/v1/blueprints") {
      await route.fulfill({ status, contentType: "application/json",
        body: JSON.stringify(status === 200 ? indexBody : { error: "Registry not configured" }) });
    } else if (path.endsWith("/preview")) {
      await route.fulfill(previewFailure
        ? { status: 502, contentType: "application/json", body: JSON.stringify(previewFailure) }
        : { status: 200, contentType: "application/json", body: JSON.stringify(previewBody) });
    } else if (path.endsWith("/package")) {
      await route.fulfill(downloadStatus === 200
        ? { status: 200, contentType: "application/gzip", body: pkg }
        : { status: downloadStatus, contentType: "application/json", body: JSON.stringify(downloadBody || { error: "Digest changed" }) });
    } else {
      await route.fulfill({ status: 404, body: "not found" });
    }
  });
  return requests;
}

test.beforeEach(async ({ page }) => {
  await page.goto("/blueprint-marketplace-harness.html");
  await page.waitForFunction(() => window.__ready === true);
});

test("only signed registry metadata is rendered and safe escaped; preview does not import", async ({ page }) => {
  const requests = await stubRegistry(page);
  await page.evaluate(() => window.__open());
  await expect(page.locator(".modal-ov")).toBeVisible();
  await expect(page.locator(".modal-ov h2")).toHaveText("atlas hub");
  await expect(page.locator(".modal-ov")).toContainText("Purchase approvals");
  await expect(page.locator(".modal-ov")).toContainText("<img src=x");
  await expect(page.locator(".modal-ov img")).toHaveCount(0);
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  await expect(page.locator(".modal-ov")).toContainText(digest);
  await expect(page.locator(".modal-ov")).toContainText("cat-requests");
  await expect(page.locator(".modal-ov")).toContainText("cat-department");
  await expect(page.locator(".modal-ov")).toContainText("badge");
  await expect(page.locator(".modal-ov")).toContainText("access");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
  expect(requests.map((r) => r.path).every((p) => !p.endsWith("/package"))).toBe(true);
  await page.locator("[data-back]").click();
  await page.locator("[data-close]").first().click();
  await expect(page.locator(".modal-ov")).toHaveCount(0);
});

test("untrusted catalogue and offered product names cannot inject markup", async ({ page }) => {
  const hostile = '<img src=x onerror="alert(1)">';
  await stubRegistry(page, {
    previewBody: {
      ...preview,
      catalogues: [{ id: hostile, items: [hostile] }],
      products: [{ id: hostile, homeCatalog: hostile }],
    },
  });
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator(".modal-ov")).toContainText(hostile);
  await expect(page.locator(".modal-ov img")).toHaveCount(0);
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("cancelled operator confirmation never downloads or imports", async ({ page }) => {
  const requests = await stubRegistry(page);
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.locator("[data-install]").click();
  expect(requests.filter((r) => r.path.endsWith("/package"))).toHaveLength(0);
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("explicit confirmation and matching digest reuses existing dry-run and import", async ({ page }) => {
  const requests = await stubRegistry(page);
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator("[data-install]").click();
  await expect(page.locator("#result")).toContainText("ok:");
  const downloaded = requests.filter((r) => r.path.endsWith("/package"));
  expect(downloaded).toHaveLength(1);
  expect(downloaded[0].ifMatch).toBe('"' + digest + '"');
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.map((x) => x.path)).toEqual([
    "/api/v1/applications/source?dryRun=true",
    "/api/v1/applications/source",
  ]);
  expect(await page.evaluate(() => window.__reloads)).toBe(1);
  await expect(page.locator(".modal-ov")).toHaveCount(0);
});

test("changed digest prevents import even when operator confirms", async ({ page }) => {
  await stubRegistry(page, { downloadStatus: 412 });
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator("[data-install]").click();
  await expect(page.locator("#result")).toContainText("Digest changed");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("server without an operator registry shows a failure without import", async ({ page }) => {
  await stubRegistry(page, { status: 503 });
  await page.evaluate(() => window.__open());
  await expect(page.locator(".modal-ov")).toContainText("Registry not configured");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("signed tags filter the listing without changing which release is previewed", async ({ page }) => {
  const tagged = {
    ...index,
    releases: [
      { ...release, tags: ["einstieg"] },
      { ...release, blueprintId: "org.example.directory", title: "Directory samples", tags: ["identitaet", "entra"] },
      { ...release, blueprintId: "org.example.untagged", title: "Untagged" },
    ],
  };
  const requests = await stubRegistry(page, { indexBody: tagged });
  await page.evaluate(() => window.__open());
  const ov = page.locator(".modal-ov");
  await expect(ov).toContainText("Directory samples");
  await expect(ov.locator("[data-tag]")).toHaveCount(4); // all + three tags
  await ov.locator('[data-tag="identitaet"]').click();
  await expect(ov.locator('[data-tag="identitaet"]')).toHaveAttribute("aria-pressed", "true");
  await expect(ov).toContainText("Directory samples");
  await expect(ov).not.toContainText("Purchase approvals");
  await expect(ov).not.toContainText("Untagged");
  // The preview button still names the release in the signed index, not its
  // position in the filtered list.
  await ov.locator("[data-preview]").first().click();
  await expect(page.locator("[data-install]")).toBeVisible();
  expect(requests.map((r) => r.path)).toContain("/api/v1/blueprints/org.example.directory/releases/1.0.0/preview");
  await ov.locator("[data-back]").click();
  await ov.locator('[data-tag=""]').click();
  await expect(ov).toContainText("Untagged");
});

test("the way to the hub is shown folded above the listing, with what each station established", async ({ page }) => {
  const two = { ...index, releases: [release, { ...release, blueprintId: "org.example.newer", title: "Newer", version: "2.0.0" }] };
  await stubRegistry(page, { way: healthyWay(two, { compatible: 1,
    incompatible: [{ blueprintId: "org.example.newer", version: "2.0.0", minAtlas: "0.25.0" }] }) });
  await page.evaluate(() => window.__open());
  const ov = page.locator(".modal-ov");
  const summary = ov.locator(".hub-connected summary");
  await expect(summary).toContainText("Verbunden mit registry.example.org in 116 ms");
  await expect(summary).toContainText("TLS 1.3");
  await expect(summary).toContainText("Releases: 2, davon für atlas 0.24.0: 1");
  await summary.click();
  const route = ov.locator(".hub-route");
  await expect(route.locator('[data-stage="resolve"]')).toHaveAttribute("data-state", "ok");
  await expect(route.locator('[data-stage="resolve"]')).toContainText("registry.example.org → 203.0.113.7 (+1 weitere)");
  await expect(route.locator('[data-stage="tls"]')).toContainText("Zertifikat von R11 (Let's Encrypt)");
  await expect(route.locator('[data-stage="index"]')).toContainText("HTTP 200 · 2,0 KB");
  await expect(route.locator('[data-stage="signature"]')).toContainText("Schlüssel 3f9a0c1d2e4b…");
  await expect(route.locator('[data-stage="hub"]')).toHaveAttribute("data-state", "ok");
  // The release this atlas is too old for says so, and is not previewed.
  await expect(ov.locator("[data-too-old]")).toHaveText("benötigt atlas ≥ 0.25.0 (dieser Server: 0.24.0)");
  await expect(ov.locator('[data-preview="1"]')).toBeDisabled();
  await expect(ov.locator('[data-preview="0"]')).toBeEnabled();
});

test("a firewall that drops shows as a connection nobody answered, and nothing after it is reached", async ({ page }) => {
  await stubRegistry(page, { way: [
    healthyWay(index)[0],
    healthyWay(index)[1],
    { type: "step", stage: "connect", ok: false, ms: 10003, cause: "timeout", address: "203.0.113.8:443", tried: 2 },
    { type: "done", ok: false, ms: 10015, atlasVersion: "0.24.0", releases: 0, compatible: 0 },
  ] });
  await page.evaluate(() => window.__open());
  const ov = page.locator(".modal-ov");
  await expect(ov.locator('[data-stage="connect"]')).toHaveAttribute("data-state", "fail");
  await expect(ov.locator('[data-stage="connect"]')).toContainText("10,0 s");
  for (const later of ["tls", "index", "signature", "hub"]) {
    await expect(ov.locator(`[data-stage="${later}"]`)).toHaveAttribute("data-state", "skipped");
  }
  const finding = ov.locator(".hub-finding");
  await expect(finding).toHaveAttribute("data-cause", "dropped");
  await expect(ov.locator('[data-stage="connect"] .hub-detail')).toHaveText("Keine Antwort auf den Verbindungsaufbau");
  // The finding stands at the station the way ended at.
  await expect(ov.locator('[data-stage="connect"] .hub-finding')).toHaveCount(1);
  await expect(finding).toContainText("Keine der 2 Adressen hat innerhalb von 5 s geantwortet");
  await expect(finding).toContainText("DROP");
  await expect(finding).toContainText("HTTPS_PROXY wird nicht verwendet");
  // Without an administrator's error text there is none to show.
  await expect(finding).not.toContainText("nur für Administratoren");
  await expect(ov.locator("[data-retry]")).toBeVisible();
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("a TLS inspection shows the issuer nobody expected, and an administrator reads the error", async ({ page }) => {
  const requests = await stubRegistry(page, { way: [
    ...healthyWay(index).slice(0, 3),
    { type: "step", stage: "tls", ok: false, ms: 40, cause: "unknown_authority", issuer: "Fortinet CA <script>",
      error: "tls: failed to verify certificate: x509: certificate signed by unknown authority" },
    { type: "done", ok: false, ms: 90, atlasVersion: "0.24.0", releases: 0, compatible: 0 },
  ] });
  await page.evaluate(() => window.__open());
  const finding = page.locator(".hub-finding");
  await expect(page.locator('[data-stage="tls"] .hub-detail')).toHaveText("Zertifikat nicht vertrauenswürdig");
  await expect(finding).toContainText("«Fortinet CA <script>»");
  await expect(finding).toContainText("TLS-Inspektion");
  await expect(finding).toContainText("certificate signed by unknown authority");
  await expect(page.locator(".modal-ov script")).toHaveCount(0);
  // Retrying asks the server again.
  await page.locator("[data-retry]").click();
  await expect(page.locator(".hub-finding")).toBeVisible();
  expect(requests.filter((r) => r.path === "/api/v1/blueprints/connection")).toHaveLength(2);
});

test("the station being waited on counts up while the others wait", async ({ page }) => {
  await page.evaluate(() => window.__route([
    { type: "start", host: "registry.example.org", atlasVersion: "0.24.0", stations: ["resolve", "connect", "tls", "index", "signature"] },
    { type: "step", stage: "resolve", ok: true, ms: 12, host: "registry.example.org", addresses: ["203.0.113.7"] },
  ]));
  const connect = page.locator('#route [data-stage="connect"]');
  await expect(connect).toHaveAttribute("data-state", "active");
  await expect(connect).toContainText("Zeitlimit 5 s je Adresse");
  await expect(connect.locator("[data-wait]")).not.toHaveText("wartet … 0 ms", { timeout: 3000 });
  await expect(page.locator('#route [data-stage="tls"]')).toHaveAttribute("data-state", "pending");
});

test("a preview the registry could not serve says where the way ended and why, and checks it again", async ({ page }) => {
  const requests = await stubRegistry(page, { previewFailure: {
    error: "trusted Blueprint registry is unavailable or invalid (stopped at tls: unknown_authority)",
    registryHost: "registry.example.org",
    station: { stage: "tls", ok: false, ms: 40, cause: "unknown_authority", issuer: "Fortinet CA <script>",
      error: "tls: failed to verify certificate: x509: certificate signed by unknown authority" },
  } });
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  const ov = page.locator(".modal-ov");
  await expect(ov.locator("[data-stopped]")).toHaveText(
    "Blueprint konnte nicht geladen werden. Der Weg zum atlas hub endete bei «TLS-Handshake»: Zertifikat nicht vertrauenswürdig.");
  // The server's English sentence is not what a person reads.
  await expect(ov).not.toContainText("unavailable or invalid");
  const finding = ov.locator(".hub-finding");
  await expect(finding).toHaveAttribute("data-cause", "unknown_authority");
  await expect(finding).toContainText("«Fortinet CA <script>»");
  await expect(finding).toContainText("registry.example.org von der Inspektion ausgenommen");
  await expect(finding).toContainText("HTTPS_PROXY wird nicht verwendet");
  await expect(finding).toContainText("certificate signed by unknown authority");
  await expect(ov.locator("script")).toHaveCount(0);
  // Checking again draws the whole way from the start.
  await ov.locator("[data-retry]").click();
  await expect(ov.locator(".hub-connected summary")).toContainText("Verbunden mit registry.example.org");
  expect(requests.filter((r) => r.path === "/api/v1/blueprints/connection")).toHaveLength(2);
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("a package that differs from the signed index is named as such, not as a network fault", async ({ page }) => {
  await stubRegistry(page, { downloadStatus: 502, downloadBody: {
    error: "Blueprint package download or signature/digest verification failed (stopped at package: package_mismatch)",
    registryHost: "registry.example.org",
    station: { stage: "package", ok: false, ms: 12, cause: "package_mismatch" },
  } });
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator("[data-install]").click();
  await expect(page.locator("#result")).toHaveText(
    "err: Import fehlgeschlagen: Der Weg zum atlas hub endete bei «Paket herunterladen und prüfen»: Paket weicht vom signierten Index ab.");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});
