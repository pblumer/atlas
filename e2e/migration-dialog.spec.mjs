// End-to-end coverage for the Operations migration dialogs (api/web/migrationdialog.js,
// ADR-0162 slice 4).
//
// The engine already refuses a migration that cannot hold, and the plan endpoint already
// answers "what would this do?" without writing anything. What these tests are about is
// whether an operator can *act* on that: see the refusal before committing rather than
// after, be stopped from submitting one that would fail, and be told plainly which
// instances a batch left behind.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/migration-dialog-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("the plan is read before anything is written, and names the refusal", async ({ page }) => {
  await page.locator("#mig-bad").click();
  const modal = page.locator(".mig-modal");
  await expect(modal).toBeVisible();

  // The plan came back refusing, and says so in the operator's terms — which element,
  // and what is wrong with moving it.
  await expect(modal.locator(".mig-verdict.bad")).toBeVisible();
  await expect(modal.locator(".mig-problems")).toContainText("review");
  await expect(modal.locator(".mig-problems")).toContainText("stranded");

  // Nothing has been written: the only call so far is the plan.
  const calls = await page.evaluate(() => window.__calls.map((c) => c.method + " " + c.url));
  expect(calls).toEqual(["POST /api/v1/instances/5002/migrate/plan"]);

  // And it cannot be submitted — a refusal an operator could have seen first is a round
  // trip they should not have to make.
  const go = modal.locator("[data-mig-go]");
  await expect(go).toBeDisabled();
  await modal.locator("#mig-reason").fill("trying anyway");
  await expect(go).toBeDisabled();

  expect(page.__errors, "page errors").toEqual([]);
});

test("a migratable instance shows what would move, and needs a reason before it goes", async ({ page }) => {
  await page.locator("#mig-ok").click();
  const modal = page.locator(".mig-modal");
  await expect(modal.locator(".mig-verdict.ok")).toBeVisible();

  // The mapping worth reading is the pair whose id changed; the rest is a count.
  await expect(modal.locator(".mig-moved")).toContainText("review");
  await expect(modal.locator(".mig-moved")).toContainText("review_v2");
  await expect(modal.locator(".mig-map")).toContainText("2 elements matched by id");

  // The reason gates the button, exactly as manual completion does (ADR-0159).
  const go = modal.locator("[data-mig-go]");
  await expect(go).toBeDisabled();
  await modal.locator("#mig-reason").fill("the tax rate was wrong");
  await expect(go).toBeEnabled();

  await go.click();
  await expect(modal).toHaveCount(0);

  const calls = await page.evaluate(() => window.__calls);
  expect(calls.map((c) => c.url)).toEqual([
    "/api/v1/instances/5001/migrate/plan",
    "/api/v1/instances/5001/migrate",
  ]);
  // The reason travels with the command; the target is the version that was picked.
  expect(calls[1].body.reason).toBe("the tax rate was wrong");
  expect(calls[1].body.targetProcessDefKey).toBe(103);
  expect(await page.evaluate(() => window.__result)).toBe(true);

  expect(page.__errors, "page errors").toEqual([]);
});

test("changing the target re-reads the plan for that target", async ({ page }) => {
  await page.locator("#mig-ok").click();
  const modal = page.locator(".mig-modal");
  await expect(modal.locator(".mig-verdict.ok")).toContainText("v3"); // newest by default

  await modal.locator("#mig-target").selectOption({ label: "v2" });
  await expect(modal.locator(".mig-verdict.ok")).toContainText("v2");

  // A plan is the answer to *that* target — showing a stale one beside a new selection
  // is the one way this dialog could mislead about live state.
  const planned = await page.evaluate(() =>
    window.__calls.filter((c) => c.url.endsWith("/migrate/plan")).map((c) => c.body.targetProcessDefKey));
  expect(planned).toEqual([103, 102]);

  expect(page.__errors, "page errors").toEqual([]);
});

test("the picker offers only other versions of the same process", async ({ page }) => {
  await page.locator("#mig-ok").click();
  const options = await page.locator("#mig-target option").allTextContents();
  // v3 (tagged) and v2 — not v1, which is the one the instance is already on, and not
  // the unrelated process's version.
  expect(options).toEqual(["v3 · hotfix", "v2"]);

  // The exported helper agrees, checked directly rather than through the DOM.
  const keys = await page.evaluate(() =>
    window.__otherVersions(window.__PROCESSES, "migrating", 101).map((p) => p.key));
  expect(keys).toEqual([103, 102]);

  expect(page.__errors, "page errors").toEqual([]);
});

test("a process with nowhere to migrate says so instead of opening an empty picker", async ({ page }) => {
  await page.locator("#mig-only").click();
  await expect(page.locator(".mig-modal")).toHaveCount(0);
  const t = await page.evaluate(() => window.__toast);
  expect(t.msg).toContain("Only one version");
  expect(await page.evaluate(() => window.__result)).toBe(false);
  // Nothing was written.
  expect(await page.evaluate(() => window.__calls.length)).toBe(0);

  expect(page.__errors, "page errors").toEqual([]);
});

test("a batch drains every page and lists the instances it left behind", async ({ page }) => {
  await page.locator("#mig-batch").click();
  const modal = page.locator(".mig-modal");
  await expect(modal).toBeVisible();

  // The source defaults to the version that actually holds instances, the target to the
  // newest — the shape of the job nearly every time this is opened.
  await expect(modal.locator("#migb-from")).toHaveValue("101");
  await expect(modal.locator("#migb-to")).toHaveValue("103");
  await expect(modal.locator("#migb-from")).toContainText("3 running");

  await modal.locator("#migb-reason").fill("draining v1");
  await modal.locator("[data-migb-go]").click();

  // The server pages; the caller repeats while `remaining` is true.
  await expect(page.locator(".mig-modal .modal-head h2")).toBeVisible();
  const calls = await page.evaluate(() => window.__calls.filter((c) => c.url.includes("migrate-instances")));
  expect(calls.length).toBe(2);
  expect(calls[0].body.reason).toBe("draining v1");

  // Both numbers, and the refusal as a work list: each named instance is still on the
  // old version and still needs a decision.
  await expect(page.locator(".mig-modal .modal-head h2")).toContainText("Migrated 3");
  await expect(page.locator(".mig-modal .modal-head h2")).toContainText("1 left behind");
  const refused = page.locator(".mig-refused");
  await expect(refused).toContainText("7007");
  await expect(refused).toContainText("ExclusiveGateway");
  await expect(refused.locator("a")).toHaveAttribute("href", "#/operations/i/7007");

  expect(page.__errors, "page errors").toEqual([]);
});

test("a batch refuses to migrate a version onto itself", async ({ page }) => {
  await page.locator("#mig-batch").click();
  const modal = page.locator(".mig-modal");
  await modal.locator("#migb-reason").fill("a reason");
  await expect(modal.locator("[data-migb-go]")).toBeEnabled();

  await modal.locator("#migb-to").selectOption("101"); // same as From
  await expect(modal.locator(".mig-err")).toBeVisible();
  await expect(modal.locator("[data-migb-go]")).toBeDisabled();

  expect(page.__errors, "page errors").toEqual([]);
});
