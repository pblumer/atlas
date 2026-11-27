// End-to-end coverage for the replay's Agent tab (api/web/editor.js,
// ADR-0566): what an instance's agents asked a model, what that cost
// and what came back.
//
// The record exists so that "what did the agent do in this instance" has an answer an
// operator can read. These tests are that answer, checked where it is read: the sums per
// agent, each call with its model and its tokens, the result answering the call that
// asked for it, a bound that was reached, and — the case that must not look like a model
// that said nothing — texts that are no longer there.
import { test, expect } from "@playwright/test";

async function mount(page, query = "") {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/agent-runs-harness.html" + query);
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator("#rp-tabs button[data-tab='agent']").click();
  await expect(page.locator("#tab-agent")).toBeVisible();
}

test("an agent's calls are listed with what they add up to", async ({ page }) => {
  await mount(page);
  const tab = page.locator("#tab-agent");
  await expect(tab.locator(".vp-title")).toHaveText("Modellaufrufe");
  // Two calls to a model; what a tool returned is not a call.
  await expect(tab.locator(".vp-count")).toHaveText("2 Aufrufe");
  // The agent is named as the rest of the replay names an element, with its run's sums
  // as the last call states them.
  await expect(tab.locator(".dec-group-h")).toContainText("Erfassen");
  // The thousands mark follows the reader's region, so it is not spelled out here.
  await expect(tab.locator(".agent-sum")).toHaveText(/^2 Runden · 1.900 Tokens gelesen · 55 geschrieben$/);
  await expect(tab.locator(".dec-card2")).toHaveCount(2);
  expect(page.__errors).toEqual([]);
});

test("a call shows its model, its tokens, the tools it called and its texts", async ({ page }) => {
  await mount(page);
  const first = page.locator("#tab-agent .dec-card2").first();
  await expect(first.locator(".dec-card2-h")).toContainText("Runde 1");
  await expect(first.locator(".dec-card2-h")).toContainText("claude-opus-5-5");
  await expect(first).toContainText("940 gelesen · 25 geschrieben");
  await expect(first).toContainText("davon 900 aus dem Cache");
  await expect(first).toContainText("4.2 s");
  await expect(first).toContainText("tool_use");
  // The tool is named as the diagram names it, with the model's own id for the call.
  await expect(first.locator(".pill-kv")).toContainText("Freigeben");
  await expect(first.locator(".pill-kv")).toContainText("toolu_1");
  // The texts are folded until somebody wants one.
  const prompt = first.locator("details.agent-text").first();
  await expect(prompt.locator("pre")).toBeHidden();
  await prompt.locator("summary").click();
  await expect(prompt.locator("pre")).toHaveText("Goal: Finde den Zins");
  await expect(first.locator(".agent-limit")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("what a tool returned follows the call that asked for it", async ({ page }) => {
  await mount(page);
  const result = page.locator("#tab-agent .agent-result");
  await expect(result).toHaveCount(1);
  await expect(result.locator(".agent-result-l")).toContainText("Freigeben");
  await expect(result.locator(".agent-result-l")).toContainText("toolu_1");
  await expect(result.locator("pre")).toHaveText("1.45 %");
  // In the order it happened: after the first call's card, before the second's.
  const order = await page.locator("#tab-agent .dec-group > .dec-card2, #tab-agent .dec-group > .agent-result")
    .evaluateAll((els) => els.map((e) => e.className));
  expect(order).toEqual(["dec-card2", "agent-result", "dec-card2"]);
  expect(page.__errors).toEqual([]);
});

test("a reached bound and texts that are gone are said, not left blank", async ({ page }) => {
  await mount(page);
  const second = page.locator("#tab-agent .dec-card2").nth(1);
  await expect(second.locator(".agent-limit")).toHaveText("Rundengrenze erreicht");
  await expect(second).toContainText("Der Schlüssel der betroffenen Person wurde gelöscht");
  await expect(second.locator("details.agent-text")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("an instance that asked no model says so", async ({ page }) => {
  await mount(page, "?empty=1");
  await expect(page.locator("#tab-agent .ops-empty")).toContainText("hat kein Sprachmodell gefragt");
  await expect(page.locator("#tab-agent .dec-card2")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
