// The way from a step to its form, and back (api/web/editor.js, api/web/form-editor.js,
// api/web/form-return.js; ADR-0465).
//
// A start event and a user task each name a form, and until these doors existed the
// Modeler could only say which: the picker showed the name, and reaching the form meant
// leaving for the Modeler home and finding it in a list. "Create a new form" opened a
// second tab and ended with "then reopen this to link it". A business rule task had
// already been given better — "＋ New decision" and "Edit", a way back, and the decision
// adopted on return (ADR-0320) — so the form was the one linked artifact with no door.
//
// What is pinned here is the round trip, because none of it is visible to the Go suite
// and every way it can go wrong is quiet: leaving must not cost the diagram's edits, the
// form editor must know where back is, a form created or renamed on the way must arrive
// linked, a record left behind must not rewrite a link changed since, and leaving the
// form editor must not cost the form's edits either.
//
// These drive the REAL app (index.html → app.js) against a mocked /api/v1, because the
// routing is half of what is under test.
import { test, expect } from "@playwright/test";

// One process with every element that offers a form picker: a plain start event that
// names a start form, a user task with none, a user task naming a form this server does
// not hold, and a service task naming a repair form (ADR-0169).
const XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_reise" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="reise" name="Reise" isExecutable="true">
    <bpmn:startEvent id="Start_1" name="Reise angefragt">
      <bpmn:extensionElements><zeebe:formDefinition formId="reise-antrag"/></bpmn:extensionElements>
      <bpmn:outgoing>f0</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:userTask id="Task_review" name="Prüfen"><bpmn:incoming>f0</bpmn:incoming><bpmn:outgoing>f1</bpmn:outgoing></bpmn:userTask>
    <bpmn:userTask id="Task_lost" name="Nachfragen">
      <bpmn:extensionElements><zeebe:formDefinition formId="gone"/></bpmn:extensionElements>
      <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:serviceTask id="Activity_charge" name="Belasten">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="charge-card"/>
        <zeebe:formDefinition formId="fix-payment"/>
      </bpmn:extensionElements>
      <bpmn:incoming>f2</bpmn:incoming><bpmn:outgoing>f3</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End_1" name="Erledigt"><bpmn:incoming>f3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f0" sourceRef="Start_1" targetRef="Task_review"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Task_review" targetRef="Task_lost"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Task_lost" targetRef="Activity_charge"/>
    <bpmn:sequenceFlow id="f3" sourceRef="Activity_charge" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="reise">
    <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_review_di" bpmnElement="Task_review"><dc:Bounds x="260" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Task_lost_di" bpmnElement="Task_lost"><dc:Bounds x="420" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Activity_charge_di" bpmnElement="Activity_charge"><dc:Bounds x="580" y="98" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"><dc:Bounds x="740" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f0_di" bpmnElement="f0"><di:waypoint x="196" y="138"/><di:waypoint x="260" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="360" y="138"/><di:waypoint x="420" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f2_di" bpmnElement="f2"><di:waypoint x="520" y="138"/><di:waypoint x="580" y="138"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f3_di" bpmnElement="f3"><di:waypoint x="680" y="138"/><di:waypoint x="740" y="138"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

