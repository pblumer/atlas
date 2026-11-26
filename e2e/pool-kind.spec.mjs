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
  expect(after).not.toMatch(/id="Pool_bank"[^>]*atlas:processId/);
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

// ---------- the message a flow carries, and the hints ----------

const selectFlow = async (page, id) => {
  await page.evaluate((el) => window.__select(el), id);
  await expect(page.locator("#f-name")).toBeVisible();
};
// The check runs after a pause; wait until the Modeler has asked.
const checked = (page, atLeast = 1) =>
  expect.poll(() => page.evaluate(() => window.__checks.length), { timeout: 10000 }).toBeGreaterThanOrEqual(atLeast);

test("a flow between two pools without content is given its message in the panel", async ({ page }) => {
  await checked(page);
  await selectFlow(page, "Flow_open");
  await expect(page.locator("#f-flowmsg")).toHaveValue("");
  // Offered: what the source sends and the target waits for, the name both sides know first.
  await expect.poll(() => page.locator("#f-flowmsg-list option").evaluateAll((os) => os.map((o) => `${o.value}: ${o.label}`))).toEqual([
    "Zahlung: gesendet und erwartet", "Mahnung: wird gesendet, aber nicht erwartet", "Ablage: wird erwartet, aber nicht gesendet"]);

  await page.locator("#f-flowmsg").fill("Zahlung");
  await page.locator("#f-flowmsg").blur();
  let out = await xml(page);
  const id = /<bpmn:message id="([^"]+)" name="Zahlung"/.exec(out);
  expect(id, "a message named Zahlung is declared").toBeTruthy();
  // The flow references it, and shows the name on the canvas, since nobody captioned it.
  expect(out).toMatch(new RegExp(`<bpmn:messageFlow id="Flow_open" name="Zahlung" sourceRef="Pool_bank" targetRef="Pool_ref" messageRef="${id[1]}"`));

  // A second name declares a second message; going back reuses the first.
  await selectFlow(page, "Flow_open");
  await page.locator("#f-flowmsg").fill("Mahnung");
  await page.locator("#f-flowmsg").blur();
  await selectFlow(page, "Flow_open");
  await page.locator("#f-flowmsg").fill("Zahlung");
  await page.locator("#f-flowmsg").blur();
  out = await xml(page);
  expect(out.match(/<bpmn:message id="[^"]+" name="Zahlung"/g)).toHaveLength(1);
  expect(out).toMatch(new RegExp(`<bpmn:messageFlow id="Flow_open" name="Zahlung"[^>]*messageRef="${id[1]}"`));

  // Emptying the field takes the name away again.
  await selectFlow(page, "Flow_open");
  await page.locator("#f-flowmsg").fill("");
  await page.locator("#f-flowmsg").blur();
  expect(await xml(page)).toMatch(/<bpmn:messageFlow id="Flow_open" sourceRef="Pool_bank" targetRef="Pool_ref"\s*\/>/);
  // Every change was checked again.
  await checked(page, 2);
  expect(await page.evaluate(() => window.__toasts)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("a flow docked on a message event carries that event's message and is not asked", async ({ page }) => {
  await selectFlow(page, "Flow_docked");
  await expect(page.locator("#f-flowmsg-docked")).toContainText("Freigabe");
  await expect(page.locator("#f-flowmsg-docked")).toContainText("Freigeben");
  await expect(page.locator("#f-flowmsg")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the hints stand on the element they are about, and all of them on the collaboration", async ({ page }) => {
  await checked(page);
  // On the canvas, the elements the check has something to say about are marked.
  await expect(page.locator('.djs-element.collab-hint[data-element-id="Flow_open"]')).toHaveCount(1);
  await expect(page.locator('.djs-element.collab-hint[data-element-id="Pool_ref"]')).toHaveCount(1);
  await expect(page.locator('.djs-element.collab-hint[data-element-id="Pool_order"]')).toHaveCount(0);

  // The flow's panel says its own hint, the pool's its own.
  await selectFlow(page, "Flow_open");
  await expect(page.locator(".collab-hints li")).toHaveCount(1);
  // Worded from the finding's parts, in the reader's language, with the rule under it.
  await expect(page.locator(".collab-hint-msg")).toHaveText("Der Flow von Bank zu Archiv nennt keine Nachricht und wird deshalb nicht geprüft.");
  await expect(page.locator(".collab-hint-rule")).toHaveText("collaboration.flow-unnamed");
  await select(page, "Pool_ref");
  await expect(page.locator(".collab-hints li")).toHaveCount(1);
  await expect(page.locator(".collab-hint-msg")).toHaveText("Archiv verweist auf den Prozess nowhere, der auf diesem Server nicht deployt ist.");
  // A rule the catalogue does not know yet is said in the server's own sentence.
  await select(page, "Pool_new");
  await expect(page.locator(".collab-hint-msg")).toHaveText("Something a newer server found.");
  // A pool nothing is said about shows no box at all.
  await select(page, "Pool_order");
  await expect(page.locator(".collab-hints")).toBeHidden();

  // The collaboration's own panel lists them all, and a hint leads to its element.
  await page.evaluate(() => window.__atlasModeler.get("selection").select(null));
  await expect(page.locator(".collab-hints li")).toHaveCount(3);
  await expect(page.locator(".collab-hints-head")).toHaveText("Hinweise zum Zusammenspiel (3)");
  await page.locator('.collab-hints li[data-el="Pool_ref"]').click();
  await expect(kind(page)).toHaveAttribute("data-kind", "reference");
  // Hints are not problems: the Problems bar does not list them.
  await expect(page.locator(".prob-row", { hasText: "collaboration." })).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// ---------- the process a reference pool shows ----------

// What the modeler holds about a pool: its bounds, and what hangs in its graphics.
const pool = (page, id) => page.evaluate((el) => {
  const r = window.__atlasModeler.get("elementRegistry");
  const s = r.get(el), g = r.getGraphics(el);
  const label = g.querySelector(":scope > .djs-visual text");
  return { x: s.x, y: s.y, width: s.width, height: s.height,
    shown: g.querySelectorAll(":scope > .ref-process").length,
    ownLabelHidden: !!label && label.style.display === "none" };
}, id);
const picture = (page) => page.locator('.djs-element[data-element-id="Pool_ship"] > .ref-process');

test("a reference pool shows the deployed process, as a picture that is not in the model", async ({ page }) => {
  await checked(page);
  await select(page, "Pool_ship");
  const before = await pool(page, "Pool_ship"), tail = await pool(page, "Pool_tail");
  expect(before).toMatchObject({ height: 60, shown: 0, ownLabelHidden: false });
  await expect(page.locator("#f-poolshow")).toBeEnabled();
  await expect(page.locator("#f-poolshow")).toHaveText("Prozess im Pool anzeigen");
  // The panel says which version would be shown, and that it is a picture.
  await expect(page.locator("#f-poolshow-note")).toContainText("Zeigt Versand in Version 2");

  await page.locator("#f-poolshow").click();
  await expect(picture(page)).toHaveCount(1);
  await expect(picture(page)).toHaveAttribute("data-process", "versand");
  await expect(picture(page)).toHaveAttribute("data-version", "2");
  // The deployed drawing: its four elements, and not the pool it was deployed under.
  for (const id of ["Start_v", "Task_v", "Throw_v", "End_v", "v1", "v2", "v3"]) {
    await expect(picture(page).locator(`[data-ref-element="${id}"]`)).toHaveCount(1);
  }
  await expect(picture(page).locator('[data-ref-element="Task_v"]')).toContainText("Packen");
  await expect(picture(page).locator('[data-ref-element="Pool_v"]')).toHaveCount(0);
  // Nothing of it answers as an element of this diagram: no lookup finds it, and it
  // cannot be clicked.
  await expect(page.locator('[data-element-id="Task_v"]')).toHaveCount(0);
  expect(await picture(page).evaluate((g) => getComputedStyle(g).pointerEvents)).toBe("none");
  expect(await page.evaluate(() => !!window.__atlasModeler.get("elementRegistry").get("Task_v"))).toBe(false);

  // The pool grew to hold it, and what stood below moved by as much.
  const after = await pool(page, "Pool_ship"), tailAfter = await pool(page, "Pool_tail");
  expect(after.height).toBeGreaterThan(150);
  expect(after.y).toBe(before.y);
  expect(tailAfter.y - tail.y).toBe(after.height - before.height);
  // The pool's name stands in a band at the left, as on a pool with content.
  expect(after.ownLabelHidden).toBe(true);
  await expect(picture(page).locator(".ref-name")).toHaveText("Versand");

  // Where a message arrives and where one leaves is said on the element.
  await expect(picture(page).locator('.ref-point-in[data-element="Start_v"]')).toHaveText("erwartet Auftrag");
  await expect(picture(page).locator('.ref-point-out[data-element="Throw_v"]')).toHaveText("sendet Versandt");
  // A flow at the pool's edge is led on to the element that carries its message; the
  // flow itself still ends at the pool.
  await expect(picture(page).locator('.ref-lead[data-flow="Flow_ship"][data-element="Start_v"]')).toHaveCount(1);
  await expect(picture(page).locator('.ref-lead[data-flow="Flow_back"][data-element="Throw_v"]')).toHaveCount(1);
  await expect(picture(page).locator(".ref-lead")).toHaveCount(2);

  // The file says that the pool is drawn with its process, where BPMN says how a thing
  // is drawn — on the shape, not on the participant — and holds nothing of the process.
  const out = await xml(page);
  expect(out).toMatch(/<bpmn:participant id="Pool_ship" name="Versand" atlas:processId="versand"\s*\/>/);
  expect(out).toMatch(/<bpmndi:BPMNShape id="Pool_ship_s" bpmnElement="Pool_ship" isHorizontal="true" atlas:showProcess="true">/);
  expect(out).not.toContain("Task_v");
  expect(out).not.toContain('<bpmn:process id="versand"');
  expect(out).toMatch(/<bpmn:messageFlow id="Flow_ship" sourceRef="Pool_bank" targetRef="Pool_ship"\s*\/>/);
  // The button now hides it.
  await select(page, "Pool_ship");
  await expect(page.locator("#f-poolshow")).toHaveText("Prozess ausblenden");
  await expect(page.locator("#f-poolshow")).toHaveAttribute("aria-pressed", "true");
  expect(await page.evaluate(() => window.__toasts)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("showing the process is one step to undo, and hiding it gives the room back", async ({ page }) => {
  await checked(page);
  await select(page, "Pool_ship");
  const before = await pool(page, "Pool_ship"), tail = await pool(page, "Pool_tail");
  await page.locator("#f-poolshow").click();
  await expect(picture(page)).toHaveCount(1);

  // One undo: the attribute, the size and the pool below are back together.
  await page.evaluate(() => window.__atlasModeler.get("commandStack").undo());
  await expect(picture(page)).toHaveCount(0);
  expect(await pool(page, "Pool_ship")).toEqual(before);
  expect((await pool(page, "Pool_tail")).y).toBe(tail.y);
  expect(await xml(page)).not.toContain("atlas:showProcess");

  // Redo, then hide it through the panel.
  await page.evaluate(() => window.__atlasModeler.get("commandStack").redo());
  await expect(picture(page)).toHaveCount(1);
  await select(page, "Pool_ship");
  await page.locator("#f-poolshow").click();
  await expect(picture(page)).toHaveCount(0);
  expect(await pool(page, "Pool_ship")).toEqual(before);
  expect((await pool(page, "Pool_tail")).y).toBe(tail.y);
  expect(await xml(page)).not.toContain("atlas:showProcess");
  // The deployed model was fetched once and drawn from what was kept.
  expect(await page.evaluate(() => window.__pictures)).toBe(1);
  expect(page.__errors).toEqual([]);
});

test("a drawing opens the way it was left, and the picture follows the pool", async ({ page }) => {
  await checked(page);
  await select(page, "Pool_ship");
  await page.locator("#f-poolshow").click();
  await expect(picture(page)).toHaveCount(1);
  const saved = await xml(page);

  // Opened again, the pool shows its process once the check has answered.
  const checks = await page.evaluate(() => window.__checks.length);
  await page.evaluate((x) => window.__atlasModeler.importXML(x), saved);
  await checked(page, checks + 1);
  await expect(picture(page)).toHaveCount(1);
  await expect(picture(page).locator('[data-ref-element="Throw_v"]')).toHaveCount(1);

  // A pool smaller than the picture shows it smaller instead of letting it out.
  const natural = await picture(page).locator(".ref-picture").evaluate((s) => +s.getAttribute("width"));
  await page.evaluate(() => {
    const m = window.__atlasModeler, el = m.get("elementRegistry").get("Pool_ship");
    m.get("modeling").resizeShape(el, { x: el.x, y: el.y, width: 300, height: el.height });
  });
  await expect.poll(() => picture(page).locator(".ref-picture").evaluate((s) => +s.getAttribute("width"))).toBeLessThan(natural);
  expect(await picture(page).locator(".ref-picture").evaluate((s) => +s.getAttribute("width"))).toBeLessThan(300 - 30 - 2 * 24 + 0.01);
  // Renamed, the band says the new name.
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    m.get("modeling").updateProperties(m.get("elementRegistry").get("Pool_ship"), { name: "Logistik" });
  });
  await expect(picture(page).locator(".ref-name")).toHaveText("Logistik");
  await expect(picture(page)).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a reference nothing deploys has nothing to show, and a pool that stands for nothing shows nothing", async ({ page }) => {
  await checked(page);
  // The archive names a process this server does not hold.
  await select(page, "Pool_ref");
  await expect(page.locator("#f-poolshow")).toBeDisabled();
  await expect(page.locator("#f-poolshow-note")).toContainText("nicht deployt");
  // Neither a pool modelled here nor an external party is offered it.
  await select(page, "Pool_order");
  await expect(page.locator("#f-poolshow")).toHaveCount(0);
  await select(page, "Pool_bank");
  await expect(page.locator("#f-poolshow")).toHaveCount(0);

  // Taking the reference away takes the picture and its attribute along.
  await select(page, "Pool_ship");
  await page.locator("#f-poolshow").click();
  await expect(picture(page)).toHaveCount(1);
  await select(page, "Pool_ship");
  await page.locator("#f-poolref").selectOption("");
  await expect(kind(page)).toHaveAttribute("data-kind", "external");
  await expect(picture(page)).toHaveCount(0);
  const out = await xml(page);
  expect(out).toMatch(/<bpmn:participant id="Pool_ship" name="Versand"\s*\/>/);
  expect(out).not.toContain("atlas:showProcess");
  expect(await pool(page, "Pool_ship")).toMatchObject({ shown: 0, ownLabelHidden: false });
  expect(page.__errors).toEqual([]);
});

// ---------- the wrench menu of a reference pool ----------

// bpmn-js's own replace menu offers a pool without a process "Expanded pool", which
// gives it an empty process. For a reference that is the wrong thing twice: the pool
// would be a reference and a process of its own, and what the modeller means by
// expanding it is to see the process it stands for. The menu therefore does what the
// panel's button does.
const openMenu = (page, id) => page.evaluate((el) => {
  const m = window.__atlasModeler, e = m.get("elementRegistry").get(el);
  m.get("selection").select(e);
  m.get("popupMenu").open(e, "bpmn-replace", { x: 400, y: 300 });
}, id);
// The entries of the menu itself; its header holds bpmn-js's own toggles.
const menuEntries = (page) => page.locator(".djs-popup-results .entry");

test("the wrench menu of a reference pool shows and hides its process", async ({ page }) => {
  await checked(page);
  const processes = (await xml(page)).match(/<bpmn:process /g).length;
  const before = await pool(page, "Pool_ship");

  await openMenu(page, "Pool_ship");
  await expect(menuEntries(page)).toHaveCount(1);
  await expect(menuEntries(page)).toHaveText("Aufgeklappter Pool (zeigt den Prozess)");
  await menuEntries(page).click();
  await expect(page.locator(".djs-popup")).toHaveCount(0);
  await expect(picture(page)).toHaveCount(1);
  // The same as the panel's button: no process of its own, the attribute on the shape.
  let out = await xml(page);
  expect(out.match(/<bpmn:process /g)).toHaveLength(processes);
  expect(out).toMatch(/<bpmn:participant id="Pool_ship" name="Versand" atlas:processId="versand"\s*\/>/);
  expect(out).toContain('bpmnElement="Pool_ship" isHorizontal="true" atlas:showProcess="true"');
  await expect(page.locator("#f-poolshow")).toHaveText("Prozess ausblenden");

  // Shown, the menu offers the way back.
  await openMenu(page, "Pool_ship");
  await expect(menuEntries(page)).toHaveCount(1);
  await expect(menuEntries(page)).toHaveText("Zugeklappter Pool (blendet den Prozess aus)");
  await menuEntries(page).click();
  await expect(picture(page)).toHaveCount(0);
  expect(await pool(page, "Pool_ship")).toEqual(before);
  out = await xml(page);
  expect(out).not.toContain("atlas:showProcess");
  expect(out.match(/<bpmn:process /g)).toHaveLength(processes);
  expect(await page.evaluate(() => window.__toasts)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("the wrench menu does not give a reference a process of its own, and leaves other pools alone", async ({ page }) => {
  await checked(page);
  const before = await xml(page);
  // A reference nothing deploys has nothing to show: the entry says so and does nothing.
  await openMenu(page, "Pool_ref");
  await expect(menuEntries(page)).toHaveCount(1);
  await expect(menuEntries(page)).toHaveText("Aufgeklappter Pool (Prozess nicht deployt)");
  await expect(menuEntries(page)).toHaveClass(/disabled/);
  await menuEntries(page).click({ force: true });
  expect(await xml(page)).toBe(before);
  await page.evaluate(() => window.__atlasModeler.get("popupMenu").close());

  // An external party keeps bpmn-js's own entry, which gives it a process to draw in.
  await openMenu(page, "Pool_bank");
  await expect(menuEntries(page)).toHaveText(["Aufgeklappter Pool"]);
  // So does a pool modelled here, which may be emptied.
  await page.evaluate(() => window.__atlasModeler.get("popupMenu").close());
  await openMenu(page, "Pool_order");
  await expect(menuEntries(page)).toHaveText(["Leerer Pool (entfernt den Inhalt)"]);
  expect(page.__errors).toEqual([]);
});
