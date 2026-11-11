// End-to-end coverage for the Operations page *Interactions* (ADR-0559 §7, §8).
// Drives the REAL page, and the real vendored bpmn-js, against a mocked /api/v1: the
// graph opens as a BPMN collaboration with every pool collapsed — senders above the
// process, receivers below — possible and observed message flows are told apart by
// colour, a process expands to its deployed diagram and its flows then dock on the
// sending and receiving elements, the sends nobody received are named, the replay
// reads its history backwards page by page until it begins, plays one message at a
// time or bundled, and Live appends what came after. The Go suite decides what the
// routes answer and to whom; this checks the wiring.
import { test, expect } from "@playwright/test";

const GRAPH = {
  processId: "bestellung",
  participants: [
    { id: "process:bestellung", kind: "process", center: true, name: "Sekretariat", processId: "bestellung", version: 2, definitionKey: 2 },
    { id: "process:versand", kind: "process", name: "Versand", processId: "versand", version: 1, definitionKey: 10 },
    { id: "process:rechnung", kind: "process", name: "Rechnung", processId: "rechnung", version: 1, definitionKey: 20 },
    { id: "inbound-watch:c1", kind: "inbound-watch", name: "Lager-Jira", connectorId: "c1", workerKind: "jira" },
    { id: "outside-view", kind: "outside-view" },
  ],
  exchanges: [
    { from: "process:bestellung", to: "process:versand", message: "freigabe", possible: true,
      observed: { received: 3, started: 3, lastAt: "2026-11-11T10:00:00Z" } },
    { from: "process:bestellung", to: "process:rechnung", message: "freigabe", possible: true },
    { from: "process:versand", to: "process:bestellung", message: "versandt", possible: true,
      observed: { received: 3, started: 0, lastAt: "2026-11-11T10:00:01Z" } },
    { from: "inbound-watch:c1", to: "process:bestellung", message: "versandt", possible: true },
    { from: "outside-view", to: "process:bestellung", message: "storno", possible: false,
      observed: { received: 1, started: 0, lastAt: "2026-11-11T09:00:00Z" } },
  ],
  unreceived: [{ from: "process:bestellung", message: "protokoll", count: 2, lastAt: "2026-11-11T10:00:00Z" }],
  ports: [
    { direction: "out", message: "freigabe", elements: ["release"], roles: ["throw"] },
    { direction: "out", message: "protokoll", elements: ["note"], roles: ["throw"] },
    { direction: "in", message: "versandt", elements: ["wait"], roles: ["catch"] },
  ],
  observed: { from: "2026-11-10T10:00:00Z", to: "2026-11-11T10:00:00Z", readFrom: "2026-11-10T10:00:00Z", rows: 40, partial: false },
  retention: "720h0m0s",
};


// The deployed models an expanded pool draws. Both use the element id "start", which
// the page must keep apart.
const ORDER_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="d1" targetNamespace="t">
  <collaboration id="C"><participant id="P_order" name="Sekretariat" processRef="bestellung"/></collaboration>
  <message id="Msg_release" name="freigabe"/>
  <process id="bestellung" isExecutable="true">
    <startEvent id="start"><outgoing>f1</outgoing></startEvent>
    <intermediateThrowEvent id="release"><incoming>f1</incoming><outgoing>f2</outgoing><messageEventDefinition messageRef="Msg_release"/></intermediateThrowEvent>
    <intermediateCatchEvent id="wait"><incoming>f2</incoming><messageEventDefinition/></intermediateCatchEvent>
    <sequenceFlow id="f1" sourceRef="start" targetRef="release"/>
    <sequenceFlow id="f2" sourceRef="release" targetRef="wait"/>
  </process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="PL" bpmnElement="C">
    <bpmndi:BPMNShape id="P_order_di" bpmnElement="P_order" isHorizontal="true"><dc:Bounds x="100" y="50" width="500" height="160"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="170" y="110" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="release_di" bpmnElement="release"><dc:Bounds x="300" y="110" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="wait_di" bpmnElement="wait"><dc:Bounds x="440" y="110" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="f1_di" bpmnElement="f1"><di:waypoint x="206" y="128"/><di:waypoint x="300" y="128"/></bpmndi:BPMNEdge>
    <bpmndi:BPMNEdge id="f2_di" bpmnElement="f2"><di:waypoint x="336" y="128"/><di:waypoint x="440" y="128"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</definitions>`;

const SHIP_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="d2" targetNamespace="t">
  <process id="versand" isExecutable="true">
    <startEvent id="start"><outgoing>g1</outgoing><messageEventDefinition/></startEvent>
    <intermediateThrowEvent id="report"><incoming>g1</incoming><messageEventDefinition/></intermediateThrowEvent>
    <sequenceFlow id="g1" sourceRef="start" targetRef="report"/>
  </process>
  <bpmndi:BPMNDiagram id="D2"><bpmndi:BPMNPlane id="PL2" bpmnElement="versand">
    <bpmndi:BPMNShape id="start_di" bpmnElement="start"><dc:Bounds x="20" y="20" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="report_di" bpmnElement="report"><dc:Bounds x="160" y="20" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNEdge id="g1_di" bpmnElement="g1"><di:waypoint x="56" y="38"/><di:waypoint x="160" y="38"/></bpmndi:BPMNEdge>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</definitions>`;