// installMock stands in for the server: the draft above (whose stored XML follows every
// save, so coming back opens what was saved on the way out), the forms it can link, the
// deployment it was once deployed as, and the writes a round trip makes — recorded so a
// test asserts what was sent rather than only what was then shown.
//
// `projectId` files the draft — and the deployment it was once deployed as — under an
// application, the way a diagram authored inside one is filed.
function installMock(page, { projectId = "" } = {}) {
  const state = {
    stored: XML,
    draftSaves: [],
    formSaves: [],
    forms: new Map([
      ["reise-antrag", "Reise – Antrag"],
      ["review-form", "Prüfung"],
      ["fix-payment", "Fix the payment"],
    ]),
  };
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/drafts" && method === "GET") {
      return route.fulfill({ json: [{ processId: "reise", name: "Reise", projectId }] });
    }
    if (path === "/api/v1/drafts/reise/xml") {
      return route.fulfill({ body: state.stored, contentType: "application/xml" });
    }
    if (path === "/api/v1/drafts" && method === "POST") {
      state.stored = request.postData();
      state.draftSaves.push(state.stored);
      return route.fulfill({ json: { processId: "reise", name: "Reise" } });
    }
    if (path === "/api/v1/processes" && method === "GET") {
      return route.fulfill({ json: [{ key: 7, processId: "reise", name: "Reise", version: 1, projectId }] });
    }
    if (path === "/api/v1/processes/7/xml") {
      return route.fulfill({ body: XML, contentType: "application/xml" });
    }
    if (path === "/api/v1/forms" && method === "GET") {
      return route.fulfill({ json: [...state.forms].map(([id, name]) => ({ id, name })) });
    }
    if (/^\/api\/v1\/forms\/[^/]+\/availability$/.test(path)) {
      return route.fulfill({ json: { id: decodeURIComponent(path.split("/")[4]), available: true } });
    }
    if (/^\/api\/v1\/forms\/[^/]+$/.test(path) && method === "GET") {
      const id = decodeURIComponent(path.split("/").pop());
      if (!state.forms.has(id)) return route.fulfill({ status: 404, json: { error: "form not found" } });
      return route.fulfill({ json: {
        id, name: state.forms.get(id), projectId: "",
        schema: { type: "default", id, components: [{ type: "textfield", key: "ziel", label: "Ziel" }] },
      } });
    }
    if (path === "/api/v1/forms" && method === "POST") {
      const body = request.postDataJSON();
      state.formSaves.push(body);
      const renamed = body.from && body.from !== body.id ? body.from : "";
      if (renamed) state.forms.delete(renamed);
      state.forms.set(body.id, body.name);
      return route.fulfill({ json: { id: body.id, name: body.name, renamedFrom: renamed } });
    }
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Reisen", myRole: "owner" }] });
    }
    if (path.endsWith("/validate")) return route.fulfill({ json: { version: "test", problems: [] } });
    return route.fulfill({ json: [] });
  });
  return state;
}

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  // The live collaboration session (ADR-0140) is not what this is about, and the static
  // server cannot serve its stream; without EventSource attachCollab is a no-op.
  await page.addInitScript(() => { delete window.EventSource; });
});

// openDiagram opens a route that mounts the BPMN editor and waits for the canvas.
async function openDiagram(page, hash = "#/modeler/draft/reise") {
  await page.goto("/index.html" + hash);
  await expect(page.locator('[data-element-id="Start_1"]').first()).toBeVisible({ timeout: 20000 });
  await page.locator('[data-tab="implement"]').click();
}

// select selects an element and opens the named property group, unless it is open.
async function select(page, id, group) {
  await page.evaluate((el) => {
    const m = window.__atlasModeler;
    m.get("selection").select(m.get("elementRegistry").get(el));
  }, id);
  const g = page.locator(`.pgroup[data-group="${group}"]`);
  await expect(g).toBeVisible();
  if (await g.evaluate((n) => n.classList.contains("collapsed"))) {
    await g.locator(".pgroup-head").click();
  }
}

// formIdOf reads the form an element names in the diagram on screen.
const formIdOf = (page, id) => page.evaluate((el) => {
  const bo = window.__atlasModeler.get("elementRegistry").get(el).businessObject;
  const fd = ((bo.extensionElements && bo.extensionElements.values) || [])
    .find((v) => v.$type === "zeebe:FormDefinition");
  return fd ? fd.formId || "" : "";
}, id);

const selectedIds = (page) => page.evaluate(() =>
  window.__atlasModeler.get("selection").get().map((e) => e.id));

const hash = (page) => page.evaluate(() => location.hash);

// formEditorReady waits for the form editor's Design pane, which form-js renders after
// the mount call returns.
const formEditorReady = (page) =>
  expect(page.locator("#bio-properties-panel-id")).toBeAttached({ timeout: 20000 });

// retypeFormId types into the Design pane's ID field the way form-identity.spec.mjs
// does: form-js's preact panel listens for the native setter and an input event.
const retypeFormId = (page, v) => page.evaluate((value) => {
  const el = document.getElementById("bio-properties-panel-id");
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
  set.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  el.dispatchEvent(new Event("change", { bubbles: true }));
}, v);

test("a start event's form is one click away, with a new one beside it", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await select(page, "Start_1", "Start form");

  const open = page.locator("#f-form-open");
  await expect(open).toHaveText("Open form");
  // A real link: what a middle click, "open in new tab" or "copy link" reaches is the
  // form's own address, with no way back attached to it.
  await expect(open).toHaveAttribute("href", "#/modeler/form/e/reise-antrag");
  // The generator still opens on the start-form case (ADR-0260).
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new/for/reise");
  // The panel no longer asks the author to come back and link the form by hand.
  await expect(page.locator(".pgroup[data-group=\"Start form\"]")).not.toContainText("reopen this to link it");
  expect(page.__errors).toEqual([]);
});

