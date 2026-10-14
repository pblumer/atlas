// End-to-end coverage for the setup block the Modeler shows beside a chosen Worker Type
// (api/web/workertypedocs.js, rendered by api/web/editor.js).
//
// The catalog says what a task states; it said nothing about what has to exist at the
// provider first — the token, the permission, the share — which is the part a new
// installation stumbles over. That is now written beside the fields, with a deep link
// into the handbook this server serves itself, and these tests hold the three things a
// refactor silently drops: that the block is there at all, that its steps are behind the
// fold while what a newcomer needs stays visible, and that the link points at this
// type's handbook card rather than the top of the page
// (ADR-0289).
//
// It reuses the placement harness: the same diagram already carries one task per
// interesting Worker Type, and mounting the real editor.js twice would only differ in
// the file name.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-placement-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// The setup section is a collapsible group like Operation or Failure handling. It opens
// by itself only for a type this server has no Worker configured for; the harness's mock
// answers with none, so a type that names a Worker starts open and the others start
// folded. openSetup makes a test independent of which case it is looking at.
async function openSetup(page) {
  const group = page.locator(".wt-group");
  if (await group.evaluate((el) => el.classList.contains("collapsed"))) {
    await group.locator(".io-group-head").click();
  }
}

test("the setup section is a group that folds away", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_mail"));
  const group = page.locator(".wt-group");
  await expect(group).toBeVisible();
  // Mail names a configured Worker and the harness has none, so it opens on its own:
  // the case the section was written for.
  await expect(group).not.toHaveClass(/collapsed/);
  await expect(page.locator(".wtdoc-needs")).toBeVisible();

  await group.locator(".io-group-head").click();
  await expect(group).toHaveClass(/collapsed/);
  await expect(page.locator(".wtdoc-needs")).toBeHidden();

  // The choice outlives the re-render a new selection causes — it is a statement about
  // the section, not about this one task.
  await page.evaluate(() => window.__select("Activity_sql"));
  await expect(page.locator(".wt-group")).toHaveClass(/collapsed/);
  expect(page.__errors).toEqual([]);
});

test("a type that configures nothing starts folded", async ({ page }) => {
  // User provisioning names no configured Worker: its setup is a one-time read, so it
  // does not take the panel on every selection.
  await page.evaluate(() => window.__select("Activity_login"));
  await page.locator(".pgroup-head", { hasText: "Worker type" }).click();
  await expect(page.locator(".wt-group")).toHaveClass(/collapsed/);
  expect(page.__errors).toEqual([]);
});

test("a Worker Type that needs an account says so, and links to its own handbook card", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_mail"));
  await openSetup(page);
  const doc = page.locator(".wtdoc");
  await expect(doc).toBeVisible();
  // The one line someone who has configured nothing has to read: whether this needs a
  // Worker record at all.
  await expect(doc.locator(".wtdoc-needs")).toContainText("configured mail Worker");
  const link = doc.locator(".wtdoc-link");
  await expect(link).toHaveAttribute("href", "/handbuch.html#runbook-mail");
  // The handbook is this server's own asset, so the link opens it rather than reaching
  // for a documentation site an air-gapped install cannot see.
  await expect(link).toHaveAttribute("target", "_blank");
  expect(page.__errors).toEqual([]);
});

test("the steps are behind the fold, and open on demand", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_sql"));
  await openSetup(page);
  const steps = page.locator(".wtdoc-steps li");
  // Folded: the panel is 270px wide, and an author who already has the worker is here
  // for the fields.
  await expect(steps.first()).toBeHidden();
  await page.locator(".wtdoc-more > summary").click();
  await expect(steps.first()).toBeVisible();
  // A database's whole configuration is its connection string, and that is what the
  // steps have to say — the placement notice above says only where it runs.
  await expect(page.locator(".wtdoc-steps")).toContainText("sqlserver://");
  expect(page.__errors).toEqual([]);
});

// The steps name menu paths in somebody else's product, so the panel says when they
// were last read against it — inside the fold, with the steps they qualify.
test("the steps say when they were last checked against the provider", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_mail"));
  await openSetup(page);
  const checked = page.locator(".wtdoc-checked");
  await expect(checked).toBeHidden();
  await page.locator(".wtdoc-more > summary").click();
  await expect(checked).toBeVisible();
  await expect(checked).toContainText(/Steps last checked \w+ 20\d\d/);
  // Machine-readable alongside the prose, and it says which source wins when they differ.
  await expect(checked.locator("time")).toHaveAttribute("datetime", /^20\d\d-\d\d$/);
  await expect(checked).toContainText("the provider is right");
  expect(page.__errors).toEqual([]);
});

test("a type that needs nothing says that, instead of showing nothing", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_login"));
  await page.locator(".pgroup-head", { hasText: "Worker type" }).click();
  await openSetup(page);
  // User provisioning acts on this server's own login store: no Worker record, no
  // credential. An empty panel there would read exactly like a type whose setup nobody
  // wrote down.
  await expect(page.locator(".wtdoc-needs")).toContainText("No Worker record");
  await expect(page.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-userprov");
  expect(page.__errors).toEqual([]);
});

test("switching the Worker Type switches the setup with it", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_rest"));
  await openSetup(page);
  await expect(page.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-rest");
  await page.evaluate(() => window.__select("Activity_mail"));
  await openSetup(page);
  await expect(page.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-mail");
  // One block per panel: a stale one left behind would be setup instructions for the
  // type the author just moved away from.
  await expect(page.locator(".wtdoc")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("the business rule task's temis binding carries its setup too", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_rule_temis"));
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await openSetup(page);
  await expect(page.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-temis");
  // The embedded binding configures no worker, so it gets no setup block: there is
  // nothing outside Atlas to do.
  await page.evaluate(() => window.__select("Activity_rule"));
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await expect(page.locator(".wtdoc")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
