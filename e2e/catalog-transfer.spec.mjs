// Moving a catalogue between installations, from the catalogue screen
// (ADR-0451), and asking before an import replaces what changed here
// (ADR-0467).
//
// The export buttons ask the server for the document and hand it to the browser as a
// file; the import reads a file and posts it. Neither is something a Go test can see:
// which route the page asks, with which query, what the file is called, and what a
// refusal looks like on the page. These drive the real detail and list views through
// the editor harness's mock.
import { test, expect } from "@playwright/test";

async function open(page) {
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".cat-transfer")).toBeVisible();
  await page.evaluate(() => { window.__sent.length = 0; window.__toasts.length = 0; });
}

const sent = (page, method) => page.evaluate((m) =>
  window.__sent.filter((c) => String(c.method).toUpperCase() === m), method);

const aFile = (doc) => ({
  name: "products.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(doc)),
});

test("the catalogue exports with its processes when that is ticked", async ({ page }) => {
  await open(page);
  await page.locator('.cat-transfer [name="withProcesses"]').check();
  const download = page.waitForEvent("download");
  await page.locator('.cat-transfer [data-act="export-catalog"]').click();
  expect((await download).suggestedFilename()).toBe("catalogue-c1.json");
  const [call] = await sent(page, "GET");
  expect(call.url).toBe("/api/v1/catalogs/c1/document?processes=true");
});

test("the ticked products export, and none ticked asks for some", async ({ page }) => {
  await open(page);
  await page.locator('.cat-transfer [data-act="export-products"]').click();
  await expect.poll(() => page.evaluate(() => window.__toasts.filter((t) => t.kind === "err").length)).toBe(1);
  expect(await sent(page, "GET")).toEqual([]);

  await page.locator('.export-pick[value="p2"]').check();
  await page.locator('.export-pick[value="p3"]').check();
  const download = page.waitForEvent("download");
  await page.locator('.cat-transfer [data-act="export-products"]').click();
  expect((await download).suggestedFilename()).toBe("products-c1.json");
  const [call] = await sent(page, "GET");
  expect(call.url).toBe("/api/v1/catalog-products/document?ids=p2,p3");
});

test("importing products posts the file into this catalogue", async ({ page }) => {
  await open(page);
  const doc = { products: [{ id: "neu", texts: { de: "Neu" } }] };
  await page.locator('.cat-transfer [name="productsFile"]').setInputFiles(aFile(doc));
  await page.locator('.cat-transfer [data-act="import-products"]').click();
  // The plan first, then — nothing on the server differing — the import itself.
  await expect.poll(async () => (await sent(page, "POST")).length).toBe(2);
  const [plan, call] = await sent(page, "POST");
  expect(plan.url).toBe("/api/v1/catalogs/import?into=c1&dryRun=true");
  expect(call.url).toBe("/api/v1/catalogs/import?into=c1");
  expect(call.body).toEqual(doc);
  const toasts = await page.evaluate(() => window.__toasts);
  expect(toasts.map((t) => t.msg).join(" ")).toContain("1 created");
});

test("an import that would replace what changed here asks first, one box each", async ({ page }) => {
  await open(page);
  await page.evaluate(() => {
    window.__importPlan = [
      { ref: "product:p1", name: "P1", status: "unchanged", outcome: "unchanged" },
      { ref: "product:p2", name: "Laptop", status: "changed", outcome: "undecided" },
      { ref: "product:p3", name: "Monitor", status: "changed", outcome: "undecided" },
    ];
  });
  await page.locator('.cat-transfer [name="productsFile"]').setInputFiles(aFile({ products: [] }));
  await page.locator('.cat-transfer [data-act="import-products"]').click();
  const dialog = page.locator(".import-choices");
  await expect(dialog).toContainText("2 changed on both sides");
  await expect(dialog.locator(".import-pick")).toHaveCount(2);
  // Unticked is keep: nothing is replaced unless somebody ticks it.
  await dialog.locator('.import-pick[value="product:p2"]').check();
  await dialog.locator("[data-ok]").click();
  await expect.poll(async () => (await sent(page, "POST")).length).toBe(2);
  const [, call] = await sent(page, "POST");
  expect(decodeURIComponent(call.url)).toBe("/api/v1/catalogs/import?into=c1&overwrite=product:p2&keep=product:p3");
});

