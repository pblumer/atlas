// End-to-end coverage for the element Documentation property in the Modeler panel
// (api/web/editor.js, ADR-0025). Driven through the real vendored bpmn-js: every
// element — task, gateway, event, sequence flow, data object — plus the process, the
// collaboration and each pool offers one Documentation field, shows what the imported
// model carries, and writes it back as a <bpmn:documentation> child of that element.
// Atlas never interprets it, so the exported XML is the whole contract: the prose must
// land on the documented element and survive the round-trip a deploy makes.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/documentation-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

// mount boots the editor on one of the harness's two models and waits for the panel.
async function mount(page, kind) {
  await page.evaluate((k) => window.__mount(k), kind || "process");
  await expect(page.locator("#p-body")).toBeVisible();
}

// open selects an element; General (which holds the Documentation field) is the one
// property group that starts expanded, so the field is on screen straight away.
async function open(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await expect(page.locator("#f-doc")).toBeVisible();
}

// expand opens a collapsed property group by its title.
async function expand(page, title) {
  await page.locator(".pgroup-head", { hasText: title }).click();
}

// elementXML returns the exported XML of one element, so an assertion sees the
// documentation in its element rather than anywhere in the document.
async function elementXML(page, tag, id) {
  const xml = await page.evaluate(() => window.__xml());
  const m = new RegExp(`<bpmn:${tag} id="${id}"[\\s\\S]*?</bpmn:${tag}>`).exec(xml);
  return m ? m[0] : "";
}

// setDoc types into a documentation field and commits it (the panel saves on change).
async function setDoc(page, id, text) {
  await page.locator(`#${id}`).fill(text);
  await page.locator(`#${id}`).blur();
}

test("an element shows the documentation its model carries", async ({ page }) => {
  await mount(page);
  await open(page, "Start_1");
  await expect(page.locator("#f-doc")).toHaveValue("Startet, sobald der Antrag im Portal eingeht.");

  // …and an undocumented element shows an empty field, not the previous element's.
  await open(page, "Activity_user");
  await expect(page.locator("#f-doc")).toHaveValue("");
  expect(page.__errors).toEqual([]);
});

test("every element kind can be documented, and the prose lands on that element", async ({ page }) => {
  await mount(page);
  const cases = [
    ["Activity_user", "userTask", "Vier-Augen-Prinzip: eine zweite Person prüft."],
    ["Gateway_1", "exclusiveGateway", "Genehmigt, wenn alle Unterlagen vorliegen."],
    ["Activity_pay", "serviceTask", "Zahlt über den Zahlungsdienst aus."],
    ["End_1", "endEvent", "Der Antrag ist abgeschlossen."],
    ["f3", "sequenceFlow", "Immer — die Auszahlung beendet den Vorgang."],
    ["DataObj_1", "dataObjectReference", "Der eingereichte Antrag mit allen Anlagen."],
  ];
  for (const [id, , text] of cases) {
    await open(page, id);
    await setDoc(page, "f-doc", text);
  }
  for (const [id, tag, text] of cases) {
    expect(await elementXML(page, tag, id)).toContain(`<bpmn:documentation>${text}</bpmn:documentation>`);
  }
  expect(page.__errors).toEqual([]);
});

test("editing replaces the documentation and clearing removes the element", async ({ page }) => {
  await mount(page);
  await open(page, "Start_1");
  await setDoc(page, "f-doc", "Startet auch über die Hotline.");

  let start = await elementXML(page, "startEvent", "Start_1");
  expect(start).toContain("<bpmn:documentation>Startet auch über die Hotline.</bpmn:documentation>");
  expect(start).not.toContain("Portal"); // the old text is replaced, not appended

  await setDoc(page, "f-doc", "   ");
  start = await elementXML(page, "startEvent", "Start_1");
  expect(start).not.toContain("documentation"); // blank leaves no empty element behind
  expect(page.__errors).toEqual([]);
});

test("documenting an element is undoable like any other edit", async ({ page }) => {
  await mount(page);
  await open(page, "Gateway_1");
  await setDoc(page, "f-doc", "Vorläufig.");
  expect(await elementXML(page, "exclusiveGateway", "Gateway_1")).toContain("Vorläufig.");

  await page.evaluate(() => window.__undo());
  expect(await elementXML(page, "exclusiveGateway", "Gateway_1")).not.toContain("Vorläufig.");
  expect(page.__errors).toEqual([]);
});

test("a sequence flow keeps its documentation next to its label", async ({ page }) => {
  await mount(page);
  await open(page, "f2");
  await expect(page.locator("#f-doc")).toHaveValue("Nur bei vollständigen Unterlagen.");
  await expect(page.locator("#f-name")).toHaveValue("ja");

  await setDoc(page, "f-doc", "Nur wenn die Bonität geprüft ist.");
  const flow = await elementXML(page, "sequenceFlow", "f2");
  expect(flow).toContain("<bpmn:documentation>Nur wenn die Bonität geprüft ist.</bpmn:documentation>");
  expect(flow).toContain('name="ja"');
  expect(page.__errors).toEqual([]);
});

test("the process itself is documented from the empty selection", async ({ page }) => {
  await mount(page);
  await expand(page, "Process");
  await expect(page.locator("#f-doc")).toHaveValue("Bearbeitet eingehende Anträge bis zur Auszahlung.");

  await setDoc(page, "f-doc", "Deckt Anträge aus Portal und Hotline ab.");
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:process id="dokumentiert"[\s\S]*?<bpmn:documentation>Deckt Anträge aus Portal und Hotline ab\.<\/bpmn:documentation>/);
  expect(page.__errors).toEqual([]);
});

test("a pool documents both the participant and the process it executes", async ({ page }) => {
  await mount(page, "collab");
  await page.evaluate(() => window.__select("Pool_sales"));
  await expand(page, "Pool");
  await expand(page, "Process");

  // Saving rebuilds the panel; the expanded groups persist, so the second field is
  // still on screen — the two descriptions are independent, not one shared field.
  await setDoc(page, "f-doc", "Der Vertrieb nimmt die Anfrage auf.");
  await setDoc(page, "f-procdoc", "Führt die Anfrage bis zum Angebot.");

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:participant id="Pool_sales"[\s\S]*?Der Vertrieb nimmt die Anfrage auf\./);
  expect(xml).toMatch(/<bpmn:process id="vertrieb"[\s\S]*?Führt die Anfrage bis zum Angebot\./);
  expect(page.__errors).toEqual([]);
});

test("a black-box pool is documented even though it runs no process", async ({ page }) => {
  await mount(page, "collab");
  await page.evaluate(() => window.__select("Pool_bank"));
  await expand(page, "Pool");
  await setDoc(page, "f-doc", "Externe Bank — nicht von uns modelliert.");

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:participant id="Pool_bank"[\s\S]*?Externe Bank — nicht von uns modelliert\./);
  expect(page.__errors).toEqual([]);
});

test("the collaboration itself takes a description", async ({ page }) => {
  await mount(page, "collab");
  await expand(page, "Collaboration");
  await setDoc(page, "f-doc", "Zusammenspiel von Vertrieb und Bank.");

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:collaboration id="Collab_1">[\s\S]*?Zusammenspiel von Vertrieb und Bank\./);
  expect(page.__errors).toEqual([]);
});
