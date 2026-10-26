// e2e for ids derived from an element's name (api/web/elementids.js,
// ADR-0474, amended 2026-10-23).
//
// Naming a BPMN element "Microsoft Teams bestellen" — in the properties panel or on the
// canvas — renames its id to activity_microsoft_teams_bestellen, so the author is spared
// typing the id as well. A second element of the same type with the same name becomes
// ..._01, a third ..._02: no two elements share an id. The id follows the name only
// while nobody has chosen it and nothing outside the diagram can hold it: an id the
// author typed, an id any deployed version of the process carries, and the process's own
// id are never touched.
import { test, expect } from "@playwright/test";

const DAY = new Date(2026, 9, 23, 10, 30, 0);

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.clock.setFixedTime(DAY);
  await page.goto("/element-ids-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("the name segment: lowercase ASCII words joined by underscores", async ({ page }) => {
  const got = await page.evaluate(() => {
    const s = window.__lib.nameSlug;
    return {
      teams: s("Microsoft Teams bestellen"),
      umlauts: s("Größe ändern, Übersicht prüfen"),
      accents: s("Café für Señor Ångström"),
      symbols: s("Bestellung prüfen & freigeben (2. Stufe)!"),
      blank: s("   "),
      onlySymbols: s("???"),
      long: s("Dies ist ein sehr langer Name für eine Aufgabe, der weit über das hinausgeht, was eine ID tragen sollte"),
    };
  });
  expect(got.teams).toBe("microsoft_teams_bestellen");
  expect(got.umlauts).toBe("groesse_aendern_uebersicht_pruefen");
  expect(got.accents).toBe("cafe_fuer_senor_angstroem");
  expect(got.symbols).toBe("bestellung_pruefen_freigeben_2_stufe");
  expect(got.blank).toBe("");
  expect(got.onlySymbols).toBe("");
  expect(got.long.length).toBeLessThanOrEqual(60);
  expect(got.long).toMatch(/^[a-z0-9]+(_[a-z0-9]+)*$/);
});

test("naming a task in the panel or on the canvas names its id", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const [a, b] = await page.evaluate(() => window.__draw("bpmn:Task", 2));
  expect(await page.evaluate(([id]) => window.__name(id, "Microsoft Teams bestellen"), [a]))
    .toBe("activity_microsoft_teams_bestellen");
  expect(await page.evaluate(([id]) => window.__name(id, "Lizenz zuweisen", true), [b]))
    .toBe("activity_lizenz_zuweisen");
  const gw = await page.evaluate(() => window.__draw("bpmn:ExclusiveGateway", 1));
  expect(await page.evaluate(([id]) => window.__name(id, "Genehmigt?"), gw)).toBe("gateway_genehmigt");
  const ev = await page.evaluate(() => window.__draw("bpmn:EndEvent", 1));
  expect(await page.evaluate(([id]) => window.__name(id, "Bestellt", true), ev)).toBe("event_bestellt");
  const flow = await page.evaluate(([x, y]) => window.__connect(x, y), ["activity_microsoft_teams_bestellen", "activity_lizenz_zuweisen"]);
  expect(await page.evaluate(([id]) => window.__name(id, "ja"), [flow])).toBe("flow_ja");
});

test("a second element with the same name gets _01, a third _02", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const ids = await page.evaluate(() => window.__draw("bpmn:Task", 3));
  const named = [];
  for (const id of ids) {
    named.push(await page.evaluate(([x]) => window.__name(x, "Microsoft Teams bestellen"), [id]));
  }
  expect(named).toEqual([
    "activity_microsoft_teams_bestellen",
    "activity_microsoft_teams_bestellen_01",
    "activity_microsoft_teams_bestellen_02",
  ]);
  // Another type with the same name is told apart by its type, not by a counter.
  const gw = await page.evaluate(() => window.__draw("bpmn:ExclusiveGateway", 1));
  expect(await page.evaluate(([id]) => window.__name(id, "Microsoft Teams bestellen"), gw))
    .toBe("gateway_microsoft_teams_bestellen");
  const all = await page.evaluate(() => window.__ids());
  expect(new Set(all).size).toBe(all.length);
});