test("what is newer in the file starts ticked, what changed here only does not", async ({ page }) => {
  await open(page);
  await page.evaluate(() => {
    window.__importPlan = [
      { ref: "product:p2", name: "Laptop", status: "changed", outcome: "undecided" },
      { ref: "product:p3", name: "Monitor", status: "behind", outcome: "updated" },
      { ref: "product:p4", name: "Maus", status: "behind", outcome: "updated" },
      { ref: "product:p5", name: "Dock", status: "ahead", outcome: "kept" },
    ];
  });
  await page.locator('.cat-transfer [name="productsFile"]').setInputFiles(aFile({ products: [] }));
  await page.locator('.cat-transfer [data-act="import-products"]').click();
  const dialog = page.locator(".import-choices");
  await expect(dialog.locator('.import-pick[value="product:p3"]')).toBeChecked();
  await expect(dialog.locator('.import-pick[value="product:p5"]')).not.toBeChecked();
  await expect(dialog.locator('.import-group[data-status="ahead"]')).toContainText("Changed here only");
  // Left as they start, behind and ahead are not named; a box moved is.
  await dialog.locator('.import-pick[value="product:p4"]').uncheck();
  await dialog.locator("[data-ok]").click();
  await expect.poll(async () => (await sent(page, "POST")).length).toBe(2);
  const [, call] = await sent(page, "POST");
  expect(decodeURIComponent(call.url)).toBe("/api/v1/catalogs/import?into=c1&keep=product:p2,product:p4");
});

test("cancelling the choice imports nothing", async ({ page }) => {
  await open(page);
  await page.evaluate(() => {
    window.__importPlan = [{ ref: "product:p2", name: "Laptop", status: "changed", outcome: "undecided" }];
  });
  await page.locator('.cat-transfer [name="productsFile"]').setInputFiles(aFile({ products: [] }));
  await page.locator('.cat-transfer [data-act="import-products"]').click();
  await page.locator(".import-choices [data-cancel]").click();
  await expect(page.locator(".import-choices")).toHaveCount(0);
  expect((await sent(page, "POST")).length).toBe(1);
});

test("a refused import lists every problem and the processes it deployed", async ({ page }) => {
  await open(page);
  await page.evaluate(() => {
    window.__importRefusal = {
      problems: [{ subject: "catalog:c1", problem: "you do not maintain this catalogue" }],
      deployed: ["prov-badge"],
    };
  });
  await page.locator('.cat-transfer [name="productsFile"]').setInputFiles(aFile({ products: [] }));
  await page.locator('.cat-transfer [data-act="import-products"]').click();
  const report = page.locator(".transfer-report");
  await expect(report).toContainText("Not imported.");
  await expect(report).toContainText("catalog:c1");
  await expect(report).toContainText("you do not maintain this catalogue");
  await expect(report).toContainText("prov-badge");
});

test("the catalogue list imports a whole file, published when asked", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  await page.evaluate(() => window.__mountList());
  await expect(page.locator(".cat-import")).toBeVisible();
  await page.evaluate(() => { window.__sent.length = 0; });

  const doc = { catalogs: [{ id: "neu", rank: 9, languages: ["de"] }] };
  await page.locator('.cat-import [name="catalogFile"]').setInputFiles(aFile(doc));
  await page.locator('.cat-import [name="publish"]').check();
  await page.locator('.cat-import [data-act="import-catalog"]').click();
  await expect.poll(async () => (await sent(page, "POST")).length).toBe(2);
  const [plan, call] = await sent(page, "POST");
  expect(plan.url).toBe("/api/v1/catalogs/import?dryRun=true");
  expect(call.url).toBe("/api/v1/catalogs/import");
  expect(call.body).toEqual({ ...doc, publish: true });
});

test("the catalogue list shows the table of names package imports keep, to remove from and add to", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  await page.evaluate(() => {
    window.__mappings = [
      { kind: "group", answer: "Fachbereich Bau", id: "grp_all", name: "Everybody", here: true },
      { kind: "account", answer: "Fabienne Meier", id: "weg", name: "", here: false },
    ];
    return window.__mountList();
  });
  const card = page.locator(".cat-mappings");
  await expect(card.locator("tbody tr")).toHaveCount(2);
  await expect(card.locator('tr[data-answer="Fabienne Meier"]')).toContainText("no longer here");
  await page.evaluate(() => { window.__sent.length = 0; });

  await card.locator('tr[data-answer="Fachbereich Bau"] [data-act="mapping-remove"]').click();
  await expect.poll(() => sent(page, "DELETE")).toHaveLength(1);
  expect((await sent(page, "DELETE"))[0].url).toBe("/api/v1/catalogs/import/mappings?kind=group&answer=Fachbereich+Bau");

  // An approver is chosen among the accounts, by username.
  await card.locator('.mapping-add [name="kind"]').selectOption("account");
  await expect(card.locator('.mapping-add [name="id"] option')).toHaveText(["Arno"]);
  await card.locator('.mapping-add [name="answer"]').fill("Arno Muster");
  await card.locator(".mapping-add button[type=submit]").click();
  await expect.poll(async () => (await sent(page, "POST")).filter((c) => c.url.endsWith("/mappings"))).toHaveLength(1);
  const [post] = (await sent(page, "POST")).filter((c) => c.url.endsWith("/mappings"));
  expect(post.body).toEqual({ mappings: [{ kind: "account", answer: "Arno Muster", id: "arno" }] });
});
