// End-to-end coverage for where an import lands (api/web/app.js).
//
// The case behind it, reported from the running server: a XOML workflow imported
// from the Applications overview turned up under "Not assigned". Nothing was
// broken — an import started inside an application does file itself there, and
// still does — but the overview is not inside one, so it had no application to
// name and quietly used none. The author was not asked and was not told, and the
// artifact was somewhere they had not chosen.
//
// So the overview now asks, with "Not assigned" kept as a real answer rather than
// as the silent default: an artifact that belongs to no application yet is a
// perfectly good outcome, it just has to be the one somebody picked. An import
// started inside an application still asks nothing — the answer is where the
// author already is, and a dialog restating that is a click for no information.
//
// These drive the REAL app (index.html → app.js) against a mocked /api/v1,
// because what is under test is which application the import request names.
import { test, expect } from "@playwright/test";

const XOML = `<SequentialWorkflow><NotificationActivity Description="Notify"/></SequentialWorkflow>`;

const BPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="D" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="p1" name="P1" isExecutable="true"><bpmn:startEvent id="s1"/></bpmn:process>
</bpmn:definitions>`;

const APPLICATIONS = [
  { id: "app-1", name: "Sven's Stuff", myRole: "owner", protected: false },
  { id: "app-2", name: "Archiv", myRole: "editor", protected: false },
  { id: "system", name: "Atlas System", myRole: "owner", protected: true },
];

// installMock answers the console's surface and records every write, so a test can
// prove which application the import named — the whole point of the dialog.
//
// taken makes the server answer as it does when the process id is already held: the
// import that says it is a new draft is refused, the one that omits ?from= — the
// deliberate replacement — is accepted (ADR-0222).
function installMock(page, { taken = false } = {}) {
  const posts = [];
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const method = route.request().method();
    if (method === "POST") posts.push(url.pathname + url.search);
    if (url.pathname.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (url.pathname.endsWith("/api/v1/applications")) {
      return route.fulfill({ json: APPLICATIONS });
    }
    if (url.pathname.endsWith("/api/v1/imports/mim")) {
      if (taken && url.searchParams.has("from")) {
        return route.fulfill({ status: 409, json: {
          error: 'another draft already uses the process id "mim_301"',
        } });
      }
      return route.fulfill({ json: {
        processId: "mim_301", name: "301", savedAt: 1,
        report: { native: 1, preserved: 0, manualReview: 0, notes: [] },
      } });
    }
    if (url.pathname.endsWith("/api/v1/drafts") && method === "POST") {
      return route.fulfill({ json: { processId: "p1", name: "P1", savedAt: 1 } });
    }
    return route.fulfill({ json: [] });
  });
  return posts;
}

// startImport opens a "Create new" menu, clicks one of its import items, and hands
// the file picker the file — the picker opens on the click, so the two are one step.
async function startImport(page, act, file) {
  await page.locator(".dropdown-toggle", { hasText: "Create new" }).first().click();
  const [chooser] = await Promise.all([
    page.waitForEvent("filechooser"),
    page.locator(`button[data-act="${act}"]`).click(),
  ]);
  await chooser.setFiles(file);
}

const xomlFile = { name: "301.xoml", mimeType: "application/xml", buffer: Buffer.from(XOML) };
const bpmnFile = { name: "p1.bpmn", mimeType: "application/xml", buffer: Buffer.from(BPMN) };

async function openOverview(page) {
  await page.goto("/index.html#/modeler");
  await expect(page.locator("h1", { hasText: "Applications" })).toBeVisible({ timeout: 20000 });
}

test("an import from the overview files itself into the application that was picked", async ({ page }) => {
  const posts = installMock(page);
  await openOverview(page);
  await startImport(page, "import-mim", xomlFile);

  // The dialog names the file, so it is clear what is being filed where.
  await expect(page.locator("#pick-opt")).toBeVisible();
  await expect(page.locator(".modal-head h2")).toContainText("301.xoml");
  await page.locator("#pick-opt").selectOption({ label: "Sven's Stuff" });
  await page.locator("[data-ok]").click();

  await expect.poll(() => posts.find((p) => p.includes("/imports/mim")), { timeout: 10000 })
    .toContain("projectId=app-1");
});

test("“Not assigned” is a real answer, and the one the dialog opens on", async ({ page }) => {
  const posts = installMock(page);
  await openOverview(page);
  await startImport(page, "import-mim", xomlFile);

  // Filing an artifact under no application stays possible — it is the first option
  // and the preselected one, so the previous behaviour is one Enter away.
  const select = page.locator("#pick-opt");
  await expect(select.locator("option").first()).toHaveText("Not assigned");
  await expect(select).toHaveValue("");
  // A protected system application is platform-managed, so it is not offered.
  await expect(select.locator("option")).toHaveCount(3);
  await page.locator("[data-ok]").click();

  await expect.poll(() => posts.some((p) => p.includes("/imports/mim")), { timeout: 10000 }).toBe(true);
  expect(posts.find((p) => p.includes("/imports/mim"))).not.toContain("projectId");
});

test("a BPMN import from the overview asks the same question", async ({ page }) => {
  const posts = installMock(page);
  await openOverview(page);
  await startImport(page, "import", bpmnFile);

  await expect(page.locator("#pick-opt")).toBeVisible();
  await page.locator("#pick-opt").selectOption({ label: "Archiv" });
  await page.locator("[data-ok]").click();

  await expect.poll(() => posts.find((p) => p.includes("/api/v1/drafts")), { timeout: 10000 })
    .toContain("projectId=app-2");
});

test("cancelling the dialog imports nothing", async ({ page }) => {
  const posts = installMock(page);
  await openOverview(page);
  await startImport(page, "import-mim", xomlFile);

  await expect(page.locator("#pick-opt")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#pick-opt")).toHaveCount(0);

  // Cancelling means cancelling: nothing is filed anywhere, least of all under an
  // application the author never chose.
  await page.waitForTimeout(500);
  expect(posts.filter((p) => p.includes("/imports/mim"))).toEqual([]);
});

test("an import started inside an application asks nothing and files itself there", async ({ page }) => {
  const posts = installMock(page);
  await page.goto("/index.html#/modeler/p/app-1");
  await expect(page.locator("h1", { hasText: "Sven's Stuff" })).toBeVisible({ timeout: 20000 });
  await startImport(page, "import-mim", xomlFile);

  await expect.poll(() => posts.find((p) => p.includes("/imports/mim")), { timeout: 10000 })
    .toContain("projectId=app-1");
  // The application is already known here, so no dialog was in the way of it.
  await expect(page.locator("#pick-opt")).toHaveCount(0);
});

test("a XOML import over a draft already held asks before replacing it", async ({ page }) => {
  const posts = installMock(page, { taken: true });
  await openOverview(page);
  page.on("dialog", (d) => d.accept());
  await startImport(page, "import-mim", xomlFile);
  await page.locator("#pick-opt").selectOption({ label: "Sven's Stuff" });
  await page.locator("[data-ok]").click();

  // Two requests: the one that claimed a free id and was refused, then the
  // replacement the author confirmed — which is the one that omits ?from=.
  await expect.poll(() => posts.filter((p) => p.includes("/imports/mim")).length, { timeout: 10000 }).toBe(2);
  const [claim, replace] = posts.filter((p) => p.includes("/imports/mim"));
  expect(claim).toContain("from=");
  expect(replace).not.toContain("from=");
  expect(replace).toContain("projectId=app-1");
});

test("declining that question keeps the draft that was already there", async ({ page }) => {
  const posts = installMock(page, { taken: true });
  await openOverview(page);
  page.on("dialog", (d) => d.dismiss());
  await startImport(page, "import-mim", xomlFile);
  await page.locator("#pick-opt").selectOption({ label: "Sven's Stuff" });
  await page.locator("[data-ok]").click();

  // The refused claim is the only request: nothing was replaced, and the author is
  // told that rather than left to read an error and guess what became of the draft.
  await expect(page.locator("#toast")).toContainText("cancelled");
  expect(posts.filter((p) => p.includes("/imports/mim")).length).toBe(1);
});
