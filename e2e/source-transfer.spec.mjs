// Moving an application's source to another installation, as the Console asks it
// (ADR-0467, amended 2026-10-21).
//
// A download carries the constant values only when that is ticked, and lists what the
// processes need from the target that no download carries. An import names what the
// target still lacks before anybody publishes. Which URL the dialog downloads, what it
// lists and what it leaves out are things no Go test can see; these drive the real
// module through its own harness.
import { test, expect } from "@playwright/test";
import zlib from "node:zlib";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/source-transfer-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const result = (page) => page.locator("#result");
const dialog = (page) => page.locator(".source-download");

test("a download lists what the target needs and carries the constants when ticked", async ({ page }) => {
  page.evaluate(() => window.__download(window.NEEDS));
  await expect(dialog(page)).toBeVisible();
  for (const [kind, n] of [["server-constant", 2], ["application-constant", 1], ["worker", 1]]) {
    await expect(dialog(page).locator(`.need-group[data-kind="${kind}"] li`)).toHaveCount(n);
  }
  await expect(dialog(page).locator('[data-need="server-constant/LANGUAGE"]')).toContainText("has a fallback");
  await expect(dialog(page).locator('[data-need="worker/Postausgang"]')).toContainText("mail");
  await expect(dialog(page).locator(".source-unread")).toContainText("halb");

  // Ticked by default when the application holds values: TENANT_ID travels.
  const box = dialog(page).locator('[name="withConstants"]');
  await expect(box).toBeChecked();
  await expect(dialog(page).locator('[data-need="application-constant/TENANT_ID"]')).toContainText("travels with the download");
  await box.uncheck();
  await expect(dialog(page).locator('[data-need="application-constant/TENANT_ID"]')).toContainText("set it on the target");
  await box.check();
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/source?constants=true");
  expect(page.__errors).toEqual([]);
});

test("a download without the constants asks for the plain tree", async ({ page }) => {
  page.evaluate(() => window.__download(window.NEEDS));
  await dialog(page).locator('[name="withConstants"]').uncheck();
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/source");
});

test("an application holding no values offers none, and cancelling downloads nothing", async ({ page }) => {
  page.evaluate(() => window.__download({ applicationId: "a1", constants: 0, unread: [], needs: [] }));
  const box = dialog(page).locator('[name="withConstants"]');
  await expect(box).toBeDisabled();
  await expect(dialog(page)).toContainText("holds no constant values here");
  await expect(dialog(page)).toContainText("read nothing from the installation");
  await dialog(page).locator("[data-cancel]").click();
  await expect(result(page)).toHaveText("cancelled");
  await expect(dialog(page)).toHaveCount(0);
});

test("an import names only what the target still lacks", async ({ page }) => {
  const answer = {
    name: "Bedarf",
    needs: [
      { kind: "server-constant", name: "LANGUAGE", type: "string", fallback: true, state: "fallback" },
      { kind: "server-constant", name: "MAIL_DOMAIN", type: "string", state: "missing" },
      { kind: "application-constant", name: "TENANT_ID", type: "string", state: "type-mismatch", detail: "number" },
      { kind: "worker", name: "Postausgang", type: "mail", state: "missing" },
      { kind: "worker", name: "Archiv", type: "s3", state: "configured" },
    ],
    constantsNotSet: ["REGION"],
    needsUnread: [],
  };
  expect(await page.evaluate((a) => window.__report(a), answer)).toBe(true);
  page.evaluate((a) => window.__importNeeds(a), answer);
  const report = page.locator(".import-needs");
  await expect(report).toBeVisible();
  await expect(report.locator("h2")).toHaveText("Before Bedarf runs here");
  // A need the target answers, or a fallback covers, is not listed.
  await expect(report.locator("[data-need]")).toHaveCount(3);
  await expect(report.locator('[data-need="server-constant/MAIL_DOMAIN"]')).toContainText("missing");
  await expect(report.locator('[data-need="application-constant/TENANT_ID"]')).toContainText("held with another type: number");
  await expect(report.locator('[data-need="worker/Archiv"]')).toHaveCount(0);
  await expect(report.locator('.need-group[data-kind="not-set"]')).toContainText("REGION");
  await report.locator("[data-done]").click();
  await expect(result(page)).toHaveText("closed");

  // Nothing missing, nothing unset: nothing to report.
  expect(await page.evaluate(() => window.__report({ needs: [{ kind: "worker", name: "x", state: "configured" }] }))).toBe(false);
  expect(page.__errors).toEqual([]);
});

