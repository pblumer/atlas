// End-to-end coverage for the agent fields in the Modeler panel (ADR-0253 phase 4).
//
// The record accepted a second entry semantics on one element — an ad-hoc subprocess
// either starts every unconnected activity at once (ADR-0143) or lets an agent pick,
// round by round — and named the cost: "a model reader must know which one is in force,
// and the Modeler has to make that visible". These tests are that condition, checked.
//
// They assert two things at once, because either alone would be misleading. The exported
// XML, because that is what the deploy compiles. And the *prose*, because a panel that
// wrote the right attribute while telling the reader the opposite would be worse than
// one that did neither.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/agent-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await expect(page.locator("#p-body")).toBeVisible();
});

// open selects an element on the Implement tab, where every field below lives. The tab
// is chosen once in beforeEach and stays chosen, so this only has to select.
async function open(page, id) {
  await page.evaluate((el) => window.__select(el), id);
}

// expand opens a collapsed property group by its title. Every group but General starts
// collapsed, which is what an author sees — so a test that drives a field has to open
// its section first, exactly as a person would.
async function expand(page, title) {
  await openSection(page, page.locator(`.pgroup[data-group*="${title}" i] > .pgroup-head`).first(), ".pgroup");
}

// expandGroup opens one of the mapping-style groups (Parameters), which carry their own
// head markup rather than the <h3> one.
async function expandGroup(page, title) {
  await openSection(page, page.locator(`.io-group[data-group*="${title}" i] .io-group-head`).first(), ".io-group");
}

// openSection clicks a section head only when its section is collapsed. The head is a
// toggle and the panel remembers the author's choice across re-renders, so clicking
// unconditionally would close a section a previous step had already opened.
async function openSection(page, head, groupSel) {
  const collapsed = await head.evaluate(
    (el, sel) => el.closest(sel).classList.contains("collapsed"), groupSel);
  if (collapsed) await head.click();
}

async function elementXML(page, tag, id) {
  const xml = await page.evaluate(() => window.__xml());
  const m = new RegExp(`<bpmn:${tag} id="${id}"[\\s\\S]*?</bpmn:${tag}>`).exec(xml);
  return m ? m[0] : "";
}

