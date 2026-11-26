// End-to-end coverage for the Modeler's warning about a variable the model reads and
// nothing in it writes (api/web/editor.js unwrittenReads), in the Problems bar and in
// the deploy panel.
//
// The compiler cannot give this warning: it knows a linked form by its id and nothing of
// the process a call activity calls, so every form field and every variable a child
// hands back would read as unwritten. The Modeler has both, from the Variables panel's
// own reading of the diagram, and says it beside the compiler's findings. It is a
// warning and never more: a value can come from outside the model — a message's payload,
// a start that is not declared — so the deploy goes ahead.
//
// The diagram is the Variables panel's (vars-scope-harness.html). One task reads
// `betrag`, which nothing writes. Everything else it reads is written somewhere the
// compiler does not see: the called process's `kindergebnis`, a multi-instance round's
// item, an input mapping's local, an agent tool's parameter and its toolCallId.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/vars-scope-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
});

const unwritten = (page) => page.locator("#prob-list .prob-row", {
  has: page.locator(".prob-rule", { hasText: /^variable\.unwritten$/ }),
});

test("a name read and written nowhere is a warning on the element that reads it", async ({ page }) => {
  await expect(page.locator("#problems-bar")).toHaveClass(/has-warnings/);
  await expect(unwritten(page)).toHaveCount(1);
  const row = unwritten(page).first();
  await expect(row).toHaveAttribute("data-el", "pruefen");
  await expect(row).toHaveClass(/prob-warning/);
  await expect(row.locator(".prob-msg")).toContainText("betrag");
  // Nothing the compiler cannot see is reported: the child's variables, the round's
  // item, the input mapping's local, the agent tool's parameter and call id.
  for (const n of ["kindergebnis", "pos", "limite", "suchbegriff", "toolCallId"]) {
    await expect(page.locator("#prob-list .prob-msg", { hasText: n }), n).toHaveCount(0);
  }
  expect(page.__errors).toEqual([]);
});

