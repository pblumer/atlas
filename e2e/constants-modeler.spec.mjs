// End-to-end coverage for process constants in the Modeler (api/web/editor.js,
// ADR-0454): the values every instance starts with and nobody is asked
// for. The process panel's Implement tab edits them beside the start variables, the
// Variables panel offers them like any variable, and a model carrying them survives the
// Modeler's round trip. A constant's name is written in capitals, and the constants the
// installation holds are read under a namespace — application.NAME, server.NAME — which
// the Variables panel and completion offer and declare when picked
// (ADR-0456).
//
// The assertions read the exported XML, because that is what a deploy sends. Without the
// moddle types (api/web/atlas-moddle.json) the round trip fails: bpmn-js drops an
// extension element it cannot parse, and a Save would strip every constant.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1600, height: 900 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/constants-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

// openConstants mounts the editor on one of the harness's two models, switches to the
// Implement tab, shows the process itself (nothing selected) and opens its Constants group.
async function openConstants(page, kind) {
  await page.evaluate((k) => window.__mount(k), kind || "declared");
  await expect(page.locator("#p-body")).toBeVisible();
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__deselect());
  const group = page.locator('.pgroup[data-group="Constants"]');
  await expect(group).toHaveCount(1);
  if (await group.evaluate((g) => g.classList.contains("collapsed"))) {
    await group.locator(".pgroup-head").click();
  }
  await expect(page.locator("#cv-add")).toBeVisible();
}

const constantsXML = async (page) => {
  const xml = await page.evaluate(() => window.__xml());
  return /<atlas:constants>[\s\S]*?<\/atlas:constants>/.exec(xml)?.[0] || "";
};

