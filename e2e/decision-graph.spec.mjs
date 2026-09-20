// e2e for the decision graph window (api/web/decision-graph.js).
//
// Double-clicking a business rule task in Operations opens the decision behind it:
// its requirements graph with the case drawn on it, the rules that fired, and the
// answer. The window exists to be turned towards somebody from the business, so
// what these tests assert is not that it rendered — it is that what it says is
// true. A value on the wrong node, or a silence that reads like "no rules matched"
// when it means "no rules were recorded", is the failure mode that matters.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/decision-graph-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// nodeText returns what one node of the graph says, found by the name drawn in it.
const nodeText = (page, name) =>
  page.locator(".drg-canvas g", { has: page.locator(`text="${name}"`) }).first();

test("an evaluation is addressed by the server's exact key, not by a rounded number", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  const calls = await page.evaluate(() => window.__apiCalls);
  expect(calls).toHaveLength(1);
  // A nanosecond timestamp is past 2^53. Round-tripping it through a JavaScript
  // number turns …033700 into …033800, which addresses an evaluation that does not
  // exist — so the path must carry the digits the server served, unchanged.
  expect(calls[0].path).toBe("/api/v1/instances/281474999937230/decisions/1789824241612033700/graph");
  expect(calls[0].path).not.toContain("1789824241612033800");
});

test("the graph carries the case: what went in, what came of it, and the answer", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");

  // Every input datum the task handed over shows the value it handed over.
  await expect(nodeText(page, "betrag")).toContainText("60000");
  await expect(nodeText(page, "laufzeitMonate")).toContainText("48");
  await expect(nodeText(page, "einkommen")).toContainText("5000");
  // An intermediate decision shows what it worked out — read off the trace, which
  // records the value the table above it tested.
  await expect(nodeText(page, "Tragbarkeit")).toContainText("0.25");
  // And the decision that answered shows the answer, marked as the answer.
  const answer = nodeText(page, "Kreditentscheid");
  await expect(answer).toContainText("abgelehnt");
  await expect(answer).toHaveClass(/is-result/);
});

test("a decision the caller supplied is drawn as given, not as computed", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  // bonitaet arrived in the input context rather than being worked out, which is
  // what a decision service's boundary looks like from outside. The picture has to
  // say so, or the legend beside it is a lie — and "who decided the rating" is
  // exactly the question somebody accounting for a rejection will ask.
  const given = await nodeText(page, "Bonität").locator("rect").getAttribute("fill");
  const computed = await nodeText(page, "Tragbarkeit").locator("rect").getAttribute("fill");
  expect(given).toBe("#eff6ff");
  expect(computed).not.toBe(given);
  await expect(nodeText(page, "Bonität").locator("title"))
    .toHaveText(/supplied, not computed/);
});

test("a node the case never touched says so rather than showing nothing", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  const idle = nodeText(page, "zahlungsstoerungen");
  await expect(idle).toHaveClass(/is-idle/);
  await expect(idle).toContainText("not part of this case");
});

test("the rule that carried the answer is shown, with why the ones above it did not", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-rules .mgrid");
  await expect(page.locator(".drg-rules .mtable-head")).toContainText("Rule 6 fired");

  // The rule that fired, and the output it carried.
  const fired = page.locator(".drg-rules tr.mrule.is-hit");
  await expect(fired).toHaveCount(1);
  await expect(fired).toContainText("abgelehnt");

  // And the near miss: rule 5 matched the rating and failed on the ratio. That pair
  // — one condition green, the next red — is the whole explanation of the rejection,
  // so it is asserted rather than left to the eye.
  const nearMiss = page.locator(".drg-rules tr.mrule").nth(4);
  await expect(nearMiss.locator("td.mcell.is-ok")).toContainText('"C"');
  await expect(nearMiss.locator("td.mcell.is-no")).toContainText("<= 0.15");
});

test("a decision service says why there is no rule matrix instead of showing none", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__atService));
  await page.waitForSelector(".drg-canvas svg");

  await expect(page.locator(".drg-tag")).toHaveText(/decision service/i);
  // The distinction the window turns on: the values are exact, and only the
  // rule-level detail is missing. Reading this as "nothing matched" would be a
  // different, and false, account of the case.
  const rules = page.locator(".drg-rules");
  await expect(rules).toContainText("records no rule-by-rule trace");
  await expect(rules).toContainText("exactly what the case carried");
  await expect(page.locator(".drg-rules .mgrid")).toHaveCount(0);

  // The graph still carries the case, which is the point of saying it that way.
  await expect(nodeText(page, "betrag")).toContainText("60000");
  await expect(nodeText(page, "Kreditentscheid")).toContainText("abgelehnt");
  // With no trace there is nothing that can speak for the encapsulated decision, so
  // it is left blank rather than guessed at.
  await expect(nodeText(page, "Tragbarkeit")).toContainText("not part of this case");
});

