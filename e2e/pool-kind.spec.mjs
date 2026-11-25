// End-to-end coverage for what the Modeler says a pool is, and for making a pool a
// reference to a deployed process (ADR-0559, amendment of 2026-11-25). Driven through
// the real vendored bpmn-js: the kind is read from the pool's content, never chosen
// when the file is created; a pool without content offers the deployed processes; the
// choice is written as atlas:processId on the participant and survives the export; and
// a pool that had only the empty process bpmn-js gave it loses that process.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/pool-kind-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#p-body")).toBeVisible();
});

const select = async (page, id) => {
  await page.evaluate((el) => window.__select(el), id);
  await expect(page.locator("#f-poolkind")).toBeVisible();
};
const kind = (page) => page.locator("#f-poolkind");
const xml = (page) => page.evaluate(() => window.__xml());

test("the panel says what each pool is, from its content", async ({ page }) => {
  await select(page, "Pool_order");
  await expect(kind(page)).toHaveAttribute("data-kind", "modelled");
  await expect(kind(page)).toContainText("Hier modelliert");
  // A pool modelled here is not offered a reference.
  await expect(page.locator("#f-poolref")).toHaveCount(0);

  await select(page, "Pool_bank");
  await expect(kind(page)).toHaveAttribute("data-kind", "external");
  await expect(kind(page)).toContainText("Externe Partei");
  // One entry per process, under its newest version's name; a process without a name
  // by its id.
  await expect(page.locator("#f-poolref option")).toHaveText(["— keiner —", "rechnung", "Versand (versand)"]);
  await expect(page.locator("#f-addproc")).toHaveCount(1);

  // The pool bpmn-js gave an empty process is an external party too.
  await select(page, "Pool_new");
  await expect(kind(page)).toHaveAttribute("data-kind", "external");

  // A reference to a process this server does not hold says so and stays selected.
  await select(page, "Pool_ref");
  await expect(kind(page)).toHaveAttribute("data-kind", "reference");
  await expect(page.locator("#f-poolref")).toHaveValue("nowhere");
  await expect(page.locator("#f-poolref option:checked")).toHaveText("nowhere (nicht deployt)");
  await expect(page.locator("#f-poolref-note")).toContainText("ist auf diesem Server nicht deployt");
  // A reference is not also offered a process of its own.
  await expect(page.locator("#f-addproc")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a black-box pool becomes a reference, and an external party again", async ({ page }) => {
  await select(page, "Pool_bank");
  await page.locator("#f-poolref").selectOption("versand");
  await expect(kind(page)).toHaveAttribute("data-kind", "reference");
  await expect(page.locator("#f-poolref")).toHaveValue("versand");
  // The pool had a name, which stays; the choice is on the participant.
  expect(await xml(page)).toMatch(/<bpmn:participant id="Pool_bank" name="Bank" atlas:processId="versand"\s*\/>/);

  await page.locator("#f-poolref").selectOption("");
  await expect(kind(page)).toHaveAttribute("data-kind", "external");
  const after = await xml(page);
  expect(after).toMatch(/<bpmn:participant id="Pool_bank" name="Bank"\s*\/>/);
  expect(after).not.toContain('atlas:processId="versand"');
  expect(page.__errors).toEqual([]);
});

test("a pool with an empty process loses it when it becomes a reference", async ({ page }) => {
  expect(await xml(page)).toContain('<bpmn:process id="leer"');
  await select(page, "Pool_new");
  await page.locator("#f-poolref").selectOption("versand");
  await expect(kind(page)).toHaveAttribute("data-kind", "reference");
  const after = await xml(page);
  // The reference has no process of its own in the file, and the unnamed pool took the
  // process's name as a proposal.
  expect(after).not.toContain('<bpmn:process id="leer"');
  expect(after).not.toContain('processRef="leer"');
  expect(after).toMatch(/<bpmn:participant id="[^"]+" name="Versand" atlas:processId="versand"\s*\/>/);
  // The process modelled here is untouched.
  expect(after).toContain('<bpmn:participant id="Pool_order" name="Bestellung" processRef="bestellung"');
  expect(await page.evaluate(() => window.__toasts)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

// The application's artifact list names a draft by what it holds: the server counts the
// participants each time the draft is listed, and two or more is a collaboration.
test("an application lists a draft with several pools as a collaboration", async ({ page }) => {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: "app-1", name: "Bestellwesen", visibility: "private" }] });
    if (path === "/api/v1/drafts") {
      return route.fulfill({ json: [
        { processId: "bestellung", name: "Bestellung und Versand", projectId: "app-1", savedAt: 1792000000, participants: 3 },
        { processId: "allein", name: "Allein", projectId: "app-1", savedAt: 1791000000 },
        { processId: "im-pool", name: "Im Pool", projectId: "app-1", savedAt: 1790000000, participants: 1 },
      ] });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/modeler/p/app-1");
  const row = (name) => page.locator("tr", { has: page.locator("a", { hasText: name }) }).first();
  await expect(row("Bestellung und Versand")).toBeVisible({ timeout: 15000 });
  await expect(row("Bestellung und Versand").locator(".artifact-type")).toHaveText("BPMNCollaboration");
  await expect(row("Allein").locator(".artifact-type")).toHaveText("BPMNProzess");
  // One pool around a process is still a process.
  await expect(row("Im Pool").locator(".artifact-type")).toHaveText("BPMNProzess");
  expect(page.__errors).toEqual([]);
});