test("the Implement tab lists the process's constants, without a required mark", async ({ page }) => {
  await openConstants(page, "declared");
  const rows = page.locator("#cv-list .sv-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).locator(".sv-name")).toHaveValue("DOMAIN_SUFFIX");
  await expect(rows.nth(0).locator(".sv-default")).toHaveValue("contoso.ch");
  await expect(rows.nth(1).locator(".sv-name")).toHaveValue("MAX_AGENTS");
  await expect(rows.nth(1).locator(".sv-type")).toHaveValue("number");
  await expect(rows.nth(1).locator(".sv-default")).toHaveValue("20");
  // Nobody supplies a constant, so there is nothing for "required" to mean.
  await expect(page.locator("#cv-list .sv-required")).toHaveCount(0);
  // The start variables are still their own list.
  await expect(page.locator("#sv-list .sv-row")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a model carrying constants survives the round trip untouched", async ({ page }) => {
  await openConstants(page, "declared");
  const xml = await constantsXML(page);
  expect(xml).toContain('<atlas:constant name="DOMAIN_SUFFIX" type="string" value="contoso.ch" />');
  expect(xml).toContain('<atlas:constant name="MAX_AGENTS" type="number" value="20" />');
  expect(page.__errors).toEqual([]);
});

test("a constant added in the panel reaches the deployed XML, beside the start variables", async ({ page }) => {
  await openConstants(page, "plain");
  expect(await constantsXML(page)).toBe("");

  await page.locator("#cv-add").click();
  const row = page.locator("#cv-list .sv-row").last();
  await row.locator(".sv-name").fill("TENANT_ID");
  await row.locator(".sv-default").fill("tenant-dev");
  await row.locator(".sv-default").blur();

  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="TENANT_ID" type="string" value="tenant-dev" />');
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toContain('<atlas:startVariable name="Name" type="string" required="true" />');

  // Removing the last constant removes the element rather than leaving an empty one.
  await row.locator(".sv-del").click();
  await expect.poll(() => constantsXML(page)).toBe("");
  expect(page.__errors).toEqual([]);
});

test("the Variables panel offers a constant like any variable", async ({ page }) => {
  await openConstants(page, "declared");
  await page.locator("#vars-toggle").click();
  const row = page.locator('#vars-list .var-row[data-var="DOMAIN_SUFFIX"]');
  await expect(row).toBeVisible();
  await expect(row.locator(".var-meta")).toContainText("Prozesskonstante");
  await expect(page.locator('#vars-list .var-row[data-var="MAX_AGENTS"] .vtag')).toHaveText("number");
  expect(page.__errors).toEqual([]);
});

// A constant can take its value from its application on each installation
// (ADR-0453): the source select writes
// source="application", and the value field becomes an optional fallback — written only
// when there is one.
test("a constant bound to its application writes its source, and a fallback only when given", async ({ page }) => {
  await openConstants(page, "plain");
  await page.locator("#cv-add").click();
  let row = page.locator("#cv-list .sv-row").last();
  await row.locator(".sv-name").fill("TENANT_ID");
  await row.locator(".sv-name").blur();
  await row.locator(".sv-source").selectOption("application");

  row = page.locator("#cv-list .sv-row").last();
  await expect(row.locator(".sv-default")).toHaveAttribute("placeholder", "Fallback (optional)");
  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="TENANT_ID" type="string" source="application" />');

  await row.locator(".sv-default").fill("tenant-dev");
  await row.locator(".sv-default").blur();
  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="TENANT_ID" type="string" value="tenant-dev" source="application" />');

  // The Variables panel says where the value comes from.
  await page.locator("#vars-toggle").click();
  await expect(page.locator('#vars-list .var-row[data-var="application.TENANT_ID"] .var-meta')).toContainText("Anwendungskonstante");
  expect(page.__errors).toEqual([]);
});

// A constant's name is written in capitals: the field applies it while it is typed, so
// what the model says is what the server accepts.
test("a constant's name is written in capitals as it is typed", async ({ page }) => {
  await openConstants(page, "plain");
  await page.locator("#cv-add").click();
  const name = page.locator("#cv-list .sv-row").last().locator(".sv-name");
  await name.pressSequentially("tenant-id größe");
  await expect(name).toHaveValue("TENANT_ID_GROESSE");
  await name.blur();
  await expect.poll(() => constantsXML(page)).toContain('name="TENANT_ID_GROESSE"');
  expect(page.__errors).toEqual([]);
});

// Typing inside a name keeps the caret after what was typed, though the rewrite changes
// the length of the text before it.
test("a letter typed inside a name lands where it was typed", async ({ page }) => {
  await openConstants(page, "plain");
  await page.locator("#cv-add").click();
  const name = page.locator("#cv-list .sv-row").last().locator(".sv-name");
  await name.pressSequentially("tenant");
  await name.press("Home");
  await name.pressSequentially("ä-");
  await name.pressSequentially("x");
  await expect(name).toHaveValue("AE_XTENANT");
  expect(page.__errors).toEqual([]);
});

// The caret is worked out from the value before the edit, not from the selection the
// browser reports during it. An input method inserts and places the caret on its own
// schedule; a caret taken from that report once put the next letter at the start of the
// name. Both ways it can go wrong are staged here: the selection misreported while the
// input event runs, and the caret moved after it.
test("a caret the browser misplaces during an insertion does not decide where the next letter goes", async ({ page }) => {
  await openConstants(page, "plain");
  await page.locator("#cv-add").click();
  const name = page.locator("#cv-list .sv-row").last().locator(".sv-name");
  await name.pressSequentially("tenant-id gr");
  const caret = await name.evaluate((el) => new Promise((resolve) => {
    el.dispatchEvent(new InputEvent("beforeinput", { bubbles: true, cancelable: true, inputType: "insertText", data: "ö" }));
    el.value += "ö";
    el.setSelectionRange(1, 1); // misreported while the event runs
    el.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText", data: "ö" }));
    el.setSelectionRange(2, 2); // moved once the insertion returns
    setTimeout(() => resolve(el.selectionStart), 20);
  }));
  await expect(name).toHaveValue("TENANT_ID_GROE");
  expect(caret).toBe("TENANT_ID_GROE".length);
  await name.pressSequentially("ße");
  await expect(name).toHaveValue("TENANT_ID_GROESSE");
  expect(page.__errors).toEqual([]);
});

// The installation's own constants are a third source beside the model and the
// application: the select writes source="server".
test("a constant read from the installation writes source server", async ({ page }) => {
  await openConstants(page, "plain");
  await page.locator("#cv-add").click();
  const row = page.locator("#cv-list .sv-row").last();
  await row.locator(".sv-name").fill("ENTRA_TENANT_ID");
  await row.locator(".sv-name").blur();
  await row.locator(".sv-source").selectOption("server");
  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="ENTRA_TENANT_ID" type="string" source="server" />');
  await page.locator("#vars-toggle").click();
  await expect(page.locator('#vars-list .var-row[data-var="server.ENTRA_TENANT_ID"] .var-meta')).toContainText("Serverkonstante");
  expect(page.__errors).toEqual([]);
});

// What the installation holds and the process does not read yet is listed under the
// Variables, with the facts each namespace carries; Declare adds the declaration.
test("the Variables panel offers undeclared namespace members and declares one", async ({ page }) => {
  await openConstants(page, "declared");
  await page.locator("#vars-toggle").click();
  const avail = page.locator("#vars-available");
  // The section starts closed; open it the way a reader would.
  await avail.locator(".vars-group-head").click();
  await expect(avail.locator('[data-avail="application.CLIENT_ID"]')).toBeVisible();
  await expect(avail.locator('[data-avail="server.ENTRA_TENANT_ID"]')).toBeVisible();
  await expect(avail.locator('[data-avail="server.environment"]')).toContainText("Serverinfo");
  await avail.locator('[data-declare="application.CLIENT_ID"]').click();
  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="CLIENT_ID" type="string" source="application" />');
  // Declared, it is a variable of the process now, and no longer offered.
  await expect(page.locator('#vars-list .var-row[data-var="application.CLIENT_ID"]').first()).toContainText("Anwendungskonstante");
  await expect(avail.locator('[data-avail="application.CLIENT_ID"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// The undeclared members are a group like Form / Process, closed until the author opens
// it: an installation with many constants would otherwise push the process's own
// variables out of view. Opening it is remembered, so it stays open across a re-render
// and a reopening, and so does closing it again.
test("the undeclared namespace members start closed and remember being opened", async ({ page }) => {
  await openConstants(page, "declared");
  await page.locator("#vars-toggle").click();
  const avail = page.locator("#vars-available");
  const head = avail.locator(".vars-group-head");
  const row = avail.locator('[data-avail="application.CLIENT_ID"]');
  await expect(head).toBeVisible();
  await expect(head).toHaveAttribute("aria-expanded", "false");
  await expect(row).toBeHidden();
  // Counted while closed, so what is on offer is not lost from view.
  const count = Number(await head.locator(".vg-count").innerText());
  expect(count).toBeGreaterThan(0);
  await expect(avail.locator(".var-row-available")).toHaveCount(count);

  await head.click();
  await expect(row).toBeVisible();
  await expect(head).toHaveAttribute("aria-expanded", "true");
  // Every keystroke in the filter re-renders the list; the section stays as the author
  // left it.
  await page.locator("#vars-filter").fill("CLIENT");
  await page.locator("#vars-filter").fill("");
  await expect(row).toBeVisible();
  // So does closing and reopening the panel.
  await page.locator("#vars-toggle").click();
  await page.locator("#vars-toggle").click();
  await expect(row).toBeVisible();

  await head.click();
  await expect(row).toBeHidden();
  await expect(head).toHaveAttribute("aria-expanded", "false");
  await page.locator("#vars-toggle").click();
  await page.locator("#vars-toggle").click();
  await expect(row).toBeHidden();
  expect(page.__errors).toEqual([]);
});

// A filter is a search: it opens the closed section while it finds something there, and
// leaves the author's choice for the unfiltered list alone. Closed by hand while
// filtering, the section stays closed until the filter is cleared.
test("filtering opens the undeclared namespace members without changing the stored choice", async ({ page }) => {
  await openConstants(page, "declared");
  await page.locator("#vars-toggle").click();
  const filter = page.locator("#vars-filter");
  const avail = page.locator("#vars-available");
  const head = avail.locator(".vars-group-head");
  const row = avail.locator('[data-avail="application.CLIENT_ID"]');
  await expect(row).toBeHidden();

  await filter.fill("CLIENT");
  await expect(row).toBeVisible();
  await expect(head).toHaveAttribute("aria-expanded", "true");
  await filter.fill("");
  await expect(row).toBeHidden();

  // Closed while filtering, it stays closed as the filter is refined.
  await filter.fill("CLI");
  await expect(row).toBeVisible();
  await head.click();
  await expect(row).toBeHidden();
  await filter.fill("CLIENT");
  await expect(row).toBeHidden();
  // Cleared, the list is back to the stored choice, and the next filter opens it again.
  await filter.fill("");
  await expect(row).toBeHidden();
  await filter.fill("CLIENT");
  await expect(row).toBeVisible();

  // Opened for the unfiltered list, it stays open after a filter closed it by hand.
  await filter.fill("");
  await head.click();
  await expect(row).toBeVisible();
  await filter.fill("CLIENT");
  await head.click();
  await expect(row).toBeHidden();
  await filter.fill("");
  await expect(row).toBeVisible();
  expect(page.__errors).toEqual([]);
});

// Completion offers a namespace member the process does not read yet, and picking it
// declares it — the model keeps saying what it reads without a trip to the process panel.
test("picking an undeclared namespace member in completion declares it", async ({ page }) => {
  await openConstants(page, "declared");
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    m.get("selection").select(m.get("elementRegistry").get("Activity_feel"));
  });
  await page.locator('[data-tab="implement"]').click();
  const expr = page.locator(".code-editor #f-expr");
  await expect(expr).toBeAttached();
  // The script's group may start collapsed; open it the way a reader would.
  const group = page.locator(".pgroup", { has: expr }).last();
  if (await group.count() && await group.evaluate((g) => g.classList.contains("collapsed"))) {
    await group.locator(".pgroup-head").first().click();
  }
  await expr.click();
  await expr.fill("");
  await expr.pressSequentially("server.ENT");
  const opt = page.locator(".feel-pop .feel-opt").filter({ hasText: "server.ENTRA_TENANT_ID" });
  await expect(opt).toBeVisible();
  await expect(opt).toContainText("declares it");
  await page.keyboard.press("Enter");
  await expect(expr).toHaveValue(/server\.ENTRA_TENANT_ID$/);
  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="ENTRA_TENANT_ID" type="string" source="server" />');
  expect(page.__errors).toEqual([]);
});