test("a decision whose model is gone says so rather than drawing an empty frame", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__atGone));
  await page.waitForSelector(".drg-canvas");
  await expect(page.locator(".drg-canvas")).toContainText("no graph to draw");
  // The record itself still stands, and still answers the first question asked of it.
  await expect(page.locator(".drg-result")).toContainText("abgelehnt");
});

test("an evaluation that cannot be loaded reports the reason in the window", async ({ page }) => {
  await page.evaluate(() => window.__open("1789824241612039999"));
  await expect(page.locator(".drg-body")).toContainText("no decision evaluation recorded at that time");
});

test("the window closes by Escape, by the backdrop and by its button, and gives focus back", async ({ page }) => {
  for (const close of [
    async () => page.keyboard.press("Escape"),
    async () => page.locator(".drg-x").click(),
    async () => page.locator(".drg-ov").click({ position: { x: 5, y: 5 } }),
  ]) {
    await page.locator("#opener").focus();
    await page.evaluate(() => window.__open(window.__at));
    await page.waitForSelector(".drg-canvas svg");
    await close();
    await expect(page.locator(".drg-ov")).toHaveCount(0);
    // Focus goes back where the gesture came from, so a keyboard reader is not
    // dropped at the top of the page each time they look at a decision.
    expect(await page.evaluate(() => document.activeElement && document.activeElement.id)).toBe("opener");
  }
});

test("opening a second decision replaces the first rather than stacking windows", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  await page.evaluate(() => window.__open(window.__atService));
  await page.waitForSelector(".drg-tag");
  await expect(page.locator(".drg-ov")).toHaveCount(1);
  await expect(page.locator("#drg-title")).toHaveText("Kreditfreigabe");
});

test("navigating away takes the window with it", async ({ page }) => {
  await page.evaluate(() => window.__open(window.__at));
  await page.waitForSelector(".drg-canvas svg");
  // It is appended to the body, so nothing else removes it — and a fixed overlay left
  // standing over the next screen is the worst kind of bug to reproduce.
  await page.evaluate(() => { location.hash = "#/operations/incidents"; });
  await expect(page.locator(".drg-ov")).toHaveCount(0);
});

test("the plain renderer the Modeler shares still draws a model with no case on it", async ({ page }) => {
  // app.js imports this same function for the read-only DMN view, so it has to keep
  // drawing a graph that carries no evaluation at all.
  const svg = await page.evaluate(() => window.__plain());
  expect(svg).toContain("<svg");
  expect(svg).toContain("Kreditentscheid");
  expect(svg).toContain("decision table");
  expect(svg).not.toContain("not part of this case");
});

// The DRD notation is not styling: the shape is how a reader tells one kind of node
// from another (DMN 1.5 §5.3.3, Table 5-2). Drawing a decision with rounded corners
// makes it read as an input datum or a decision service, which are the two things it
// is not.
test("each kind of node is drawn as the DRD notation draws it", async ({ page }) => {
  const svg = await page.evaluate(() => window.__plainWithBkm());

  // a decision: a plain rectangle, square corners
  const decision = svg.match(/<rect x="210" y="100"[^>]*>/);
  expect(decision, "the decision is drawn").not.toBeNull();
  expect(decision[0]).not.toContain("rx=");

  // input data: a stadium — a rectangle with fully rounded ends
  const input = svg.match(/<rect x="60" y="540"[^>]*>/);
  expect(input, "the input datum is drawn").not.toBeNull();
  expect(input[0]).toContain('rx="22.5"');

  // a business knowledge model: a rectangle with two corners cut off, which a
  // rounded rectangle cannot express
  const bkm = svg.match(/<polygon points="[^"]*"[^>]*>/);
  expect(bkm, "the knowledge model is drawn as a polygon").not.toBeNull();
  expect(bkm[0].match(/points="([^"]*)"/)[1].split(" ")).toHaveLength(4);
});