const ev = (position, at, kind, from, to, message, extra = {}) =>
  ({ position, at, kind, from, to, message, count: 1, ...extra });

// The history, newest page first: the route answers each page oldest first.
const NEWEST = {
  processId: "bestellung", participants: GRAPH.participants, more: true, next: "50", latest: "90", rows: 4,
  events: [
    ev("60", "2026-11-11T10:00:00Z", "unreceived", "process:bestellung", "", "protokoll"),
    ev("70", "2026-11-11T10:00:00Z", "started", "process:bestellung", "process:versand", "freigabe", { correlationKey: "A-3" }),
    ev("80", "2026-11-11T10:00:01Z", "received", "process:versand", "process:bestellung", "versandt", { correlationKey: "A-3" }),
    ev("90", "2026-11-11T10:00:02Z", "outside", "process:bestellung", "outside-view", "freigabe", { started: 1 }),
  ],
};
const OLDER = {
  processId: "bestellung", participants: GRAPH.participants, more: false, next: "10", latest: "40", rows: 3,
  events: [
    ev("10", "2026-11-11T09:00:00Z", "received", "outside-view", "process:bestellung", "storno"),
    ev("20", "2026-11-11T09:30:00Z", "started", "process:bestellung", "process:versand", "freigabe", { correlationKey: "A-1" }),
    ev("40", "2026-11-11T09:30:01Z", "received", "process:versand", "process:bestellung", "versandt", { correlationKey: "A-1" }),
  ],
};
const NEWER = {
  processId: "bestellung", participants: GRAPH.participants, more: false, next: "95", latest: "95", rows: 1,
  events: [ev("95", "2026-11-11T10:05:00Z", "started", "process:bestellung", "process:versand", "freigabe", { correlationKey: "A-4" })],
};

function installMock(page) {
  const calls = [];
  let followed = false;
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname, q = url.searchParams;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "u1", username: "op", roles: ["operator"] } } });
    }
    if (path.endsWith("/process-interactions/replay")) {
      calls.push("replay" + url.search);
      if (q.get("before") === "50") return route.fulfill({ json: OLDER });
      if (q.get("after")) {
        if (!followed && q.get("after") === "90") { followed = true; return route.fulfill({ json: NEWER }); }
        return route.fulfill({ json: { ...NEWER, events: [], latest: q.get("after") } });
      }
      return route.fulfill({ json: NEWEST });
    }
    const xml = path.match(/\/processes\/(\d+)\/xml$/);
    if (xml) {
      calls.push("xml" + xml[1]);
      return route.fulfill({ contentType: "application/xml", body: xml[1] === "2" ? ORDER_XML : SHIP_XML });
    }
    if (path.endsWith("/process-interactions")) {
      calls.push("graph" + url.search);
      if (q.get("processId") === "versand") {
        return route.fulfill({ json: { ...GRAPH, processId: "versand", ports: [
          { direction: "in", message: "freigabe", elements: ["start"], roles: ["start"] },
          { direction: "out", message: "versandt", elements: ["report"], roles: ["throw"] }] } });
      }
      if (q.get("processId") !== "bestellung") {
        return route.fulfill({ json: { ...GRAPH, processId: q.get("processId"), ports: [] } });
      }
      return route.fulfill({ json: GRAPH });
    }
    return route.fulfill({ json: [] });
  });
  return calls;
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

