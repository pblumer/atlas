// End-to-end coverage for the Instances overview's second arrangement: besides the one
// list of processes it always had, it can group the same rows under the application
// each process is filed in (ADR-0034, ADR-0128). The list stays the default and stays
// as it was; grouping is a choice the browser remembers, every application folds away
// under a heading that keeps its process, running, incident and finished counts, and a
// process filed nowhere — or under an application this person cannot read — sits in
// the trailing "not assigned" group, as it does in the Modeler's deployed list.
import { test, expect } from "@playwright/test";

const APP_A = "app-lizenz";
const APP_B = "app-m365";

// Two applications, one process outside any, and one filed under an application whose
// name the server withholds (a deleted one, or one this person cannot read).
const PROCESSES = [
  { key: 101, processId: "proc_lz_bestand", name: "Lizenzmanagement: Bestand abgleichen", version: 1, deployedAt: 1793000003, projectId: APP_A, applicationName: "Lizenzmanagement", active: true },
  { key: 102, processId: "proc_lz_buchen", name: "Lizenzmanagement: Buch fragen", version: 1, deployedAt: 1793000002, projectId: APP_A, applicationName: "Lizenzmanagement", active: true },
  { key: 201, processId: "proc_m365_team", name: "Microsoft Teams: Team", version: 1, deployedAt: 1793000001, projectId: APP_B, applicationName: "Microsoft 365", active: true },
  { key: 202, processId: "proc_m365_team", name: "Microsoft Teams: Team", version: 2, deployedAt: 1793000004, projectId: APP_B, applicationName: "Microsoft 365", active: true },
  { key: 301, processId: "atlas-auftrag-erfuellung", name: "Shop: Auftrag erfüllen", version: 1, deployedAt: 1793000000, projectId: "", applicationName: "", active: true },
  { key: 401, processId: "proc_hidden", name: "Verborgen", version: 1, deployedAt: 1792999999, projectId: "app-unreadable", applicationName: "", active: true },
];

const SUMMARY = [
  { processDefKey: 101, processId: "proc_lz_bestand", active: 0, completed: 1, latestCompletedAt: 1793000003000000000 },
  { processDefKey: 102, processId: "proc_lz_buchen", active: 2, completed: 2, latestCompletedAt: 1793000002000000000 },
  { processDefKey: 201, processId: "proc_m365_team", active: 1, completed: 4, latestCompletedAt: 1793000001000000000 },
  { processDefKey: 202, processId: "proc_m365_team", active: 0, completed: 5, latestCompletedAt: 1793000004000000000 },
  { processDefKey: 301, processId: "atlas-auftrag-erfuellung", active: 5, completed: 31, latestCompletedAt: 1793000000000000000 },
];

const INCIDENTS = { groups: [{ processDefKey: 102, count: 3 }], groupsTruncated: false };

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/processes") return route.fulfill({ json: PROCESSES });
    if (path === "/api/v1/instances/summary") return route.fulfill({ json: SUMMARY });
    if (path === "/api/v1/incidents/summary") return route.fulfill({ json: INCIDENTS });
    return route.fulfill({ json: [] });
  });
}

async function openInstances(page) {
  await page.evaluate(() => { location.hash = "#/operations"; });
  await expect(page.locator("#proc-card tbody tr").first()).toContainText(/\S/);
  await expect(page.locator("#proc-card")).not.toContainText("Loading…");
}

const groupButton = (page, mode) => page.locator(`#inst-group button[data-group="${mode}"]`);

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
});

test("the list stays the default: one table, one row per process", async ({ page }) => {
  await openInstances(page);
  await expect(groupButton(page, "process")).toHaveAttribute("aria-pressed", "true");
  await expect(groupButton(page, "application")).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("#proc-card table")).toHaveCount(1);
  await expect(page.locator("#proc-card .sec-head")).toHaveCount(0);
  // Five processes — the two versions of proc_m365_team are one row.
  await expect(page.locator("#proc-card tbody tr")).toHaveCount(5);
  expect(page.__errors).toEqual([]);
});