test("Open form follows the picker, and is unavailable while there is nothing to open", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await select(page, "Task_review", "Form");

  const open = page.locator("#f-form-open");
  await expect(open).toHaveAttribute("aria-disabled", "true");
  await expect(open).not.toHaveAttribute("href", /./);
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new/for/reise/Task_review");

  // Picking a form makes it openable at once, before anything is saved.
  await page.locator("#f-form").selectOption("review-form");
  await expect(open).toHaveAttribute("href", "#/modeler/form/e/review-form");
  await expect(open).not.toHaveAttribute("aria-disabled", "true");

  // A form this server does not hold has nowhere to open: the picker keeps showing the
  // link as "(missing)", and the door says why it is shut.
  await select(page, "Task_lost", "Form");
  await expect(page.locator("#f-form")).toContainText("(fehlt)");
  await expect(open).toHaveAttribute("aria-disabled", "true");
  await expect(open).toHaveAttribute("title", /no form .gone./i);
  expect(page.__errors).toEqual([]);
});

test("an untouched diagram leaves for the form without saving, and the form knows the way back", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await select(page, "Start_1", "Start form");

  await page.locator("#f-form-open").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag/from/reise/Start_1");
  await formEditorReady(page);
  expect(state.draftSaves).toHaveLength(0);

  // Back names the diagram the author came from, not the Modeler home.
  const back = page.locator("#form-back");
  await expect(back).toHaveAttribute("href", "#/modeler/draft/reise");
  await expect(back).toHaveText("← Reise");

  // Coming back selects the element the trip left from.
  await back.click();
  await expect(page.locator('[data-element-id="Start_1"]').first()).toBeVisible({ timeout: 20000 });
  await expect.poll(() => selectedIds(page)).toEqual(["Start_1"]);
  expect(await formIdOf(page, "Start_1")).toBe("reise-antrag");
  expect(page.__errors).toEqual([]);
});

test("a diagram with edits is saved before it leaves for the form", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    m.get("modeling").updateLabel(m.get("elementRegistry").get("End_1"), "Abgeschlossen");
  });
  await select(page, "Start_1", "Start form");

  await page.locator("#f-form-open").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag/from/reise/Start_1");
  expect(state.draftSaves).toHaveLength(1);
  expect(state.draftSaves[0]).toContain('name="Abgeschlossen"');
  expect(page.__errors).toEqual([]);
});

test("a form renamed on the way comes back linked under its new id", async ({ page }) => {
  const state = installMock(page);
  page.on("dialog", (d) => d.accept()); // the form editor asks before a rename
  await openDiagram(page);
  await select(page, "Start_1", "Start form");
  await page.locator("#f-form-open").click();
  await formEditorReady(page);

  await retypeFormId(page, "reise-antrag-v2");
  await expect(page.locator("#form-id-chip")).toHaveText("reise-antrag-v2");
  await page.locator("#form-save").click();
  await expect.poll(() => state.formSaves.length).toBe(1);
  expect(state.formSaves[0]).toMatchObject({ id: "reise-antrag-v2", from: "reise-antrag" });
  // The address follows the form to its new id and keeps the way back.
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag-v2/from/reise/Start_1");

  await page.locator("#form-back").click();
  await expect(page.locator('[data-element-id="Start_1"]').first()).toBeVisible({ timeout: 20000 });
  // Without this the start event would come back naming a form that no longer exists.
  await expect.poll(() => formIdOf(page, "Start_1")).toBe("reise-antrag-v2");
  await expect.poll(() => selectedIds(page)).toEqual(["Start_1"]);
  expect(page.__errors).toEqual([]);
});