test("ticked catalogues make it a package, and its warnings are shown before it downloads", async ({ page }) => {
  page.evaluate(() => window.__download({
    ...window.NEEDS,
    catalogs: [{ id: "cat-a", texts: { de: "Amt" }, bound: true }, { id: "cat-b", texts: { en: "Office" }, bound: false }],
  }, { warnings: ["form f1 is asked by product p1 and is not in the application"] }));
  const boxes = dialog(page).locator('[name="catalog"]');
  await expect(boxes).toHaveCount(2);
  // The catalogue that sells the application starts ticked; another does not.
  await expect(boxes.nth(0)).toBeChecked();
  await expect(boxes.nth(1)).not.toBeChecked();
  await expect(dialog(page).locator(".package-catalogs li").first()).toContainText("sells this application");

  await dialog(page).locator("[data-ok]").click();
  await expect(dialog(page).locator(".package-warnings")).toContainText("form f1 is asked by product p1");
  await expect(dialog(page).locator("[data-ok]")).toHaveText("Download anyway");
  expect(await page.evaluate(() => window.__previews)).toEqual(
    ["/api/v1/applications/a%201/package?constants=true&catalog=cat-a&preview=true"]);
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/package?constants=true&catalog=cat-a");
});

test("a package without warnings downloads at once, and no catalogue ticked is the source tree", async ({ page }) => {
  page.evaluate(() => window.__download({ ...window.NEEDS, catalogs: [{ id: "cat-a", bound: true }] }));
  await dialog(page).locator('[name="catalog"]').uncheck();
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/source?constants=true");
  expect(await page.evaluate(() => window.__previews)).toEqual([]);

  page.evaluate(() => window.__download({ ...window.NEEDS, constants: 0, catalogs: [{ id: "cat-a", bound: true }] }));
  await dialog(page).locator("[data-ok]").click();
  await expect(result(page)).toHaveText("/api/v1/applications/a%201/package?catalog=cat-a");
});

