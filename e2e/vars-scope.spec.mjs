// End-to-end coverage for what the Modeler's Variables panel lists (api/web/editor.js
// collectDiagramVariables, wireEditorVars).
//
// The panel answers "which variables exist here and who writes them". It read result
// fields, output mappings, data objects and form fields, and missed every variable that
// arrives through a construct instead:
//
//   - a multi-instance's output collection, which the body writes into the enclosing
//     scope, and its item and loopCounter, which each round holds for itself;
//   - an input mapping's target, a local of the element it is declared on;
//   - what a call activity brings back from the process it calls. With
//     propagateAllChildVariables on (the default) every instance-scope variable of the
//     child is merged into the caller when the child completes (engine resumeCaller),
//     and the caller's diagram names none of them;
//   - the start variables and constants, while the reader is drilled into a collapsed
//     subprocess: the canvas root is then the subprocess, not the process.
//
// With one element selected the panel narrows to that element: what it reads (the
// variables its expressions name), what it writes, and what it holds locally. A name it
// reads that nothing in the diagram writes is listed too, marked as such — that is how a
// message payload, or a typo, shows up before anything runs.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/vars-scope-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator("#vars-toggle").click();
  await expect(row(page, "kundennr")).toBeVisible();
});

const row = (page, name) => page.locator(`#vars-list .var-row[data-var="${name}"]`);
const names = (page, sel = "#vars-list") =>
  page.locator(`${sel} .var-row`).evaluateAll((els) => els.map((e) => e.dataset.var));
const group = (page, cat) => `#vars-list .vars-group[data-cat="${cat}"]`;

test("variables that arrive through a construct are listed with the rest", async ({ page }) => {
  for (const n of ["kundennr", "positionen", "MAX", "ok", "innenwert", "rueckgabe"]) {
    await expect(row(page, n), n).toBeVisible();
  }
  // The multi-instance body collects into bewertungen; each round holds pos and
  // loopCounter for itself.
  await expect(row(page, "bewertungen")).toBeVisible();
  await expect(row(page, "pos")).toBeVisible();
  await expect(row(page, "loopCounter")).toBeVisible();
  // An input mapping's target is a local of its element.
  await expect(row(page, "limite")).toBeVisible();
  await expect(row(page, "limite").locator(".var-meta")).toContainText("Prüfen");
  expect(page.__errors).toEqual([]);
});

test("a call activity that propagates its child's variables lists what the child writes", async ({ page }) => {
  // kindergebnis is written by the called process `kind`, read off its deployed model.
  await expect(row(page, "kindergebnis")).toBeVisible();
  await expect(row(page, "kindergebnis").locator('.var-origin[data-el="aufruf"]')).toBeVisible();
  await expect(row(page, "kindergebnis").locator(".var-meta")).toContainText("kind");
  // The child's own task-local never reaches its instance scope, so it never comes back.
  await expect(row(page, "nurlokal")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("drilled into a collapsed subprocess, the process's start variables and constants stay", async ({ page }) => {
  await expect(row(page, "innenwert")).toBeVisible();
  await page.evaluate(() => window.__drill("Sub_1"));
  // Whatever the panel draws next has to be drawn from the process, not the plane: an
  // edit on the subprocess's plane redraws it as any edit does.
  await page.locator("#vars-filter").fill("n");
  await page.locator("#vars-filter").fill("");
  await expect(row(page, "innenwert")).toBeVisible();
  await expect(row(page, "kundennr")).toBeVisible();
  await expect(row(page, "MAX")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("a selected task narrows the list to what it reads, writes and holds locally", async ({ page }) => {
  await page.evaluate(() => window.__select("pruefen"));
  await expect(page.locator("#vars-scope")).toBeVisible();
  await expect(page.locator("#vars-scope")).toContainText("Prüfen");

  expect(await names(page, group(page, "@reads"))).toEqual(["betrag", "kundennr", "MAX"]);
  expect(await names(page, group(page, "@writes"))).toEqual(["ok"]);
  expect(await names(page, group(page, "@local"))).toEqual(["limite"]);
  // A name inside a string literal is text, not a read.
  await expect(row(page, "geheim")).toHaveCount(0);
  // betrag is read and nothing in the diagram writes it.
  await expect(row(page, "betrag")).toHaveAttribute("data-unwritten", "1");
  await expect(row(page, "kundennr")).not.toHaveAttribute("data-unwritten", "1");
  // What the task has nothing to do with is gone.
  await expect(row(page, "bewertungen")).toHaveCount(0);
  // The namespaces' undeclared members belong to the whole process, not to a task.
  await expect(page.locator("#vars-available")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a multi-instance task reads its collection, holds its item and counter, writes the result", async ({ page }) => {
  await page.evaluate(() => window.__select("bewerten"));
  expect(await names(page, group(page, "@reads"))).toEqual(["positionen"]);
  expect(await names(page, group(page, "@writes"))).toEqual(["bewertungen"]);
  expect(await names(page, group(page, "@local"))).toEqual(["loopCounter", "pos"]);
  expect(page.__errors).toEqual([]);
});

test("a gateway reads what its branches' conditions name; a call activity writes what comes back", async ({ page }) => {
  await page.evaluate(() => window.__select("Gw_1"));
  expect(await names(page, group(page, "@reads"))).toEqual(["ok"]);

  await page.evaluate(() => window.__select("aufruf"));
  expect(await names(page, group(page, "@writes"))).toEqual(["kindergebnis"]);

  await page.evaluate(() => window.__select("aufruf_isoliert"));
  expect(await names(page, group(page, "@writes"))).toEqual(["rueckgabe"]);
  expect(page.__errors).toEqual([]);
});

test("Show all lifts the narrowing until the next selection, and no selection shows everything", async ({ page }) => {
  await page.evaluate(() => window.__select("pruefen"));
  await expect(row(page, "bewertungen")).toHaveCount(0);

  await page.locator("#vars-scope-all").click();
  await expect(row(page, "bewertungen")).toBeVisible();
  await expect(page.locator("#vars-scope")).toBeHidden();

  await page.evaluate(() => window.__select("bewerten"));
  await expect(page.locator("#vars-scope")).toContainText("Bewerten");
  await expect(row(page, "ok")).toHaveCount(0);

  await page.evaluate(() => window.__select(null));
  await expect(page.locator("#vars-scope")).toBeHidden();
  await expect(row(page, "ok")).toBeVisible();
  await expect(row(page, "bewertungen")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("declaring a namespace member while drilled into a subprocess declares it on the process", async ({ page }) => {
  await page.evaluate(() => window.__drill("Sub_1"));
  await page.locator("#vars-available .vars-group-head").click();
  await page.locator('#vars-list .var-declare[data-declare="server.environment"]').click();
  await expect(row(page, "server.environment")).toBeVisible();
  const declared = await page.evaluate(() => {
    const defs = window.__atlasModeler.getDefinitions();
    const proc = defs.rootElements.find((r) => r.id === "auftrag");
    const sub = proc.flowElements.find((f) => f.id === "Sub_1");
    const consts = (bo) => (((bo.extensionElements && bo.extensionElements.values) || [])
      .find((v) => v.$type === "atlas:Constants") || { constants: [] }).constants.map((c) => `${c.source || ""}.${c.name}`);
    return { process: consts(proc), subprocess: consts(sub) };
  });
  expect(declared.process).toContain("server.environment");
  expect(declared.subprocess).toEqual([]);
  expect(page.__errors).toEqual([]);
});