test("a form created for a user task is linked when the author comes back", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await select(page, "Task_review", "Form");

  await page.locator("#f-form-new").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/new/for/reise/Task_review/from/reise/Task_review");
  await formEditorReady(page);
  await expect(page.locator("#form-back")).toHaveAttribute("href", "#/modeler/draft/reise");

  await page.locator("#form-name").fill("Prüfung der Reise");
  await page.locator("#form-save").click();
  await expect.poll(() => state.formSaves.length).toBe(1);
  const created = state.formSaves[0].id;
  expect(created).toMatch(/^form-/);
  await expect.poll(() => hash(page)).toBe(`#/modeler/form/e/${created}/from/reise/Task_review`);

  await page.locator("#form-back").click();
  await expect(page.locator('[data-element-id="Task_review"]').first()).toBeVisible({ timeout: 20000 });
  await expect.poll(() => formIdOf(page, "Task_review")).toBe(created);
  // The link is an edit like any other: the diagram has something to save now.
  expect(await page.evaluate(() => window.__atlasModeler.get("commandStack").canUndo())).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("a link changed since the trip began is not rewritten by what the trip left behind", async ({ page }) => {
  installMock(page);
  page.on("dialog", (d) => d.accept());
  await openDiagram(page);
  await select(page, "Start_1", "Start form");
  await page.locator("#f-form-open").click();
  await formEditorReady(page);
  await retypeFormId(page, "reise-antrag-v2");
  await expect(page.locator("#form-id-chip")).toHaveText("reise-antrag-v2");
  await page.locator("#form-save").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag-v2/from/reise/Start_1");

  // The author does not come back this way. Meanwhile the start event is linked to
  // something else — here the stored draft changes underneath, as another session's
  // save would change it.
  await page.evaluate(() => {
    const raw = sessionStorage.getItem("atlas.form.trip");
    if (!raw) throw new Error("no trip recorded");
  });
  await page.route("**/api/v1/drafts/reise/xml", (route) => route.fulfill({
    body: XML.replace('formId="reise-antrag"', 'formId="review-form"'), contentType: "application/xml",
  }));
  await page.goto("/index.html#/modeler/draft/reise");
  await expect(page.locator('[data-element-id="Start_1"]').first()).toBeVisible({ timeout: 20000 });
  expect(await formIdOf(page, "Start_1")).toBe("review-form");
  expect(await page.evaluate(() => window.__atlasModeler.get("commandStack").canUndo())).toBe(false);
  expect(page.__errors).toEqual([]);
});

test("leaving the form editor asks before it drops the form's unsaved edits", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await select(page, "Start_1", "Start form");
  await page.locator("#f-form-open").click();
  await formEditorReady(page);

  let asked = 0;
  page.on("dialog", async (d) => { asked++; await d.dismiss(); });
  await page.locator("#form-name").fill("Reise – Antrag (neu)");
  await page.locator("#form-back").click();
  await expect.poll(() => asked).toBe(1);
  // Refused: the author is still with the form, edits intact.
  expect(await hash(page)).toBe("#/modeler/form/e/reise-antrag/from/reise/Start_1");
  await expect(page.locator("#form-name")).toHaveValue("Reise – Antrag (neu)");
  expect(page.__errors).toEqual([]);
});

test("an untouched form leaves without a question", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await select(page, "Start_1", "Start form");
  await page.locator("#f-form-open").click();
  await formEditorReady(page);

  let asked = 0;
  page.on("dialog", async (d) => { asked++; await d.dismiss(); });
  await page.locator("#form-back").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/draft/reise");
  expect(asked).toBe(0);
});

test("a modified click opens the form in a new tab and leaves the diagram where it is", async ({ page, context }) => {
  const state = installMock(page);
  await openDiagram(page);
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    m.get("modeling").updateLabel(m.get("elementRegistry").get("End_1"), "Abgeschlossen");
  });
  await select(page, "Start_1", "Start form");

  const [tab] = await Promise.all([
    context.waitForEvent("page"),
    page.locator("#f-form-open").click({ modifiers: ["ControlOrMeta"] }),
  ]);
  // The new tab is the form's own address; the diagram stays open here with its edits,
  // so there is nothing to save and no way back to carry.
  await expect.poll(() => tab.url()).toContain("#/modeler/form/e/reise-antrag");
  expect(tab.url()).not.toContain("/from/");
  expect(await hash(page)).toBe("#/modeler/draft/reise");
  expect(state.draftSaves).toHaveLength(0);
  await tab.close();
});

