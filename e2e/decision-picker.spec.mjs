// End-to-end coverage for how the business rule task's decision picker is cut up
// (api/web/editor.js, ADR-0050). Driven through the real vendored bpmn-js and the
// real properties panel.
//
// The list is grouped by decision file, this application's files first, and inside a
// file the published interfaces come before the decisions they are made of. What it
// replaces was grouped by nothing but where an entry happened to come from, which put
// a decision service — the one thing a task is meant to call — under "other", below
// every decision inside it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/decision-picker-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openPicker selects the business rule task and expands the group its decision
// picker lives in, then waits for the catalog fetch to have filled it.
async function openPicker(page) {
  await page.evaluate(() => window.__select("Activity_rule"));
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await expect(page.locator("#f-decision-pick optgroup").first()).toBeAttached();
}

// groups returns the picker as [{ label, options }], the structure under test.
async function groups(page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("#f-decision-pick optgroup"), (g) => ({
      label: g.label,
      options: Array.from(g.children, (o) => o.textContent),
    })));
}

test("the picker is grouped by decision file, this application's files first", async ({ page }) => {
  await openPicker(page);
  const got = await groups(page);

  expect(got.map((g) => g.label)).toEqual([
    "Kreditfreigabe",
    "Tarif 2027",
    "Alter — other application",
    "freigabe — deployed only",
  ]);
  expect(page.__errors).toEqual([]);
});

test("a file offers its decision service first and its workings last", async ({ page }) => {
  await openPicker(page);
  const [ kredit ] = await groups(page);

  // The order is the recommendation, and it holds whatever order the catalog arrived
  // in: the published interface, then the decisions that are somebody's to call, then
  // the ones a service only evaluates on the way to its answer.
  expect(kredit.options).toEqual([
    "Kreditfreigabe — decision service",
    "Bonität",
    "Kreditentscheid — published by Kreditfreigabe",
    "Tragbarkeit — internal to Kreditfreigabe — bypasses it",
  ]);

  // The two halves of "made of" do not read the same, because they are not the same
  // offer. Kreditentscheid is what the service answers with, so calling it directly
  // gets the same value by a longer route. Tragbarkeit is a working: a task bound to
  // it has reached past the interface into an arrangement the service exists to be
  // free to change, and nothing later will ever mention that — it runs, and it
  // answers correctly.
  expect(kredit.options[3]).toContain("bypasses it");
  expect(kredit.options[2]).not.toContain("bypasses");

  // Bonität is the service's *input* decision — the caller's boundary, outside the
  // service — so it carries no marker at all.
  expect(kredit.options[1]).toBe("Bonität");
  expect(page.__errors).toEqual([]);
});

// A decision an author can pick but the engine cannot run yet.
//
// The picker offers what is in the *model*, which is a layer above what is deployed:
// writing a decision into the model makes it callable by name, deploying it makes it
// runnable. Between the two the task saves cleanly and looks right, and the refusal
// comes at Publish — in a message about a decision picked minutes ago, from a preflight
// the author was not thinking about.
test("a decision that is in the model but not deployed says so", async ({ page }) => {
  await openPicker(page);
  const got = await groups(page);
  const tarif = got.find((g) => g.label === "Tarif 2027");

  expect(tarif.options).toEqual([ "praemie — not deployed" ]);

  // Only that one. Every other entry here is runnable, and a marker on all of them is
  // a marker nobody reads.
  const marked = got.flatMap((g) => g.options).filter((o) => o.includes("not deployed"));
  expect(marked).toEqual([ "praemie — not deployed" ]);

  // The two notes stack rather than replace each other: a decision can belong to a
  // service *and* not be deployed, and an author needs both facts.
  const kredit = got.find((g) => g.label === "Kreditfreigabe");
  expect(kredit.options).toContain("Kreditentscheid — published by Kreditfreigabe");
  expect(page.__errors).toEqual([]);
});

test("picking the decision service fills the task with it", async ({ page }) => {
  await openPicker(page);
  await page.locator("#f-decision-pick").selectOption("Kreditfreigabe");

  const xml = await page.evaluate(() => window.__xml());
  const task = /<bpmn:businessRuleTask id="Activity_rule"[\s\S]*?<\/bpmn:businessRuleTask>/.exec(xml)[0];
  expect(task).toMatch(/<zeebe:calledDecision[^>]*decisionId="Kreditfreigabe"/);
  // Its declared inputs are auto-filled as mappings, the same as for a decision.
  expect(task).toMatch(/<zeebe:input[^>]*target="betrag"/);
  expect(task).toMatch(/<zeebe:input[^>]*target="bonitaet"/);
  expect(page.__errors).toEqual([]);
});

// Where the task's input mapping and the decision's own inputs disagree.
//
// It is the same drift the decision editor reports one level down, at the place it
// does the most damage. Inside a model, a name nothing provides does not deploy — the
// engine answers `unknown variable` and the deploy gate refuses it. Here it does
// deploy: the mapping is FEEL over the instance's variables, and nothing can know that
// "betrag" was meant where "betraege" was typed. An input the decision declares and the
// task never maps arrives null, every rule that tests it falls through, and the process
// carries on with whatever the table's last row says — a wrong answer rather than a
// failure, and nothing downstream tells the two apart.
test("the task says where its input mapping and the decision's inputs disagree", async ({ page }) => {
  await openPicker(page);
  const note = page.locator("#dmn-inputs-drift");
  // The rows, and the note under them, live in their own group. Opened only when it is
  // shut: a panel re-render keeps whichever groups were open, so an unconditional click
  // after one closes the group it was meant to open.
  const openInputs = async () => {
    const head = page.locator(".pgroup-head", { hasText: "Decision inputs" });
    const group = page.locator(".pgroup", { has: head });
    if (((await group.getAttribute("class")) || "").includes("collapsed")) await head.click();
  };

  await openInputs();
  // Nothing to say before a decision is picked: there is nothing to compare against.
  await expect(note).toBeHidden();

  await page.locator("#f-decision-pick").selectOption("Kreditfreigabe");
  // Picking fills the rows from the decision's own inputs and re-renders the panel
  // around the filled-in task, which closes the groups again.
  const firstTarget = page.locator("#dmn-inputs .dmn-input-row .dmn-in-target").first();
  await expect(firstTarget).toHaveValue("betrag");
  await openInputs();
  await expect(firstTarget).toBeVisible();
  // They agree, so the note stays away. A note that is always there is a note nobody
  // reads.
  await expect(note).toBeHidden();

  // Mistype one target, which is how this happens in life — a rename on one side, a
  // hand-typed name on the other.
  await firstTarget.fill("betraege");

  // Both directions, because they are different mistakes: one input arrives empty, and
  // one row feeds a name the decision never reads.
  await expect(note).toBeVisible();
  await expect(note).toContainText("The decision reads");
  await expect(note).toContainText("betrag");
  await expect(note).toContainText("no row feeds it");
  await expect(note).toContainText("betraege");
  await expect(note).toContainText("is ignored");

  // It follows the typing rather than the save: a row corrected and not yet committed
  // is exactly when it is useful.
  await firstTarget.fill("betrag");
  await expect(note).toBeHidden();

  // And an input left unmapped is reported on its own, without anything extra being
  // fed: the two halves are independent.
  await firstTarget.fill("");
  await expect(note).toBeVisible();
  await expect(note).toContainText("no row feeds it");
  await expect(note).not.toContainText("is ignored");
  expect(page.__errors).toEqual([]);
});
