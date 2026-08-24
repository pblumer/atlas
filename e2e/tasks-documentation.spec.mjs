// End-to-end coverage for a user task's documentation in the Tasks app (api/web/app.js,
// ADR-0025 amended). The element's <bpmn:documentation> is the modeler's instruction for
// whoever picks the task up, so the detail pane leads with it — above the metadata rows
// and the form. The API side is covered by the Go suite; this drives the REAL app shell
// against a mocked /api/v1 to verify the UI wiring: the block appears for a documented
// task, keeps the author's paragraph breaks, and is absent (not stale) for one that
// carries none.
import { test, expect } from "@playwright/test";

const DOC = "Vergleiche die Angaben mit den Anlagen.\n\nFehlen Unterlagen, lehne ab und informiere den Antragsteller.";

const TASKS = [
  {
    key: 101, processInstanceKey: 9001, elementInstanceKey: 9101, processDefKey: 1,
    processId: "freigabe", elementId: "review", name: "Antrag prüfen",
    documentation: DOC, priority: 50,
  },
  {
    key: 102, processInstanceKey: 9001, elementInstanceKey: 9102, processDefKey: 1,
    processId: "freigabe", elementId: "sign", name: "Unterschreiben", priority: 50,
  },
];

// installMock answers /auth/me as single-user so the app never gates, serves the two
// tasks above, and returns a benign [] for every other call the shell makes on boot.
function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: TASKS });
    return route.fulfill({ json: [] });
  });
}

// bootTasks loads the real shell straight into the inbox and waits for the list.
async function bootTasks(page) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
}

const select = async (page, key) => page.locator(`.tasks-item[data-key="${key}"]`).click();

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
});

test("a documented task leads its detail with the modeler's instruction", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  const doc = page.locator(".tasks-doc");
  await expect(doc).toBeVisible();
  await expect(doc.locator("h2")).toHaveText("What to do");
  await expect(doc.locator("p")).toContainText("Vergleiche die Angaben mit den Anlagen.");
  await expect(doc.locator("p")).toContainText("Fehlen Unterlagen, lehne ab");
  expect(page.__errors).toEqual([]);
});

test("the instruction is read before the metadata and the form", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  // The block sits between the title and the field rows — the order the assignee reads.
  const order = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll(".tasks-detail-head, .tasks-doc, .tasks-fields")];
    return nodes.map((n) => n.className.split(" ")[0]);
  });
  expect(order).toEqual(["tasks-detail-head", "tasks-doc", "tasks-fields"]);

  // The author's paragraph break survives — prose, not a squashed single line.
  const white = await page.locator(".tasks-doc p").evaluate((el) => getComputedStyle(el).whiteSpace);
  expect(white).toBe("pre-wrap");
  expect(page.__errors).toEqual([]);
});

test("an undocumented task shows no block, not the previous task's text", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);
  await expect(page.locator(".tasks-doc")).toBeVisible();

  await select(page, 102);
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Unterschreiben");
  await expect(page.locator(".tasks-doc")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
