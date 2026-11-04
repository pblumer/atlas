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
  // The tests below read the English wording; German, the console's default, is the
  // last describe's, which reloads the harness without the parameter.
  await page.goto("/migration-dialog-harness.html?lang=en");
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

test("a refused rebinding offers the fork, and it needs somewhere to resume", async ({ page }) => {
  await page.locator("#mig-bad").click();
  const modal = page.locator(".mig-modal");
  const fork = modal.locator(".mig-fork");

  // The refusal comes with its next step rather than a dead end, and the section is not
  // dimmed: there is no rebinding to prefer over it here.
  await expect(fork).toBeVisible();
  await expect(fork).not.toHaveClass(/alt/);
  await expect(fork).toContainText("continue in a new instance");
  // What it costs is stated before anything is committed — in particular the work in
  // flight, which a fork cannot carry.
  await expect(fork).toContainText("3 variables");
  await expect(fork).toContainText("1 job");
  // Nothing is proposed, because the parked token's element is not in the target version.
  await expect(fork).toContainText("no element of the same id");

  const forkBtn = modal.locator("[data-mig-fork]");
  await expect(forkBtn).toBeVisible();
  await expect(forkBtn).toBeDisabled();
  await modal.locator("#mig-reason").fill("v2 replaced the task this token sits on");
  await expect(forkBtn, "a reason alone is not enough: the fork needs somewhere to resume").toBeDisabled();

  await fork.locator('input[type=checkbox][value="review_v2"]').check();
  await expect(forkBtn).toBeEnabled();
  await forkBtn.click();

  const calls = await page.evaluate(() => window.__calls.map((c) => c.method + " " + c.url));
  expect(calls).toEqual([
    "POST /api/v1/instances/5002/migrate/plan",
    "POST /api/v1/instances/5002/migrate/fork",
  ]);
  const sent = await page.evaluate(() => window.__calls[1].body);
  expect(sent.resume).toEqual(["review_v2"]);
  expect(sent.reason).toBe("v2 replaced the task this token sits on");

  // The successor is named, because that is where the work is now.
  const toast = await page.evaluate(() => window.__toast);
  expect(toast.kind).toBe("ok");
  expect(toast.msg).toContain("5003");
  expect(page.__errors, "page errors").toEqual([]);
});