test("an agent-driven ad-hoc shows its agent, and a plain one does not", async ({ page }) => {
  await open(page, "agent");
  await expect(page.locator("#f-adhocmode")).toHaveValue("agent");
  await expect(page.locator("#f-agent-connector")).toHaveValue("anthropic_pb");
  await expect(page.locator("#f-agent-resultcoll")).toHaveValue("toolCallResults");
  // The '=' prefix is the storage form, not the editing form — as everywhere else.
  await expect(page.locator("#f-agent-resultelem")).toHaveValue("toolCallResult");

  // The suggestions are the agent Workers this server has, and only those.
  const options = await page.locator("#dl-agent-connector option").evaluateAll(
    (els) => els.map((e) => e.value));
  expect(options).toEqual(["anthropic_pb", "openai_pb"]);

  await open(page, "plain");
  await expect(page.locator("#f-adhocmode")).toHaveValue("all");
  await expect(page.locator("#f-agent-connector")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// The visibility the record asked for: the two semantics do not look alike. One says
// everything starts at once, the other says entry starts nothing.
test("the panel says which entry semantics is in force", async ({ page }) => {
  await open(page, "plain");
  await expect(page.locator("#p-body")).toContainText("startet sofort");
  await open(page, "agent");
  await expect(page.locator("#p-body")).toContainText("Der Eintritt startet nichts");
});

test("switching a plain ad-hoc to an agent writes the connector, and back removes it", async ({ page }) => {
  await open(page, "plain");
  await expand(page, "Ad-hoc subprocess");
  await page.locator("#f-adhocmode").selectOption("agent");
  await expect(page.locator("#f-agent-connector")).toBeVisible();
  await page.locator("#f-agent-connector").fill("openai_pb");
  await page.locator("#f-agent-connector").blur();

  let xml = await elementXML(page, "adHocSubProcess", "plain");
  expect(xml).toContain(`connector="openai_pb"`);
  expect(xml).toMatch(/agentConnector/);

  // And back: the extension goes entirely, so the subprocess is a plain one again
  // rather than one carrying an inert attribute.
  await expand(page, "Ad-hoc subprocess");
  await page.locator("#f-adhocmode").selectOption("all");
  await expect(page.locator("#f-agent-connector")).toHaveCount(0);
  xml = await elementXML(page, "adHocSubProcess", "plain");
  expect(xml).not.toMatch(/agentConnector/);
  expect(page.__errors).toEqual([]);
});

test("the result collection and its FEEL element are stored the way the compiler reads them", async ({ page }) => {
  await open(page, "agent");
  await expand(page, "Tool results");
  await page.locator("#f-agent-resultcoll").fill("ergebnisse");
  await page.locator("#f-agent-resultcoll").blur();
  await page.locator("#f-agent-resultelem").fill("antwort");
  await page.locator("#f-agent-resultelem").blur();

  const xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).toContain(`resultCollection="ergebnisse"`);
  // Stored '=' prefixed, like every other FEEL attribute in the model.
  expect(xml).toMatch(/resultElement="=\s*antwort"/);
  // The three are written together, so editing one must not drop the others.
  expect(xml).toContain(`connector="anthropic_pb"`);
  expect(page.__errors).toEqual([]);
});

test("a tool shows what it is to the agent, and its declared parameters", async ({ page }) => {
  await open(page, "zinsen_holen");
  await expect(page.locator('.pgroup[data-group="Agent tool"]')).toHaveCount(1);
  await expect(page.locator("#p-body")).toContainText("agent");

  await expandGroup(page, "Parameters");
  await expect(page.locator("#agent-params .ap-name")).toHaveValue("url");
  await expect(page.locator("#agent-params .ap-type")).toHaveValue("string");
  await expect(page.locator("#agent-params .ap-desc")).toHaveValue("Die Zinsseite");
  await expect(page.locator("#agent-params .ap-required")).toBeChecked();
  expect(page.__errors).toEqual([]);
});

test("an added parameter lands in the XML, and a deleted one leaves", async ({ page }) => {
  await open(page, "historie_lesen");
  await expandGroup(page, "Parameters");
  await page.locator("[data-agent-param-group] .io-group-add").click();
  await page.locator("#agent-params .ap-name").fill("limit");
  await page.locator("#agent-params .ap-name").blur();
  await page.locator("#agent-params .ap-type").selectOption("number");
  await page.locator("#agent-params .ap-required").check();

  let xml = await elementXML(page, "serviceTask", "historie_lesen");
  expect(xml).toMatch(/agentParam[^>]*name="limit"/);
  expect(xml).toMatch(/agentParam[^>]*type="number"/);
  expect(xml).toMatch(/agentParam[^>]*required="true"/);
  // The task's own job type is untouched: parameters are added beside it, not over it.
  expect(xml).toContain(`type="history"`);

  await page.locator("#agent-params .io-map-del").click();
  xml = await elementXML(page, "serviceTask", "historie_lesen");
  expect(xml).not.toMatch(/agentParam/);
  expect(xml).toContain(`type="history"`);
  expect(page.__errors).toEqual([]);
});

// The compiler warns about an undocumented tool at deploy (agent.tool). Saying it here
// too is the difference between finding out while writing the tool and finding out after
// pressing Deploy.
test("an undocumented tool is warned about where it is written", async ({ page }) => {
  await open(page, "historie_lesen");
  await expect(page.locator(".warn-note")).toContainText("keine Dokumentation");
  await expect(page.locator(".warn-note")).toContainText("agent.tool");

  // A documented one is not nagged.
  await open(page, "zinsen_holen");
  await expect(page.locator(".warn-note")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// Only the activities no sequence flow leads to are tools. Saying nothing on a chained
// activity would read as "this has nothing to do with the agent", which is the wrong
// half of the truth: it runs as a later step of the tool that starts its chain.
test("a chained inner activity is told it is not a tool", async ({ page }) => {
  await open(page, "pruefen");
  await expect(page.locator("#p-body")).toContainText("kein Werkzeug, das der Agent aufrufen kann");
  await expect(page.locator("[data-agent-param-group]")).toHaveCount(0);

  // And an activity outside any agent container gets none of this at all.
  await open(page, "frei");
  await expect(page.locator("#p-body")).not.toContainText("Agent tool");
  expect(page.__errors).toEqual([]);
});

// --- The ai task (ADR-0256) --------------------------------------------------
//
// <atlas:agentConnector> has two hosts now, and they are different panels on different
// element types. The tests below are as much about the seam as about the fields: a
// service task must not show the container's toolbox settings, and a container must not
// show the task's prompt — an element that means two things has to say which one it is.

test("an ai task shows its worker, model, prompt and result variable", async ({ page }) => {
  await open(page, "einordnen");
  await expect(page.locator("#f-st-connector")).toHaveValue("anthropic_pb");
  await expect(page.locator("#f-st-model")).toHaveValue("claude-haiku-4-5");
  await expect(page.locator("#f-st-resultVariable")).toHaveValue("kategorie");
  // A service task's literal-or-FEEL field keeps its '=' in the editor — that is this
  // panel's convention, unlike the ad-hoc's resultElement above, which strips it.
  await expect(page.locator("#f-st-prompt")).toHaveValue(`="Klassifiziere: " + antrag`);

  // The picker recognised it as an ai task rather than falling back to "Job worker",
  // which is the failure a missing catalog entry produces.
  await expect(page.locator("#p-body")).toContainText("KI-Task");
  expect(page.__errors).toEqual([]);
});

test("an ai task offers no toolbox, and a container offers no prompt", async ({ page }) => {
  await open(page, "einordnen");
  // resultCollection/resultElement belong to the container; the compiler refuses them on
  // a task, so the panel must not offer a way to write them.
  await expect(page.locator("#f-agent-resultcoll")).toHaveCount(0);
  await expect(page.locator("#f-agent-resultelem")).toHaveCount(0);
  await expect(page.locator("#p-body")).not.toContainText("Agent tool");

  await open(page, "agent");
  // And the other way: prompt/resultVariable are the task's, and the compiler refuses
  // them on a container.
  await expect(page.locator("#f-st-prompt")).toHaveCount(0);
  await expect(page.locator("#f-st-resultVariable")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("editing an ai task writes what the compiler reads", async ({ page }) => {
  await open(page, "einordnen");
  await expand(page, "AI worker");
  await page.locator("#f-st-model").fill("claude-opus-5");
  await page.locator("#f-st-model").blur();
  await expand(page, "Output");
  await page.locator("#f-st-resultVariable").fill("einstufung");
  await page.locator("#f-st-resultVariable").blur();

  const xml = await elementXML(page, "serviceTask", "einordnen");
  expect(xml).toContain(`model="claude-opus-5"`);
  expect(xml).toContain(`resultVariable="einstufung"`);
  // The fields are written together, so editing one must not drop the others.
  expect(xml).toContain(`connector="anthropic_pb"`);
  expect(xml).toMatch(/prompt="[^"]*Klassifiziere/);
  expect(page.__errors).toEqual([]);
});

// The Worker's model became a *default* under ADR-0256, so a container names its own the
// same way a task does — and naming none has to leave the attribute out, because an empty
// attribute would be a second spelling of "the Worker's model".
test("an agent container names its model, and clearing it removes the attribute", async ({ page }) => {
  await open(page, "agent");
  await expand(page, "Agent");
  await expect(page.locator("#f-agent-model")).toHaveValue("claude-opus-5");

  await page.locator("#f-agent-model").fill("claude-haiku-4-5");
  await page.locator("#f-agent-model").blur();
  let xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).toContain(`model="claude-haiku-4-5"`);
  expect(xml).toContain(`connector="anthropic_pb"`);

  await expand(page, "Agent");
  await page.locator("#f-agent-model").fill("");
  await page.locator("#f-agent-model").blur();
  xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).not.toMatch(/model="/);
  expect(xml).toContain(`connector="anthropic_pb"`);
  expect(page.__errors).toEqual([]);
});

// The canvas mark ADR-0256 left open, and the reason it was worth closing: two ad-hoc
// subprocesses look identical and behave oppositely — one starts every unconnected
// activity at once, the other starts nothing and lets a model pick. A reader had to open
// the panel to find out which. On the Implement tab the agent-driven one now carries the
// Worker Type badge every implemented element carries, and the plain one carries none.
test("an agent-driven ad-hoc is marked on the canvas, and a plain one is not", async ({ page }) => {
  const badgeOf = (id) => page.locator(`[data-container-id="${id}"] .impl-badge, .djs-overlay[data-container-id="${id}"] .impl-badge`);

  await expect(badgeOf("agent")).toHaveCount(1);
  await expect(badgeOf("agent")).toHaveAttribute("title", /agent/i);
  await expect(badgeOf("plain")).toHaveCount(0);

  // And the mark follows the semantics rather than the element: switching the plain one
  // over gives it the badge.
  await open(page, "plain");
  await expand(page, "Ad-hoc subprocess");
  await page.locator("#f-adhocmode").selectOption("agent");
  await page.locator("#f-agent-connector").fill("openai_pb");
  await page.locator("#f-agent-connector").blur();
  await expect(badgeOf("plain")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

// What an agent may read is authored on the element too, beside what it may reach
// (ADR-0257). A reviewer sees both in one place, which is the
// whole argument — an agent whose data comes from somewhere the diagram does not show is
// only half reviewable.
test("an agent container names what it may read, and clearing it removes the attribute", async ({ page }) => {
  await open(page, "agent");
  await expand(page, "Agent");
  await expect(page.locator("#f-agent-context")).toHaveValue("antrag,kunde");

  await page.locator("#f-agent-context").fill("antrag, kunde, dossier");
  await page.locator("#f-agent-context").blur();
  let xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).toContain(`context="antrag, kunde, dossier"`);
  // Written together with the rest, so editing one field never drops another.
  expect(xml).toContain(`connector="anthropic_pb"`);
  expect(xml).toContain(`model="claude-opus-5"`);

  await expand(page, "Agent");
  await page.locator("#f-agent-context").fill("");
  await page.locator("#f-agent-context").blur();
  xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).not.toMatch(/context="/);
  expect(xml).toContain(`connector="anthropic_pb"`);
  expect(page.__errors).toEqual([]);
});

// The task must not offer it: the compiler refuses context on an ai task, so a field here
// would be a way to author a model that does not deploy.
test("an ai task offers no context list", async ({ page }) => {
  await open(page, "einordnen");
  await expect(page.locator("#f-agent-context")).toHaveCount(0);
  await expect(page.locator("#f-st-context")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// --- Bounds and answer fields (ADR-0566, -transcript-and-answer) ---
//
// The first test is the one that matters most and drives no field at all. bpmn-js drops
// an attribute or an extension element its moddle has no type for, so a model that
// carries a bound or an answer field would lose it the first time somebody opened it in
// the Modeler and pressed Save — silently, and the deploy that followed would run an
// agent with no bound. What is checked is that the model comes back out as it went in.
test("a model's bounds and answer fields survive being opened and saved", async ({ page }) => {
  const xml = await elementXML(page, "adHocSubProcess", "agent");
  for (const attr of [`maxIterations="6"`, `tokenBudget="40000"`, `timeout="PT90S"`, `maxContextTokens="30000"`]) {
    expect(xml).toContain(attr);
  }
  expect(xml).toMatch(/agentOutput[^>]*name="entscheid"/);
  expect(xml).toMatch(/agentOutput[^>]*values="ja,nein"/);
  expect(xml).toMatch(/agentOutput[^>]*required="true"/);
  expect(page.__errors).toEqual([]);
});

test("a container shows its bounds, and editing one keeps the others", async ({ page }) => {
  await open(page, "agent");
  await expand(page, "Agent limits");
  await expect(page.locator("#f-agent-maxiter")).toHaveValue("6");
  await expect(page.locator("#f-agent-budget")).toHaveValue("40000");
  await expect(page.locator("#f-agent-timeout")).toHaveValue("PT90S");
  await expect(page.locator("#f-agent-maxctx")).toHaveValue("30000");
  await expect(page.locator("#p-body")).toContainText("gewährt dieselbe Menge noch einmal");

  await page.locator("#f-agent-maxiter").fill("12");
  await page.locator("#f-agent-maxiter").blur();
  let xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).toContain(`maxIterations="12"`);
  expect(xml).toContain(`tokenBudget="40000"`);
  expect(xml).toContain(`timeout="PT90S"`);
  expect(xml).toContain(`connector="anthropic_pb"`);

  // A bound cleared is written as absent, which the compiler reads as "not stated" —
  // an empty attribute would be a second way to say it.
  await expand(page, "Agent limits");
  await page.locator("#f-agent-budget").fill("");
  await page.locator("#f-agent-budget").blur();
  xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).not.toContain("tokenBudget");
  expect(xml).toContain(`maxIterations="12"`);
  expect(page.__errors).toEqual([]);
});

test("a container's answer fields are shown, and an added one lands in the XML", async ({ page }) => {
  await open(page, "agent");
  await expandGroup(page, "Answer fields");
  const cards = page.locator("#agent-outputs [data-agent-output]");
  await expect(cards).toHaveCount(1);
  await expect(cards.first().locator(".ao-name")).toHaveValue("entscheid");
  await expect(cards.first().locator(".ao-type")).toHaveValue("string");
  await expect(cards.first().locator(".ao-values")).toHaveValue("ja,nein");
  await expect(cards.first().locator(".ao-required")).toBeChecked();
  // Scalars only: an object declared by name alone says nothing about what is inside.
  const types = await cards.first().locator(".ao-type option").evaluateAll((els) => els.map((e) => e.value));
  expect(types).toEqual(["string", "number", "boolean"]);

  await page.locator("[data-agent-output-group] .io-group-add").click();
  const added = page.locator("#agent-outputs [data-agent-output]").nth(1);
  await added.locator(".ao-name").fill("quote");
  await added.locator(".ao-name").blur();
  await page.locator("#agent-outputs [data-agent-output]").nth(1).locator(".ao-type").selectOption("number");

  const xml = await elementXML(page, "adHocSubProcess", "agent");
  expect(xml).toMatch(/agentOutput[^>]*name="entscheid"/);
  expect(xml).toMatch(/agentOutput[^>]*name="quote"[^>]*type="number"/);
  // The tools' own declarations are untouched: an answer field is not a parameter.
  expect(xml).toMatch(/agentParam[^>]*name="url"/);
  expect(page.__errors).toEqual([]);
});

test("an ai task gets a timeout and answer fields of its own", async ({ page }) => {
  await open(page, "einordnen");
  await expand(page, "Limits");
  await page.locator("#f-st-timeout").fill("PT20S");
  await page.locator("#f-st-timeout").blur();

  await expandGroup(page, "Answer fields");
  await expect(page.locator("#agent-outputs [data-agent-output]")).toHaveCount(0);
  await page.locator("[data-agent-output-group] .io-group-add").click();
  const card = page.locator("#agent-outputs [data-agent-output]").first();
  await card.locator(".ao-name").fill("kategorie");
  await card.locator(".ao-name").blur();
  await page.locator("#agent-outputs [data-agent-output]").first().locator(".ao-values").fill("Neubau, Sanierung");
  await page.locator("#agent-outputs [data-agent-output]").first().locator(".ao-values").blur();
  await page.locator("#agent-outputs [data-agent-output]").first().locator(".ao-required").check();

  let xml = await elementXML(page, "serviceTask", "einordnen");
  expect(xml).toContain(`timeout="PT20S"`);
  expect(xml).toMatch(/agentOutput[^>]*name="kategorie"/);
  expect(xml).toMatch(/agentOutput[^>]*values="Neubau,Sanierung"/);
  expect(xml).toMatch(/agentOutput[^>]*required="true"/);
  // The task itself is what it was.
  expect(xml).toContain(`resultVariable="kategorie"`);

  await page.locator("#agent-outputs .io-map-del").click();
  xml = await elementXML(page, "serviceTask", "einordnen");
  expect(xml).not.toMatch(/agentOutput/);

  // A task that asks no model is offered none of this.
  await open(page, "zinsen_holen");
  await expect(page.locator("[data-agent-output-group]")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// --- An MCP task as an agent's tool (ADR-0568) -------------------------
//
// An MCP server's tool reaches an agent the way everything else does: as an activity in
// the container, named by its element id and described by the modeller. These tests are
// the Modeler's half of that — the task survives being opened and saved, its fields are
// the ones the compiler reads, and inside the container it is a tool with parameters.
test("an MCP task survives being opened and saved, and is a tool of its container", async ({ page }) => {
  let xml = await elementXML(page, "serviceTask", "fehler_suchen");
  for (const attr of [`connector="monitoring"`, `tool="search_issues"`, `resultVariable="toolCallResult"`, `timeout="PT30S"`]) {
    expect(xml).toContain(attr);
  }
  expect(xml).toMatch(/mcpConnector[^>]*input="\{&#34;query&#34;:&#34;=dienst&#34;\}"|mcpConnector[^>]*input="\{&quot;query&quot;:&quot;=dienst&quot;\}"/);

  await open(page, "fehler_suchen");
  // Recognised as an MCP task rather than as a job worker with an unknown extension.
  await expect(page.locator("#p-body")).toContainText("MCP tool");
  await expect(page.locator("#f-st-connector")).toHaveValue("monitoring");
  await expect(page.locator("#f-st-tool")).toHaveValue("search_issues");
  await expect(page.locator("#f-st-resultVariable")).toHaveValue("toolCallResult");
  // The Worker field suggests the MCP Workers this server has, and no other kind's.
  const options = await page.locator("#f-st-connector").evaluate((el) =>
    [...(el.list ? el.list.options : [])].map((o) => o.value));
  expect(options).toEqual(["monitoring"]);
  // In the container it is one of the agent's tools, with the parameter it declares.
  await expect(page.locator('.pgroup[data-group="Agent tool"]')).toHaveCount(1);
  await expandGroup(page, "Parameters");
  await expect(page.locator("#agent-params .ap-name")).toHaveValue("dienst");
  expect(page.__errors).toEqual([]);
});

test("editing an MCP task writes what the compiler reads", async ({ page }) => {
  await open(page, "fehler_suchen");
  await expand(page, "Tool");
  await page.locator("#f-st-tool").fill("list_alerts");
  await page.locator("#f-st-tool").blur();
  const xml = await elementXML(page, "serviceTask", "fehler_suchen");
  expect(xml).toContain(`tool="list_alerts"`);
  // The other fields and the tool's own parameter are what they were.
  expect(xml).toContain(`connector="monitoring"`);
  expect(xml).toMatch(/agentParam[^>]*name="dienst"/);
  expect(page.__errors).toEqual([]);
});