// tarGz writes the files as the gzip tar a package is.
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
    blocks.push(h, data, Buffer.alloc((512 - (data.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return zlib.gzipSync(Buffer.concat(blocks)).toString("base64");
}

const shopPackage = tarGz([
  ["atlas.json", JSON.stringify({ formatVersion: 1, key: "k", name: "K", processes: [{ id: "p", name: "P", path: "processes/p.bpmn" }] })],
  ["processes/p.bpmn", "<definitions/>"],
  ["katalog.json", JSON.stringify({ catalogs: [{ id: "c", groups: ["{{alle}}"] }], products: [{ id: "badge", approval: { kind: "fixed", ref: "{{fm}}" } }] })],
  ["fragen.json", JSON.stringify({ alle: { kind: "group" }, fm: { kind: "account", label: { en: "approves Badge" } } })],
  ["antworten.json", JSON.stringify({ alle: "Alle", fm: "Fabienne Meier" })],
]);

test("a package asks what the directory cannot answer, plans everything at once, then installs in order", async ({ page }) => {
  const routes = {
    "POST /api/v1/applications/source?dryRun=true": { status: 200, data: { created: true, applicationId: "",
      artifacts: [{ ref: "application:k", status: "new", outcome: "created" }, { ref: "process:p", status: "new", outcome: "created" }] } },
    "POST /api/v1/catalogs/import/answers": [
      { status: 422, data: { problems: [{ name: "fm", kind: "account", question: "approves Badge", answer: "Fabienne Meier",
        reason: "unknown", detail: 'no account with the id or name "Fabienne Meier" on this server' }] } },
      { status: 200, data: { document: { catalogs: [{ id: "c", groups: ["g1"] }], products: [{ id: "badge", approval: { kind: "fixed", ref: "fmm" } }] },
        learned: [{ kind: "account", answer: "Fabienne Meier", id: "fmm", name: "Fabienne Meier-Muster" }], remembered: ["alle"] } },
    ],
    "GET /api/v1/users/assignable": { status: 200, data: [{ username: "otto" }, { username: "fmm", displayName: "Fabienne Meier-Muster" }] },
    "GET /api/v1/principals": { status: 200, data: [{ type: "user", id: "u1", name: "Otto" }, { type: "user", id: "u2", name: "Fabienne Meier-Muster" }, { type: "group", id: "g1", name: "Alle" }] },
    "POST /api/v1/catalogs/import?dryRun=true": { status: 200, data: { artifacts: [{ ref: "catalog:c", name: "c", status: "changed", outcome: "undecided" }] } },
    "POST /api/v1/applications/source?keep=catalog%3Ac": { status: 200, data: { created: true, applicationId: "a1", name: "K",
      artifacts: [{ ref: "application:k", status: "new", outcome: "created" }] } },
    "GET /api/v1/applications/a1/releases": { status: 200, data: [] },
    "POST /api/v1/applications/a1/publish": { status: 200, data: { deployed: true, release: { version: 1 }, warnings: ["worker x is not configured"] } },
    "POST /api/v1/catalogs/import?keep=catalog%3Ac": { status: 200, data: { created: ["catalog:c"], updated: [], releases: [],
      warnings: [{ subject: "badge", problem: "an answer reaches a process in the clear" }] } },
    "POST /api/v1/catalogs/import/mappings": { status: 200, data: [] },
  };
  page.evaluate(([b, r]) => window.__importPackage(b, r), [shopPackage, routes]);

  // The question the directory could not answer, with a choice among the people here.
  const answers = page.locator(".package-answers");
  await expect(answers).toContainText("approves Badge");
  // A fixed approver is an account: the choice is among usernames, not user ids.
  await expect(answers.locator('select[data-name="fm"] option')).toHaveCount(3);
  await answers.locator('select[data-name="fm"]').selectOption("fmm");
  await answers.locator("[data-ok]").click();

  // One plan for the application and the catalogues: the changed catalogue is asked.
  const choices = page.locator(".import-choices");
  await expect(choices.locator('.import-pick[value="catalog:c"]')).not.toBeChecked();
  await choices.locator("[data-ok]").click();
  await expect(result(page)).toHaveText("done: published as release 1 · shop: 1 new, 0 updated, 0 published · 1 answered as this server remembers · 1 kept for the next import");

  const calls = await page.evaluate(() => window.__calls);
  expect(calls.map((c) => c.key)).toEqual([
    "POST /api/v1/applications/source?dryRun=true",
    "POST /api/v1/catalogs/import/answers",
    "GET /api/v1/users/assignable",
    "GET /api/v1/principals",
    "POST /api/v1/catalogs/import/answers",
    "POST /api/v1/catalogs/import?dryRun=true",
    "POST /api/v1/applications/source?keep=catalog%3Ac",
    "GET /api/v1/applications/a1/releases",
    "POST /api/v1/applications/a1/publish",
    "POST /api/v1/catalogs/import?keep=catalog%3Ac",
    "POST /api/v1/catalogs/import/mappings",
  ]);
  // The answers go as the package has them, and the person's choice beside them: the
  // server remembers it against the name the package gave, once the catalogues are in.
  expect(calls[1].body.answers).toEqual({ alle: "Alle", fm: "Fabienne Meier" });
  expect(calls[1].body.chosen).toEqual({});
  expect(calls[4].body.answers).toEqual({ alle: "Alle", fm: "Fabienne Meier" });
  expect(calls[4].body.chosen).toEqual({ fm: "fmm" });
  expect(calls[10].body).toEqual({ mappings: [{ kind: "account", answer: "Fabienne Meier", id: "fmm", name: "Fabienne Meier-Muster" }] });
  expect(calls[5].body).toEqual({ catalogs: [{ id: "c", groups: ["g1"] }], products: [{ id: "badge", approval: { kind: "fixed", ref: "fmm" } }] });
  expect(await page.evaluate(() => window.__out.warnings)).toEqual(
    ["worker x is not configured", "badge: an answer reaches a process in the clear"]);
  expect(page.__errors).toEqual([]);
});

test("a source tree is a package without a shop: nothing is asked, nothing published", async ({ page }) => {
  const tree = tarGz([["atlas.json", JSON.stringify({ formatVersion: 1, key: "k", name: "K" })]]);
  const routes = {
    "POST /api/v1/applications/source?dryRun=true": { status: 200, data: { applicationId: "a1", artifacts: [{ ref: "application:k", status: "unchanged", outcome: "unchanged" }] } },
    "POST /api/v1/applications/source": { status: 200, data: { applicationId: "a1", name: "K", artifacts: [] } },
  };
  page.evaluate(([b, r]) => window.__importPackage(b, r), [tree, routes]);
  await expect(result(page)).toHaveText("done: ");
  expect((await page.evaluate(() => window.__calls)).map((c) => c.key)).toEqual(
    ["POST /api/v1/applications/source?dryRun=true", "POST /api/v1/applications/source"]);
});

test("cancelling a question installs nothing", async ({ page }) => {
  const routes = {
    "POST /api/v1/applications/source?dryRun=true": { status: 200, data: { created: true, applicationId: "", artifacts: [] } },
    "POST /api/v1/catalogs/import/answers": { status: 422, data: { problems: [{ name: "alle", kind: "group", reason: "unknown", detail: "no group" }] } },
    "GET /api/v1/users/assignable": { status: 200, data: [] },
    "GET /api/v1/principals": { status: 200, data: [] },
  };
  page.evaluate(([b, r]) => window.__importPackage(b, r), [shopPackage, routes]);
  await page.locator(".package-answers [data-cancel]").click();
  await expect(result(page)).toHaveText("cancelled");
  expect((await page.evaluate(() => window.__calls)).every((c) => !c.key.includes("source?") || c.key.includes("dryRun"))).toBe(true);
});