test("the id follows a renamed element and frees the old one", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const [a, b] = await page.evaluate(() => window.__draw("bpmn:Task", 2));
  await page.evaluate(([id]) => window.__name(id, "Teams bestellen"), [a]);
  const second = await page.evaluate(([id]) => window.__name(id, "Teams bestellen"), [b]);
  expect(second).toBe("activity_teams_bestellen_01");
  expect(await page.evaluate(() => window.__name("activity_teams_bestellen", "Teams Lizenz bestellen")))
    .toBe("activity_teams_lizenz_bestellen");
  // A duplicate keeps following its own name, and takes the base id once it is free.
  expect(await page.evaluate(() => window.__name("activity_teams_bestellen_01", "Teams bestellen!")))
    .toBe("activity_teams_bestellen");
});

test("an empty name, or one without letters, leaves the id as it is", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const [a] = await page.evaluate(() => window.__draw("bpmn:Task", 1));
  expect(await page.evaluate(([id]) => window.__name(id, "???"), [a])).toBe(a);
  expect(await page.evaluate(([id]) => window.__name(id, ""), [a])).toBe(a);
});

test("an id the author typed is never overwritten", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const [a] = await page.evaluate(() => window.__draw("bpmn:Task", 1));
  await page.evaluate(([id]) => window.__setId(id, "teams_order"), [a]);
  expect(await page.evaluate(() => window.__name("teams_order", "Microsoft Teams bestellen"))).toBe("teams_order");
  // Nor one the name produced and the author then changed.
  const [b] = await page.evaluate(() => window.__draw("bpmn:Task", 1));
  await page.evaluate(([id]) => window.__name(id, "Lizenz prüfen"), [b]);
  await page.evaluate(() => window.__setId("activity_lizenz_pruefen", "check_licence"));
  expect(await page.evaluate(() => window.__name("check_licence", "Lizenz kontrollieren"))).toBe("check_licence");
});

test("undo takes the name and the id back in one step", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const [a] = await page.evaluate(() => window.__draw("bpmn:Task", 1));
  await page.evaluate(([id]) => window.__name(id, "Microsoft Teams bestellen"), [a]);
  await page.evaluate(() => window.__undo());
  expect(await page.evaluate(([id]) => window.__has(id), [a])).toBe(true);
  expect(await page.evaluate(() => window.__has("activity_microsoft_teams_bestellen"))).toBe(false);
  await page.evaluate(() => window.__redo());
  expect(await page.evaluate(() => window.__has("activity_microsoft_teams_bestellen"))).toBe(true);
});

// An element of a deployed process keeps its id: instances, their migration and what is
// bound to the element (capabilities, landscape bindings) know it by that id. An element
// only the draft holds — an old bpmn-js id among them — is renamed as usual.
test("an id a deployed version carries is kept, the process id too", async ({ page }) => {
  const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" id="Definitions_x" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="existing" isExecutable="true">
    <bpmn:task id="activity_20261020_curie"/>
    <bpmn:task id="Activity_0abc123"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="existing">
    <bpmndi:BPMNShape id="s1" bpmnElement="activity_20261020_curie"><dc:Bounds x="100" y="100" width="100" height="80"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="s2" bpmnElement="Activity_0abc123"><dc:Bounds x="300" y="100" width="100" height="80"/></bpmndi:BPMNShape>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;
  const deployed = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d"><bpmn:process id="existing"><bpmn:task id="activity_20261020_curie"/></bpmn:process></bpmn:definitions>`;
  await page.evaluate((w) => window.__mount(w), {
    draftXml: xml,
    processes: [{ key: 7, processId: "existing", version: 1, name: "existing" }],
    deployedXml: { 7: deployed },
  });
  expect(await page.evaluate(() => window.__name("activity_20261020_curie", "Microsoft Teams bestellen")))
    .toBe("activity_20261020_curie");
  expect(await page.evaluate(() => window.__name("Activity_0abc123", "Microsoft Teams bestellen")))
    .toBe("activity_microsoft_teams_bestellen");
  expect(await page.evaluate(() => window.__name("existing", "Teams-Bestellung"))).toBe("existing");
});