test("a repair form's new form stays plain, and still finds its way back", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await select(page, "Activity_charge", "Repair form");

  await expect(page.locator("#f-form-open")).toHaveAttribute("href", "#/modeler/form/e/fix-payment");
  // A repair form is not something the generator writes (see repairFormHTML), so its
  // new form carries no step — only the way back.
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new");
  await page.locator("#f-form-new").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/new/from/reise/Activity_charge");
  await formEditorReady(page);
  await expect(page.locator("#form-back")).toHaveAttribute("href", "#/modeler/draft/reise");
  expect(page.__errors).toEqual([]);
});

test("a deployed version opens the form plainly, since there is no draft to come back to", async ({ page }) => {
  installMock(page);
  await openDiagram(page, "#/modeler/d/7");
  await select(page, "Start_1", "Start form");

  await page.locator("#f-form-open").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag");
  await formEditorReady(page);
  await expect(page.locator("#form-back")).not.toHaveAttribute("href", /draft/);
  expect(page.__errors).toEqual([]);
});

// ---------------------------------------------------------------------------------
// The canvas: the same door, where the author is looking (ADR-0465,
// amendment 2026-10-19). A context-pad entry on an element that names a form, and in
// the Implement view a mark on a start event or user task with one, which is a link.
// ---------------------------------------------------------------------------------

// pad is the context pad bpmn-js opens beside the selected element; padEntry its form door.
const padEntry = (page) => page.locator('.djs-context-pad .entry[data-action="atlas-open-form"]');

async function selectOnly(page, id) {
  await page.evaluate((el) => {
    const m = window.__atlasModeler;
    m.get("selection").select(m.get("elementRegistry").get(el));
  }, id);
}

test("the context pad opens the form an element names, and offers nothing where none is named", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);

  await selectOnly(page, "Task_review");
  await expect(page.locator(".djs-context-pad")).toBeVisible();
  await expect(padEntry(page)).toHaveCount(0);

  await selectOnly(page, "Start_1");
  await expect(padEntry(page)).toBeVisible();
  await expect(padEntry(page)).toHaveAttribute("title", /reise-antrag/);
  await padEntry(page).click();
  // The panel's round trip, entered from the canvas: the way back is on the address.
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag/from/reise/Start_1");
  await formEditorReady(page);
  expect(state.draftSaves).toHaveLength(0);
  await expect(page.locator("#form-back")).toHaveAttribute("href", "#/modeler/draft/reise");
  expect(page.__errors).toEqual([]);
});

test("the context pad reaches a repair form too", async ({ page }) => {
  installMock(page);
  await openDiagram(page);
  await selectOnly(page, "Activity_charge");
  await padEntry(page).click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/fix-payment/from/reise/Activity_charge");
});

test("a form the server does not hold is said, not opened", async ({ page }) => {
  const state = installMock(page);
  await openDiagram(page);
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    m.get("modeling").updateLabel(m.get("elementRegistry").get("End_1"), "Abgeschlossen");
  });
  await selectOnly(page, "Task_lost");
  await padEntry(page).click();
  await expect(page.locator("#toast")).toContainText("kein Formular «gone»");
  // Nothing was traded for a door that opens on nothing: no save, no navigation.
  expect(await hash(page)).toBe("#/modeler/draft/reise");
  expect(state.draftSaves).toHaveLength(0);
});

test("a modified click on the context pad opens the form in a new tab", async ({ page, context }) => {
  const state = installMock(page);
  await openDiagram(page);
  await selectOnly(page, "Start_1");
  const [tab] = await Promise.all([
    context.waitForEvent("page"),
    padEntry(page).click({ modifiers: ["ControlOrMeta"] }),
  ]);
  await expect.poll(() => tab.url()).toContain("#/modeler/form/e/reise-antrag");
  expect(tab.url()).not.toContain("/from/");
  expect(await hash(page)).toBe("#/modeler/draft/reise");
  expect(state.draftSaves).toHaveLength(0);
  await tab.close();
});