test("the warning goes once the name is no longer read", async ({ page }) => {
  await expect(unwritten(page)).toHaveCount(1);
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    const el = m.get("elementRegistry").get("pruefen");
    const script = el.businessObject.extensionElements.values.find((v) => v.$type === "zeebe:Script");
    m.get("modeling").updateModdleProperties(el, script, { expression: "=kundennr > limite" });
  });
  await expect(unwritten(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the deploy panel names what is read and unwritten, and still deploys", async ({ page }) => {
  await expect(unwritten(page)).toHaveCount(1);
  await page.locator("#deploy").click();
  const note = page.locator("#deploy-unwritten");
  await expect(note).toBeVisible();
  await expect(note).toContainText("betrag");
  await expect(note).toContainText("Prüfen");
  await expect(page.locator("#deploy-only")).toBeEnabled();
  await expect(page.locator("#deploy-run")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("the agent tool's parameter and call id are its own variables", async ({ page }) => {
  await page.locator("#vars-toggle").click();
  await page.evaluate(() => window.__select("suchen"));
  await expect(page.locator('#vars-list .vars-group[data-cat="@local"] .var-row[data-var="suchbegriff"]')).toBeVisible();
  await expect(page.locator('#vars-list .vars-group[data-cat="@local"] .var-row[data-var="toolCallId"]')).toBeVisible();
  await expect(page.locator('#vars-list .var-row[data-unwritten="1"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// A file with pools holds one process per pool, and each declares its own start
// variables and constants. They are written before the first element runs, in a pool
// as much as in a file that is one process — so reading one is not a finding. What
// nothing declares and nothing writes still is.
const POOLS = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" id="Defs_pools" targetNamespace="http://atlas/test">
  <bpmn:collaboration id="Collab_p">
    <bpmn:participant id="Pool_a" name="Verkauf" processRef="verkauf"/>
    <bpmn:participant id="Pool_b" name="Lager" processRef="lager"/>
    <bpmn:participant id="Pool_c" name="Kunde"/>
  </bpmn:collaboration>
  <bpmn:message id="Msg_meldung" name="Meldung">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= bestellnummer"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="verkauf" isExecutable="true">
    <bpmn:extensionElements>
      <atlas:startForm><atlas:startVariable name="bestellnummer" type="string" required="true"/></atlas:startForm>
      <atlas:constants><atlas:constant name="mwst" type="number" value="8.1"/></atlas:constants>
    </bpmn:extensionElements>
    <bpmn:startEvent id="Start_a"><bpmn:outgoing>a1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:intermediateCatchEvent id="Warten_a"><bpmn:incoming>a1</bpmn:incoming><bpmn:outgoing>a2</bpmn:outgoing><bpmn:messageEventDefinition id="Med_a" messageRef="Msg_meldung"/></bpmn:intermediateCatchEvent>
    <bpmn:scriptTask id="Rechnen_a"><bpmn:extensionElements><zeebe:script expression="= mwst * rabatt" resultVariable="steuer"/></bpmn:extensionElements><bpmn:incoming>a2</bpmn:incoming><bpmn:outgoing>a3</bpmn:outgoing></bpmn:scriptTask>
    <bpmn:endEvent id="End_a"><bpmn:incoming>a3</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="a1" sourceRef="Start_a" targetRef="Warten_a"/>
    <bpmn:sequenceFlow id="a2" sourceRef="Warten_a" targetRef="Rechnen_a"/>
    <bpmn:sequenceFlow id="a3" sourceRef="Rechnen_a" targetRef="End_a"/>
  </bpmn:process>
  <bpmn:process id="lager" isExecutable="true">
    <bpmn:extensionElements>
      <atlas:startForm><atlas:startVariable name="lagerort" type="string" required="false"/></atlas:startForm>
    </bpmn:extensionElements>
    <bpmn:startEvent id="Start_b"><bpmn:outgoing>b1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:scriptTask id="Suchen_b"><bpmn:extensionElements><zeebe:script expression="= lagerort + &quot;/A&quot;" resultVariable="fach"/></bpmn:extensionElements><bpmn:incoming>b1</bpmn:incoming><bpmn:outgoing>b2</bpmn:outgoing></bpmn:scriptTask>
    <bpmn:endEvent id="End_b"><bpmn:incoming>b2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="b1" sourceRef="Start_b" targetRef="Suchen_b"/>
    <bpmn:sequenceFlow id="b2" sourceRef="Suchen_b" targetRef="End_b"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D_p"><bpmndi:BPMNPlane id="P_p" bpmnElement="Collab_p">
    <bpmndi:BPMNShape id="Pool_a_s" bpmnElement="Pool_a" isHorizontal="true"><dc:Bounds x="130" y="40" width="600" height="140"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Start_a_s" bpmnElement="Start_a"><dc:Bounds x="200" y="92" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Warten_a_s" bpmnElement="Warten_a"><dc:Bounds x="290" y="92" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Rechnen_a_s" bpmnElement="Rechnen_a"><dc:Bounds x="380" y="70" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="End_a_s" bpmnElement="End_a"><dc:Bounds x="540" y="92" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="a1_e" bpmnElement="a1"><di:waypoint x="236" y="110"/><di:waypoint x="290" y="110"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="a2_e" bpmnElement="a2"><di:waypoint x="326" y="110"/><di:waypoint x="380" y="110"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="a3_e" bpmnElement="a3"><di:waypoint x="480" y="110"/><di:waypoint x="540" y="110"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNShape id="Pool_b_s" bpmnElement="Pool_b" isHorizontal="true"><dc:Bounds x="130" y="220" width="600" height="140"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Start_b_s" bpmnElement="Start_b"><dc:Bounds x="200" y="272" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Suchen_b_s" bpmnElement="Suchen_b"><dc:Bounds x="290" y="250" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="End_b_s" bpmnElement="End_b"><dc:Bounds x="450" y="272" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="b1_e" bpmnElement="b1"><di:waypoint x="236" y="290"/><di:waypoint x="290" y="290"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="b2_e" bpmnElement="b2"><di:waypoint x="390" y="290"/><di:waypoint x="450" y="290"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNShape id="Pool_c_s" bpmnElement="Pool_c" isHorizontal="true"><dc:Bounds x="130" y="400" width="600" height="60"/></bpmndi:BPMNShape>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

test("in a file with pools, a pool's start variables and constants are not unwritten", async ({ page }) => {
  await expect(unwritten(page)).toHaveCount(1); // the harness's own diagram has been read
  await page.evaluate((x) => window.__atlasModeler.importXML(x), POOLS);
  await expect(page.locator('#prob-list .prob-row[data-el="pruefen"]')).toHaveCount(0); // the new diagram has been read
  // The message's correlation key reads bestellnummer, the script tasks mwst and
  // lagerort: each is declared on its pool's process. rabatt is declared nowhere.
  await expect(unwritten(page)).toHaveCount(1);
  const row = unwritten(page).first();
  await expect(row).toHaveAttribute("data-el", "Rechnen_a");
  await expect(row.locator(".prob-msg")).toContainText("rabatt");
  for (const n of ["bestellnummer", "mwst", "lagerort"]) {
    await expect(page.locator("#prob-list .prob-msg", { hasText: n }), n).toHaveCount(0);
  }

  // The Variables panel lists them too, as it does for a file that is one process.
  await page.locator("#vars-toggle").click();
  for (const n of ["bestellnummer", "mwst", "lagerort"]) {
    await expect(page.locator("#vars-panel", { hasText: n }), n).toHaveCount(1);
  }
  expect(page.__errors).toEqual([]);
});