test("grouping by application puts each process under its application", async ({ page }) => {
  await openInstances(page);
  await groupButton(page, "application").click();
  await expect(groupButton(page, "application")).toHaveAttribute("aria-pressed", "true");

  const heads = page.locator("#proc-card .sec-head");
  // Alphabetical by application, the not-assigned group last.
  await expect(heads).toHaveCount(3);
  await expect(heads.nth(0)).toContainText("Lizenzmanagement");
  await expect(heads.nth(1)).toContainText("Microsoft 365");
  await expect(heads.nth(2)).toContainText("Nicht zugeordnet");

  // A heading sums what its processes hold, over every version.
  await expect(heads.nth(0)).toContainText("2 Prozesse");
  await expect(heads.nth(0)).toContainText("2 laufen");
  await expect(heads.nth(0)).toContainText("3 Incidents");
  await expect(heads.nth(0)).toContainText("3 abgeschlossen");
  await expect(heads.nth(1)).toContainText("1 Prozess");
  await expect(heads.nth(1)).toContainText("1 läuft");
  await expect(heads.nth(1)).toContainText("9 abgeschlossen");
  await expect(heads.nth(1)).not.toContainText("Incident");
  // The application's own page is one click away; the not-assigned group has none.
  await expect(heads.nth(0).locator(`a[href="#/modeler/p/${APP_A}"]`)).toHaveCount(1);
  await expect(heads.nth(2).locator("a")).toHaveCount(0);

  // The rows are the list's rows, under their group.
  await expect(page.locator(`#sec-ops-app-${APP_A} tbody tr`)).toHaveCount(2);
  await expect(page.locator(`#sec-ops-app-${APP_B} tbody tr`)).toHaveCount(1);
  const rest = page.locator("#sec-ops-app-unassigned tbody tr");
  await expect(rest).toHaveCount(2);
  await expect(rest.filter({ hasText: "Shop: Auftrag erfüllen" })).toHaveCount(1);
  await expect(rest.filter({ hasText: "Verborgen" })).toHaveCount(1);
  // Each group's table is a list like the flat one: sortable, with its filter row.
  await expect(page.locator(`#sec-ops-app-${APP_A} table`)).toHaveAttribute("data-dt-enhanced", "1");
  expect(page.__errors).toEqual([]);
});

test("the choice and each folded application survive a reload, and the list comes back", async ({ page }) => {
  await openInstances(page);
  await groupButton(page, "application").click();
  const toggle = page.locator(`.section-toggle[data-section="ops-app-${APP_B}"]`);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator(`#sec-ops-app-${APP_B}`)).toBeHidden();

  await page.reload();
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await openInstances(page);
  await expect(groupButton(page, "application")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(`#sec-ops-app-${APP_B}`)).toBeHidden();
  // A folded group still says what it holds.
  await expect(page.locator("#proc-card .sec-head").nth(1)).toContainText("1 Prozess");

  // Refresh keeps the arrangement.
  await page.click("#refresh");
  await expect(page.locator("#proc-card .sec-head")).toHaveCount(3);

  await groupButton(page, "process").click();
  await expect(page.locator("#proc-card .sec-head")).toHaveCount(0);
  await expect(page.locator("#proc-card table")).toHaveCount(1);
  await expect(page.locator("#proc-card tbody tr")).toHaveCount(5);
  await expect(page.locator("#proc-card table")).toHaveAttribute("data-dt-enhanced", "1");
  expect(page.__errors).toEqual([]);
});

test("a row action still works inside a group", async ({ page }) => {
  await openInstances(page);
  await groupButton(page, "application").click();
  // The overflow menu of a grouped row is bound like the list's: its terminate item
  // reaches the confirmation, with the counts of the row it was opened from.
  const row = page.locator(`#sec-ops-app-${APP_A} tbody tr`).filter({ hasText: "Buch fragen" });
  await row.locator(".dropdown-toggle").click();
  await row.locator('.dropdown-menu button[data-act="term"]').click();
  const dialog = page.locator(".confirm-modal");
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Terminate 2 running instances?");
  await dialog.locator("[data-cancel]").click();
  await expect(dialog).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
