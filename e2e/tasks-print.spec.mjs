// Printing a task (ADR-0555 §6) and naming the form revision a completion answered
// through (ADR-0554 §2). The detail pane is copied into a print root that carries
// what was typed, leaves the buttons and tabs off the paper and goes once the print
// dialog closes; the completion names the revision the form was rendered in. These
// drive the REAL app shell against a mocked /api/v1.
import { test, expect } from "@playwright/test";

const listing = (items) => ({ items, total: items.length, totalExact: true, truncated: false });

const TASK = {
  key: 401, processInstanceKey: 9401, processDefKey: 4, processId: "freigabe",
  elementId: "approve", name: "Ausgabe freigeben", formId: "freigabe-form", priority: 50,
};

const FORM = {
  id: "freigabe-form",
  revision: "sha256:" + "a".repeat(64),
  schema: {
    type: "default", id: "freigabe-form",
    components: [
      { key: "betrag", type: "number", label: "Betrag", disabled: true },
      { key: "genehmigt", type: "checkbox", label: "Genehmigt" },
      { key: "kommentar", type: "textfield", label: "Kommentar" },
    ],
  },
};

function installMock(page, sent) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() === "POST") {
      let body = null;
      try { body = JSON.parse(req.postData() || "null"); } catch { body = req.postData(); }
      sent.push({ path, body });
      return route.fulfill({ status: 200, json: { taskKey: TASK.key } });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: listing([TASK]) });
    if (path.endsWith("/api/v1/approvals")) return route.fulfill({ json: listing([]) });
    if (path.match(/\/api\/v1\/forms\//)) return route.fulfill({ json: FORM });
    if (path.match(/\/api\/v1\/instances\/\d+\/variables$/)) return route.fulfill({ json: { betrag: 1250 } });
    return route.fulfill({ json: [] });
  });
}

test("a task prints as it is being filled in, and its completion names the form revision", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // The print dialog is the browser's; what matters is what is laid out for it.
  await page.addInitScript(() => { window.print = () => { window.__printed = (window.__printed || 0) + 1; }; });
  const sent = [];
  installMock(page, sent);
  await page.goto("/index.html#/tasks");
  await page.locator('.tasks-item[data-key="401"]').click();
  await expect(page.locator("#task-form .fjs-container")).toHaveCount(1, { timeout: 15000 });

  await page.locator("#task-form input[type=text]:not([disabled])").fill("Budget vorhanden");
  await page.locator("#task-form input[type=checkbox]").check();
  await page.locator("#task-print").click();
  expect(await page.evaluate(() => window.__printed)).toBe(1);

  const root = page.locator(".task-print-root");
  await expect(root).toHaveCount(1);
  const values = await root.locator("input").evaluateAll((is) =>
    is.map((i) => (i.type === "checkbox" ? String(i.checked) : i.value)));
  expect(values).toEqual(["1250", "true", "Budget vorhanden"]);
  await expect(root.locator("button")).toHaveCount(0);
  await expect(root.locator(".tasks-detail-tabs")).toHaveCount(0);
  await expect(root.locator("[id]")).toHaveCount(0);
  await expect(root.locator(".tasks-print-foot")).toHaveText(/\d{4}-\d{2}-\d{2}/);

  // On screen nothing changed; laid out for print, only the copy is.
  await expect(root).toBeHidden();
  await page.emulateMedia({ media: "print" });
  await expect(root).toBeVisible();
  await expect(page.locator("#task-detail")).toBeHidden();
  await page.emulateMedia({ media: "screen" });

  await page.evaluate(() => window.dispatchEvent(new Event("afterprint")));
  await expect(root).toHaveCount(0);
  await expect(page.locator("body.printing-task")).toHaveCount(0);

  await page.locator("#task-complete").click();
  await expect.poll(() => sent.some((r) => r.path.endsWith("/complete"))).toBe(true);
  const done = sent.find((r) => r.path.endsWith("/complete")).body;
  expect(done.formRevision).toBe(FORM.revision);
  expect(done.variables.kommentar).toBe("Budget vorhanden");
  expect(errors).toEqual([]);
});