const pool = (page, id) => page.locator(`.djs-shape[data-element-id="Participant_${id.replace(/[^A-Za-z0-9_-]/g, "_")}"]`);
const ctl = (page, id) => page.locator(`.ix-pool-ctl[data-id="${id}"]`);
const flowsOf = async (page) => JSON.parse(await page.locator("#ix-canvas").getAttribute("data-flows"));
const flow = async (page, from, to, message) => {
  const id = (await flowsOf(page))[`${from} | ${to} | ${message}`];
  expect(id, `${from} -> ${to} : ${message} is drawn`).toBeTruthy();
  return page.locator(`.djs-connection[data-element-id="${id}"]`);
};
const caption = async (page, from, to, message) => {
  const id = (await flowsOf(page))[`${from} | ${to} | ${message}`];
  return page.locator(`[data-element-id="${id}_label"]`);
};

test("the graph opens as a BPMN collaboration, every pool collapsed", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  await expect(pool(page, "process:bestellung")).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".djs-shape[data-element-id^='Participant_']")).toHaveCount(5);
  await expect(page.locator("#ix-title")).toHaveText("Zusammenspiel: Sekretariat");
  await expect(pool(page, "process:bestellung")).toHaveClass(/ix-center/);
  // Collapsed: no pool shows a process, and nothing was asked for one.
  await expect(page.locator(".djs-shape[data-element-id$='_release']")).toHaveCount(0);
  expect(calls.some((c) => c.startsWith("xml"))).toBe(false);
  // Senders above the process, receivers below.
  const y = async (id) => (await pool(page, id).boundingBox()).y;
  expect(await y("inbound-watch:c1")).toBeLessThan(await y("process:bestellung"));
  expect(await y("outside-view")).toBeLessThan(await y("process:bestellung"));
  expect(await y("process:versand")).toBeGreaterThan(await y("process:bestellung"));
  expect(await y("process:rechnung")).toBeGreaterThan(await y("process:bestellung"));

  // Every flow is a BPMN message flow; the layers are told apart by colour.
  await expect(await flow(page, "process:bestellung", "process:versand", "freigabe")).toHaveClass(/ix-observed/);
  await expect(await caption(page, "process:bestellung", "process:versand", "freigabe")).toContainText("freigabe · 3× · 3 gestartet");
  await expect(await flow(page, "process:bestellung", "process:rechnung", "freigabe")).toHaveClass(/ix-possible/);
  await expect(await caption(page, "process:bestellung", "process:rechnung", "freigabe")).toContainText("freigabe · möglich");
  await expect(await flow(page, "outside-view", "process:bestellung", "storno")).toHaveClass(/ix-not-possible/);
  const kinds = await page.locator(".djs-connection").evaluateAll((els) => els.map((e) => e.getAttribute("data-element-id")));
  expect(kinds.every((id) => id.startsWith("MessageFlow_"))).toBe(true);

  // What the reader may not see has no name; a black box has no expander.
  await expect(pool(page, "outside-view")).toContainText("Ausserhalb deiner Sicht");
  await expect(pool(page, "outside-view")).toHaveClass(/ix-outside/);
  await expect(ctl(page, "inbound-watch:c1").locator(".ix-pool-toggle")).toHaveCount(0);
  await expect(ctl(page, "outside-view")).toContainText("Für dich nicht lesbar");
  await expect(page.locator("#ix-notes")).toContainText("Ohne Empfänger");
  await expect(page.locator("#ix-notes")).toContainText("protokoll: 2×");

  // The window defaults to a day and travels to both routes.
  expect(calls[0]).toContain("window=24h");
  expect(calls.find((c) => c.startsWith("replay"))).toContain("window=24h");
  expect(page.__errors).toEqual([]);
});

