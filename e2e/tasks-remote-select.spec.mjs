// The directory field in the REAL Tasks app (api/web/app.js + remote-select.js,
// ADR-0473), against a mocked /api/v1: the field's search goes
// to the task's own lookup route, the chosen id is what Complete sends, and leaving the
// form — the inbox's "Back to the list", its cancel — completes nothing. Shown for the
// select the editor writes and for a directory field saved as a textfield.
import { test, expect } from "@playwright/test";

const ANNA = "11111111-0000-0000-0000-000000000001";
const TASK = {
  key: 701, processInstanceKey: 9101, elementInstanceKey: 9701, processDefKey: 1,
  processId: "Process_mvjoqvisfajk", elementId: "Activity_chooseUser", name: "User auswählen",
  formId: "entra-benutzerauswahl", priority: 50,
};
const formOf = (kind) => ({
  schema: {
    type: "default", id: "entra-benutzerauswahl",
    components: [
      { type: "select", id: "Field_abteilung", key: "abteilung", label: "Abteilung", validate: { required: true },
        values: [{ label: "IT", value: "IT" }, { label: "Finanzen", value: "Finance" }] },
      { type: kind, id: "Field_user", key: "user", label: "Person", validate: { required: true },
        atlasDataSource: { version: 1, workerType: "entra", worker: "ucc_entra", resource: "users",
          filters: [{ property: "department", field: "abteilung", required: true }],
          option: { details: ["department"] } } },
    ],
  },
});

function installMock(page, { kind, lookups, completes }) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: { items: [TASK], truncated: false, nextCursor: null } });
    if (path === "/api/v1/tasks/701/form-data/user/query") {
      const body = req.postDataJSON();
      lookups.push(body);
      return route.fulfill({ json: { items: [
        { value: ANNA, label: "Anna Müller", description: "anna.mueller@example.com", details: { department: "IT" } },
      ], nextCursor: null } });
    }
    if (path === "/api/v1/tasks/701/complete") {
      completes.push(req.postDataJSON());
      return route.fulfill({ json: { taskKey: 701 } });
    }
    if (path.includes("/api/v1/forms/")) return route.fulfill({ json: formOf(kind) });
    if (path.includes("/variables")) return route.fulfill({ json: {} });
    return route.fulfill({ json: [] });
  });
}

async function openTask(page) {
  await page.goto("/index.html?lang=de#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
  await page.locator('.tasks-item[data-key="701"]').click();
  await expect(page.locator("#task-form .atlas-rs-search")).toBeVisible({ timeout: 15000 });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

for (const kind of ["select", "textfield"]) {
  test.describe(`as a ${kind}`, () => {
    test("a task searches through its own lookup route and completes with the chosen id", async ({ page }) => {
      const lookups = [], completes = [];
      installMock(page, { kind, lookups, completes });
      await openTask(page);
      await page.locator("#task-form .fjs-form-field-select .fjs-input-group").click();
      await page.locator("#task-form .fjs-dropdownlist-item", { hasText: "IT" }).first().click();
      await page.locator("#task-form .atlas-rs-search").fill("Ann");
      await expect(page.locator("#task-form .atlas-rs-list li[role=option]")).toHaveCount(1);
      expect(lookups).toEqual([{ search: "Ann", filters: { department: "IT" } }]);
      await page.locator("#task-form .atlas-rs-list li[role=option]").first().click();
      await expect(page.locator("#task-form .atlas-rs-selected-label")).toHaveText("Anna Müller");
      // Choosing completed nothing.
      expect(completes).toEqual([]);
      await page.locator("#task-complete").click();
      await expect.poll(() => completes.length).toBe(1);
      expect(completes[0]).toEqual({ variables: { abteilung: "IT", user: ANNA } });
    });

    test("leaving the form completes nothing", async ({ page }) => {
      // "Back to the list" is the inbox's way out of a task, shown where list and task
      // share one column (ADR-0417).
      await page.setViewportSize({ width: 600, height: 900 });
      const lookups = [], completes = [];
      installMock(page, { kind, lookups, completes });
      await openTask(page);
      await page.locator("#task-form .fjs-form-field-select .fjs-input-group").click();
      await page.locator("#task-form .fjs-dropdownlist-item", { hasText: "IT" }).first().click();
      await page.locator("#task-form .atlas-rs-search").fill("Ann");
      await page.locator("#task-form .atlas-rs-list li[role=option]").first().click();
      await page.locator("#task-back").click();
      await expect(page.locator("#task-form")).toHaveCount(0);
      expect(completes).toEqual([]);
    });

    test("Complete without a choice is refused by the form and sends nothing", async ({ page }) => {
      const lookups = [], completes = [];
      installMock(page, { kind, lookups, completes });
      await openTask(page);
      await page.locator("#task-complete").click();
      await expect(page.locator("#toast")).toContainText("Person");
      expect(completes).toEqual([]);
    });
  });
}
