// End-to-end coverage for the Tasks app's Start view grouping its processes by
// application, filtering them, and keeping favourites (api/web/app.js,
// viewStartProcess).
//
// A server with a few dozen start forms listed them as one alphabetical column, and
// finding "the one for travel" meant reading all of it. The list now sits under one
// heading per application the process was deployed under — named by the listing's
// applicationName, which the server leaves out where the viewer may not see the
// application — with "Other" last, and a filter box narrows it by name, id or
// application. A star puts a process into a Favourites group at the top.
//
// This drives the REAL app shell against a mocked /api/v1.
import { test, expect } from "@playwright/test";

const proc = (key, processId, name, projectId, applicationName) => ({
  key, processId, name, version: 1, startFormId: "f-" + processId, executable: true,
  ...(projectId ? { projectId } : {}), ...(applicationName ? { applicationName } : {}),
});

const PROCESSES = [
  proc(1, "proc_benutzer_aufnehmen", "Benutzerverwaltung: Benutzer aufnehmen", "app_bv", "Benutzerverwaltung"),
  proc(2, "proc_benutzer_offboarden", "Benutzerverwaltung: Benutzer offboarden", "app_bv", "Benutzerverwaltung"),
  proc(3, "proc_reisebuchung", "Reisebuchung", "app_rs", "Reisen"),
  proc(4, "proc_reise_antrag_public", "Reiseantrag (öffentlich)", "app_rs", "Reisen"),
  // Filed under an application this viewer may not see: the id is listed, the name not.
  proc(5, "proc_restructuring", "Reorganisation", "app_hidden"),
  // Filed under no application at all.
  proc(6, "proc_jira_test_01", "Jira Test Prozess"),
];

function installMock(page, processes = PROCESSES) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/processes") return route.fulfill({ json: processes });
    if (path.startsWith("/api/v1/forms/")) {
      return route.fulfill({ json: { id: "f", name: "f", schema: { type: "default", components: [] } } });
    }
    return route.fulfill({ json: [] });
  });
}

// listed reads the list top to bottom: a heading as "# Name (n)", a process by its title.
const listed = (page) => page.locator("#start-list > li").evaluateAll((els) => els.map((li) => {
  const head = li.querySelector(".start-group-head");
  if (head) return `# ${head.querySelector(".start-group-label").textContent} (${head.querySelector(".tasks-count").textContent})`;
  return li.querySelector(".tasks-item-title")?.textContent || li.textContent.trim();
}));

async function open(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/index.html?lang=en#/tasks/start");
  await expect(page.locator("#start-list .tasks-item").first()).toBeVisible({ timeout: 15000 });
  return errors;
}

test("groups the processes by application, by name, with Other last", async ({ page }) => {
  installMock(page);
  const errors = await open(page);
  expect(await listed(page)).toEqual([
    "# Benutzerverwaltung (2)",
    "Benutzerverwaltung: Benutzer aufnehmen",
    "Benutzerverwaltung: Benutzer offboarden",
    "# Reisen (2)",
    "Reiseantrag (öffentlich)",
    "Reisebuchung",
    // The hidden application's process and the unfiled one share the last group: the
    // viewer is not told a name the server kept from them.
    "# Other (2)",
    "Jira Test Prozess",
    "Reorganisation",
  ]);
  expect(errors).toEqual([]);
});

test("a closed group stays closed after a reload", async ({ page }) => {
  installMock(page);
  const errors = await open(page);
  const head = page.locator('.start-group-head[data-group="app:app_bv"]');
  await head.click();
  await expect(head).toHaveAttribute("aria-expanded", "false");
  expect(await listed(page)).toEqual([
    "# Benutzerverwaltung (2)",
    "# Reisen (2)", "Reiseantrag (öffentlich)", "Reisebuchung",
    "# Other (2)", "Jira Test Prozess", "Reorganisation",
  ]);

  await page.reload();
  await expect(page.locator("#start-list .tasks-item").first()).toBeVisible({ timeout: 15000 });
  await expect(page.locator('.start-group-head[data-group="app:app_bv"]')).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator('.tasks-item[data-key="1"]')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("the filter matches the application's name too, and opens the groups it finds in", async ({ page }) => {
  installMock(page);
  const errors = await open(page);
  await page.locator('.start-group-head[data-group="app:app_rs"]').click();
  await expect(page.locator('.tasks-item[data-key="3"]')).toHaveCount(0);

  // "reisen" names the application only — neither process's own name contains it.
  await page.locator("#start-q").fill("reisen");
  expect(await listed(page)).toEqual(["# Reisen (2)", "Reiseantrag (öffentlich)", "Reisebuchung"]);

  // Every word has to match, so a second one narrows.
  await page.locator("#start-q").fill("reisen public");
  expect(await listed(page)).toEqual(["# Reisen (1)", "Reiseantrag (öffentlich)"]);

  await page.locator("#start-q").fill("nichts dergleichen");
  await expect(page.locator("#start-list")).toContainText("No process matches");

  // Clearing the filter brings the closed group back closed: opening it was the
  // filter's doing, not the viewer's.
  await page.locator("#start-q").fill("");
  await expect(page.locator('.start-group-head[data-group="app:app_rs"]')).toHaveAttribute("aria-expanded", "false");
  expect(errors).toEqual([]);
});

test("with no application to show, the list has no headings", async ({ page }) => {
  installMock(page, [PROCESSES[4], PROCESSES[5]]);
  const errors = await open(page);
  expect(await listed(page)).toEqual(["Jira Test Prozess", "Reorganisation"]);
  expect(errors).toEqual([]);
});

test("selecting a process still opens its start form", async ({ page }) => {
  installMock(page);
  const errors = await open(page);
  await page.locator('.tasks-item[data-key="3"]').click();
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Reisebuchung");
  await expect(page.locator('.tasks-item[data-key="3"]')).toHaveClass(/selected/);
  await expect(page.locator("#start-go")).toBeVisible();
  expect(errors).toEqual([]);
});

test("a starred process heads the list under Favourites, and stays in its application", async ({ page }) => {
  installMock(page);
  const errors = await open(page);
  const star = page.locator('.tasks-item[data-key="3"] .start-fav');
  await star.click();
  // Starring is not opening: the detail pane still waits for a choice.
  await expect(page.locator("#start-go")).toHaveCount(0);
  expect(await listed(page)).toEqual([
    "# Favourites (1)", "Reisebuchung",
    "# Benutzerverwaltung (2)", "Benutzerverwaltung: Benutzer aufnehmen", "Benutzerverwaltung: Benutzer offboarden",
    "# Reisen (2)", "Reiseantrag (öffentlich)", "Reisebuchung",
    "# Other (2)", "Jira Test Prozess", "Reorganisation",
  ]);
  await expect(page.locator('.tasks-item[data-key="3"] .start-fav').first()).toHaveAttribute("aria-pressed", "true");

  // The mark follows the process id, so a new version keeps it.
  installMock(page, PROCESSES.map((p) => (p.key === 3 ? { ...p, key: 33, version: 2 } : p)));
  await page.reload();
  await expect(page.locator("#start-list .tasks-item").first()).toBeVisible({ timeout: 15000 });
  expect((await listed(page)).slice(0, 2)).toEqual(["# Favourites (1)", "Reisebuchung"]);

  // Clearing the star removes the group again.
  await page.locator('.tasks-item[data-key="33"] .start-fav').first().click();
  expect((await listed(page))[0]).toEqual("# Benutzerverwaltung (2)");
  expect(errors).toEqual([]);
});
