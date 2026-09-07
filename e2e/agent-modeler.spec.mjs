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
  await openSection(page, page.locator(".pgroup-head", { hasText: title }).first(), ".pgroup");
}

// expandGroup opens one of the mapping-style groups (Parameters), which carry their own
// head markup rather than the <h3> one.
async function expandGroup(page, title) {
  await openSection(page, page.locator(".io-group-head", { hasText: title }).first(), ".io-group");
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
  await expect(page.locator("#p-body")).toContainText("starts at once");
  await open(page, "agent");
  await expect(page.locator("#p-body")).toContainText("Entry starts nothing");
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
  await expect(page.locator("#p-body")).toContainText("Agent tool");
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
  await expect(page.locator(".warn-note")).toContainText("no documentation");
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
  await expect(page.locator("#p-body")).toContainText("not a tool the agent can call");
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
  await expect(page.locator("#p-body")).toContainText("AI Task");
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