test("a process expands to its deployed diagram, and its flows dock on its elements", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  await expect(pool(page, "process:bestellung")).toBeVisible({ timeout: 15000 });

  await ctl(page, "process:bestellung").locator(".ix-pool-toggle").click();
  await expect(page.locator(".djs-shape[data-element-id$='_release']")).toHaveCount(1);
  await expect(ctl(page, "process:bestellung").locator(".ix-pool-toggle")).toHaveText("−");
  expect(calls).toContain("xml2");
  // The release flow now leaves the throw event, not the pool's edge: its drawing
  // starts at the event's lower edge and runs through the event's middle.
  const throwBox = await page.locator(".djs-shape[data-element-id$='_release'] .djs-visual").boundingBox();
  const releaseBox = await (await flow(page, "process:bestellung", "process:rechnung", "freigabe")).locator(".djs-visual").boundingBox();
  expect(Math.abs(releaseBox.y - (throwBox.y + throwBox.height))).toBeLessThan(6);
  expect(Math.abs(releaseBox.x + releaseBox.width / 2 - (throwBox.x + throwBox.width / 2))).toBeLessThan(6);

  // A neighbour expands too; both use the element id "start", and both are drawn.
  await ctl(page, "process:versand").locator(".ix-pool-toggle").click();
  await expect(page.locator(".djs-shape[data-element-id$='_report']")).toHaveCount(1);
  await expect(page.locator(".djs-shape[data-element-id$='_start']")).toHaveCount(2);
  expect(calls).toContain("xml10");
  expect(calls.some((c) => c.includes("processId=versand") && c.includes("window=1ns"))).toBe(true);

  await page.locator("#ix-collapse-all").click();
  await expect(page.locator(".djs-shape[data-element-id$='_start']")).toHaveCount(0);
  await page.locator("#ix-expand-all").click();
  await expect(page.locator(".djs-shape[data-element-id$='_report']")).toHaveCount(1);

  await ctl(page, "process:versand").locator(".ix-pool-open").click();
  await expect(page).toHaveURL(/#\/operations\/interactions\/versand$/);
  expect(page.__errors).toEqual([]);
});

test("the replay reads its history until it begins, and plays one by one or bundled", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  // Two pages, the older read before the first page's cursor.
  await expect(page.locator("#ix-now")).toContainText("Ereignis 0 von 7", { timeout: 15000 });
  expect(calls.filter((c) => c.startsWith("replay")).some((c) => c.includes("before=50"))).toBe(true);
  await expect(page.locator("#ix-history")).toContainText("vollständig");
  await expect(page.locator("#ix-history")).toContainText("30 Tage");

  await page.locator("#ix-step").click();
  await expect(page.locator("#ix-feed li")).toHaveCount(1);
  await expect(page.locator("#ix-feed li").first()).toContainText("storno");
  await expect(page.locator("#ix-feed li").first()).toContainText("Ausserhalb deiner Sicht → Sekretariat");
  await expect(page.locator("#ix-mode")).toContainText("einzeln");
  await expect(page.locator("#ix-now")).toContainText("Ereignis 1 von 7");

  await page.locator("#ix-speed").selectOption("5");
  await page.locator("#ix-step").click();
  await expect(page.locator("#ix-mode")).toContainText("gebündelt");
  await expect(page.locator("#ix-now")).toContainText("Ereignis 6 von 7");
  await expect(page.locator("#ix-feed li.kind-unreceived")).toContainText("niemand hat sie empfangen");
  await expect(page.locator("#ix-feed li").first()).toContainText("A-3");

  await page.locator("#ix-speed").selectOption("1");
  await page.locator("#ix-step").click();
  await expect(page.locator("#ix-feed li").first()).toContainText("1× ausserhalb deiner Sicht empfangen");

  await page.locator("#ix-rewind").click();
  await expect(page.locator("#ix-now")).toContainText("Ereignis 0 von 7");
  await page.locator("#ix-play").click();
  await expect(page.locator("#ix-play")).toHaveText("Anhalten");
  await expect(page.locator("#ix-now")).toContainText("Ereignis 2 von 7", { timeout: 5000 });
  await page.locator("#ix-play").click();
  await expect(page.locator("#ix-play")).toHaveText("Abspielen");
  expect(page.__errors).toEqual([]);
});

test("Live appends what came after the newest event", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/interactions/bestellung");
  await expect(page.locator("#ix-now")).toContainText("von 7", { timeout: 15000 });
  await page.locator("#ix-live").check();
  await expect(page.locator("#ix-now")).toContainText("Ereignis 8 von 8", { timeout: 10000 });
  await expect(page.locator("#ix-feed li").first()).toContainText("A-4");
  expect(calls.some((c) => c.startsWith("replay") && c.includes("after=90"))).toBe(true);
  expect(page.__errors).toEqual([]);
});