test("a migratable instance shows the fork as the dimmed alternative", async ({ page }) => {
  await page.locator("#mig-ok").click();
  const modal = page.locator(".mig-modal");
  const fork = modal.locator(".mig-fork");

  // Offered, but visibly the second answer: rebinding keeps the instance's work, and an
  // operator who can do that should not be invited to end it.
  await expect(fork).toBeVisible();
  await expect(fork).toHaveClass(/alt/);
  // Its proposal is already ticked, so the one-click case stays one click.
  await expect(fork.locator('input[type=checkbox][value="review_v2"]')).toBeChecked();
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

  // The server pages; the caller repeats while `remaining` is true, continuing past
  // the cursor each page hands back rather than from the front, where a refused
  // instance would be selected again.
  await expect(page.locator("#migo-title")).toBeVisible();
  const calls = await page.evaluate(() => window.__calls.filter((c) => c.url.includes("migrate-instances")));
  expect(calls.length).toBe(2);
  expect(calls[0].body.reason).toBe("draining v1");
  expect(calls[0].url).not.toContain("after=");
  expect(calls[1].url).toContain("?after=7002");

  // Both numbers, and the refusal as a work list: each named instance is still on the
  // old version and still needs a decision.
  await expect(page.locator("#migo-title")).toContainText("Migrated 3");
  await expect(page.locator("#migo-title")).toContainText("1 left behind");
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

// startBatch opens the batch dialog on one of the harness's server behaviours and
// submits it with a reason.
const startBatch = async (page, button) => {
  await page.locator(button).click();
  const modal = page.locator(".mig-modal");
  await modal.locator("#migb-reason").fill("draining v1");
  await modal.locator("[data-migb-go]").click();
};

test("a batch that fails part-way says how much already moved, and re-reads", async ({ page }) => {
  await startBatch(page, "#mig-batch-fail");

  // Each instance is its own event, so the first page's two are on the new version
  // whatever happened to the second call — an error that did not say so would read as
  // "nothing happened".
  await expect.poll(() => page.evaluate(() => window.__toast && window.__toast.kind)).toBe("err");
  const t = await page.evaluate(() => window.__toast);
  expect(t.msg).toContain("2 instances were already migrated");
  expect(await page.evaluate(() => window.__done)).toBe(1);
  expect(await page.evaluate(() => window.__result)).toBe(false);
  expect(page.__errors, "page errors").toEqual([]);
});

test("a page that says more remain but not where to continue ends the walk", async ({ page }) => {
  await startBatch(page, "#mig-batch-nocursor");

  // From the front again it would meet the same instances; the loop stops instead of
  // spinning to its guard.
  await expect.poll(() => page.evaluate(() => window.__result)).toBe(true);
  const calls = await page.evaluate(() => window.__calls.filter((c) => c.url.includes("migrate-instances")));
  expect(calls.length).toBe(1);
  expect(page.__errors, "page errors").toEqual([]);
});

test("a flood of refusals is listed up to a bound and counted beyond it", async ({ page }) => {
  await startBatch(page, "#mig-batch-flood");

  await expect(page.locator("#migo-title")).toContainText("250 left behind");
  await expect(page.locator(".mig-refused li")).toHaveCount(200);
  await expect(page.locator(".mig-unlisted")).toContainText("50 more");
  await expect(page.locator(".mig-unlisted")).toContainText("v1");
  expect(page.__errors, "page errors").toEqual([]);
});

test.describe("in German, the console's default", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/migration-dialog-harness.html?lang=de");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  });

  test("the instance dialog and its plan speak German", async ({ page }) => {
    await page.locator("#mig-bad").click();
    const modal = page.locator(".mig-modal");
    await expect(modal.locator("#mig-title")).toContainText("Instanz migrieren");
    await expect(modal).toContainText("Verschieben nach");
    await expect(modal.locator(".mig-verdict.bad")).toContainText("kann nicht nach v");
    await expect(modal.locator("#mig-reason")).toHaveAttribute("placeholder", "Warum diese Instanz verschoben wird");
    await expect(modal.locator("[data-mig-go]")).toHaveText("Migrieren");
    await expect(modal.locator("[data-mig-cancel]")).toHaveText("Abbrechen");
    expect(page.__errors).toEqual([]);
  });

  test("the batch dialog and its outcome speak German", async ({ page }) => {
    await page.locator("#mig-batch").click();
    const modal = page.locator(".mig-modal");
    await expect(modal.locator("#migb-title")).toContainText("Laufende Instanzen migrieren");
    await expect(modal).toContainText("Von");
    await expect(modal).toContainText("Nach");
    await expect(modal.locator("#migb-from")).toContainText("3 laufen");
    await expect(modal.locator("#migb-from")).toContainText("keine laufen");
    await expect(modal.locator("[data-migb-go]")).toHaveText("Instanzen migrieren");

    await modal.locator("#migb-reason").fill("v1 auslaufen lassen");
    await modal.locator("[data-migb-go]").click();
    await expect(page.locator("#migo-title")).toContainText("3 migriert");
    await expect(page.locator("#migo-title")).toContainText("1 zurückgeblieben");
    await expect(page.locator("[data-migo-ok]")).toHaveText("Schliessen");
    expect(page.__errors).toEqual([]);
  });
});

// What the migration meets (ADR-0530): the dialogs set the
// comparison of the two versions beside where the instances stand, so the change an
// instance meets first — at the element it waits on — is read before it moves.
test.describe("what the target version changes", () => {
  test("one instance: the change at the task it waits on leads, the rest is folded", async ({ page }) => {
    await page.locator("#mig-ok").click();
    const impact = page.locator(".mig-modal #mig-impact");
    await expect(impact).toBeVisible();
    await expect(impact.locator("h4")).toHaveText("What the migration from v1 to v3 meets");
    await expect(impact.locator(".vc-impact-lead.warn")).toContainText("1 element instances stand on is changed or removed in v3");
    // Only the task the instance stands on is listed above the fold, with what changed.
    const lead = impact.locator(":scope > .vc-list > .vc-item");
    await expect(lead).toHaveCount(1);
    await expect(lead).toContainText("Review twice");
    await expect(lead).toContainText("review-form");
    // One instance's positions carry no count.
    await expect(lead.locator(".vc-tokens")).toHaveCount(0);
    // The whole comparison is there, folded.
    const all = impact.locator("details.vc-all");
    await expect(all).not.toHaveAttribute("open", "");
    await expect(all.locator("summary")).toContainText("Every change from v1 to v3");
    await all.locator("summary").click();
    await expect(all.locator(".vc-item")).toHaveCount(2);
    expect(page.__errors, "page errors").toEqual([]);
  });

  test("one instance on a removed element: the removal says it refuses without a mapping", async ({ page }) => {
    await page.locator("#mig-bad").click();
    const impact = page.locator(".mig-modal #mig-impact");
    const lead = impact.locator(":scope > .vc-list > .vc-item.vc-removed");
    await expect(lead).toHaveCount(1);
    await expect(lead.locator(".vc-note")).toContainText("refused unless the element is mapped");
    expect(page.__errors, "page errors").toEqual([]);
  });

  test("a batch: the changed and removed elements instances stand on, with their tokens", async ({ page }) => {
    await page.locator("#mig-batch").click();
    const impact = page.locator("#migb-impact");
    await expect(impact).toBeVisible();
    await expect(impact.locator(".vc-impact-lead.warn")).toContainText("2 elements instances stand on");
    const lead = impact.locator(":scope > .vc-list > .vc-item");
    await expect(lead).toHaveCount(2);
    await expect(lead.nth(0)).toContainText("review-v2");
    await expect(lead.nth(0).locator(".vc-tokens")).toHaveText("3 tokens");
    await expect(lead.nth(1)).toHaveClass(/vc-removed/);
    await expect(lead.nth(1).locator(".vc-tokens")).toHaveText("2 tokens");
    // done changed too, but no instance stands there: it is in the folded list only.
    await expect(lead.filter({ hasText: "Done" })).toHaveCount(0);
    expect(await page.evaluate(() => window.__reads)).toEqual([
      "/api/v1/processes/103/comparison?with=101",
      "/api/v1/processes/101/runtime/counts",
    ]);

    // Another pair is read afresh.
    await page.locator("#migb-to").selectOption("102");
    await expect.poll(() => page.evaluate(() => window.__reads.includes("/api/v1/processes/102/comparison?with=101"))).toBe(true);
    // The same version on both sides is not compared at all.
    await page.locator("#migb-from").selectOption("102");
    await expect(impact).toBeHidden();
    expect(page.__errors, "page errors").toEqual([]);
  });

  test("a batch whose counts cannot be read shows every change, and says why", async ({ page }) => {
    await page.locator("#mig-batch-nocounts").click();
    const impact = page.locator("#migb-impact");
    await expect(impact.locator(".vc-impact-lead")).toContainText("could not be read");
    await expect(impact.locator("details.vc-all")).toHaveAttribute("open", "");
    await expect(impact.locator("details.vc-all .vc-item")).toHaveCount(3);
    expect(page.__errors, "page errors").toEqual([]);
  });

  test("in German", async ({ page }) => {
    await page.goto("/migration-dialog-harness.html?lang=de");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.locator("#mig-batch").click();
    const impact = page.locator("#migb-impact");
    await expect(impact.locator("h4")).toHaveText("Was die Migration von v1 nach v3 antrifft");
    await expect(impact.locator(".vc-tokens").first()).toHaveText("3 Tokens");
    expect(page.__errors, "page errors").toEqual([]);
  });
});