test("the Implement view marks a step with a form, and the mark is the door", async ({ page }) => {
  installMock(page);
  // openDiagram lands on the Implement view, which is where the mark is drawn.
  await openDiagram(page);
  const badge = (id) => page.locator(`.djs-overlays[data-container-id="${id}"] .djs-overlay-form-badge a.form-badge`);
  await expect(badge("Start_1")).toBeVisible();
  await expect(badge("Start_1")).toHaveAttribute("href", "#/modeler/form/e/reise-antrag");
  await expect(badge("Start_1")).toHaveAttribute("title", /reise-antrag/);
  await expect(badge("Task_lost")).toBeVisible();
  // No form, no mark; and a repair form is an operator's, not a step's, so it is not
  // drawn as one — the context pad and the panel still reach it.
  await expect(badge("Task_review")).toHaveCount(0);
  await expect(badge("Activity_charge")).toHaveCount(0);

  // Picking a form draws the mark at once.
  await select(page, "Task_review", "Form");
  await page.locator("#f-form").selectOption("review-form");
  await expect(badge("Task_review")).toHaveAttribute("href", "#/modeler/form/e/review-form");

  // The Design view stays the drawing it is.
  await page.locator('[data-tab="design"]').click();
  await expect(page.locator("a.form-badge")).toHaveCount(0);
  await page.locator('[data-tab="implement"]').click();

  // The test viewport is narrow enough that the start event sits under the palette;
  // move the drawing clear of it, as an author would, and click the mark itself.
  await page.evaluate(() => window.__atlasModeler.get("canvas").scroll({ dx: 160, dy: 0 }));
  await badge("Start_1").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/e/reise-antrag/from/reise/Start_1");
  expect(page.__errors).toEqual([]);
});

// ---------------------------------------------------------------------------------
// A form created from a step belongs where the step does (ADR-0465, amendment
// 2026-10-20). Until the "＋ New form" door named the diagram's application, every form
// created from the Modeler was filed under none and turned up as ungrouped, apart from
// the process it was written for — and an application's release did not carry it.
// ---------------------------------------------------------------------------------

test("a form created for a step is filed under the diagram's application", async ({ page }) => {
  const state = installMock(page, { projectId: "app-1" });
  await openDiagram(page);
  await select(page, "Task_review", "Form");
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new/p/app-1/for/reise/Task_review");

  await page.locator("#f-form-new").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/new/p/app-1/for/reise/Task_review/from/reise/Task_review");
  await formEditorReady(page);
  // The way back is still the diagram's, not the application's.
  await expect(page.locator("#form-back")).toHaveAttribute("href", "#/modeler/draft/reise");
  await page.locator("#form-save").click();
  await expect.poll(() => state.formSaves.length).toBe(1);
  expect(state.formSaves[0].projectId).toBe("app-1");

  await page.locator("#form-back").click();
  await expect(page.locator('[data-element-id="Task_review"]').first()).toBeVisible({ timeout: 20000 });
  await expect.poll(() => formIdOf(page, "Task_review")).toBe(state.formSaves[0].id);
  expect(page.__errors).toEqual([]);
});

test("a start form and a repair form are filed under the application too", async ({ page }) => {
  const state = installMock(page, { projectId: "app-1" });
  await openDiagram(page);
  await select(page, "Start_1", "Start form");
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new/p/app-1/for/reise");

  // A repair form's new form stays without the generator's step, and gains the
  // application all the same.
  await select(page, "Activity_charge", "Repair form");
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new/p/app-1");
  await page.locator("#f-form-new").click();
  await expect.poll(() => hash(page)).toBe("#/modeler/form/new/p/app-1/from/reise/Activity_charge");
  await formEditorReady(page);
  await page.locator("#form-save").click();
  await expect.poll(() => state.formSaves.length).toBe(1);
  expect(state.formSaves[0].projectId).toBe("app-1");
  expect(page.__errors).toEqual([]);
});

test("a deployed version files a new form under the application it was deployed from", async ({ page }) => {
  const state = installMock(page, { projectId: "app-1" });
  await openDiagram(page, "#/modeler/d/7");
  await select(page, "Task_review", "Form");
  await expect(page.locator("#f-form-new")).toHaveAttribute("href", "#/modeler/form/new/p/app-1/for/reise/Task_review");

  await page.locator("#f-form-new").click();
  // No draft to come back to, so no way back on the address — and back goes to the
  // application the form now belongs to.
  await expect.poll(() => hash(page)).toBe("#/modeler/form/new/p/app-1/for/reise/Task_review");
  await formEditorReady(page);
  await expect(page.locator("#form-back")).toHaveAttribute("href", "#/modeler/p/app-1");
  await expect(page.locator("#form-back")).toHaveText("← Reisen");
  await page.locator("#form-save").click();
  await expect.poll(() => state.formSaves.length).toBe(1);
  expect(state.formSaves[0].projectId).toBe("app-1");
  expect(page.__errors).toEqual([]);
});
