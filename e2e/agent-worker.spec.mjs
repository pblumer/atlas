// e2e for what the Console makes of an agent model
// (api/web/workerdialog.js, ADR-draft-agent-models-are-console-workers).
//
// An agent Worker is the first whose configuration has a part that is neither an
// endpoint nor a credential nor derivable from either — the model name. It is a record
// field rather than a vault bundle entry precisely so it is *visible*: an operator
// weighing cost against capability changes the model, and must be able to read what it
// is set to without opening a secret store. These tests are that promise, checked on
// both paths that write it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/agent-worker-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const build = (page, values) =>
  page.evaluate((v) => { window.set(v); return window.build(); }, values);
const shapeOf = (page, kind, provider) =>
  page.evaluate(([k, p]) => window.shape(k, p), [kind, provider]);

test("an agent create carries its wire format and its model", async ({ page }) => {
  const body = await build(page, {
    kind: "agent",
    name: "openai_pb",
    provider: "chat-completions",
    model: "gpt-4o",
    credentialsRef: "openai_api_key",
  });
  expect(body.kind).toBe("agent");
  expect(body.provider).toBe("chat-completions");
  expect(body.model).toBe("gpt-4o");
  expect(body.credentialsRef).toBe("openai_api_key");
});

// Messages is the default, and a create that names no wire format still carries one —
// so nothing downstream has to re-derive it.
test("an agent create with no wire format chosen defaults to Messages", async ({ page }) => {
  const body = await build(page, { kind: "agent", name: "anthropic_pb", provider: "", credentialsRef: "k" });
  expect(body.provider).toBe("messages");
});

// The model belongs to one kind. The form keeps every field in the DOM and only hides
// the ones a kind does not use, so a model left from a kind picked earlier must not ride
// along — the defect that once refused a jira create with a message about databases.
test("no other kind carries a model", async ({ page }) => {
  const body = await build(page, {
    kind: "temis", name: "rules", endpoint: "https://temis.internal", model: "claude-opus-5",
  });
  expect(body).not.toHaveProperty("model");
});

test("the shape says what an agent's form shows", async ({ page }) => {
  const sh = await shapeOf(page, "agent", "messages");
  expect(sh.model).toBe(true);
  expect(sh.provider).toBe(true);
  // Both wire formats, and only those: the form must not offer SMTP to an agent.
  expect(sh.providerOptions.map(([v]) => v)).toEqual(["messages", "chat-completions"]);
  // An endpoint is offered but optional — each protocol has a public default, and one is
  // named only for a gateway, a proxy or a self-hosted deployment.
  expect(sh.endpoint).toBe(true);
  // A key is asked for without insisting: a self-hosted endpoint may need none, and the
  // server refuses a record with neither.
  expect(sh.credRef).toBe("optional");
  // No connection check exists for this kind yet, and the form says so by not offering
  // a button that would do nothing.
  expect(sh.test).toBe(false);
  expect(sh.sender).toBe(false);
  // The example follows the wire format, because the two families' model names look
  // nothing alike.
  expect((await shapeOf(page, "agent", "chat-completions")).modelPlaceholder).toBe("gpt-4o");
  expect(sh.modelPlaceholder).toBe("claude-opus-5");
});

test("the edit dialog shows an agent's model and saves a changed one", async ({ page }) => {
  const done = page.evaluate(() => window.edit({
    id: "c1", name: "anthropic_pb", kind: "agent", provider: "messages",
    model: "claude-opus-5", credentialsRef: "anthropic_api_key", enabled: true,
  }));
  const modal = page.locator(".conn-modal");
  await expect(modal).toBeVisible();
  await expect(modal.locator("#conn-model")).toHaveValue("claude-opus-5");
  // The select offers wire formats, not mail transports, and is labelled as one.
  await expect(modal.locator(".conn-provider-label")).toHaveText("Wire format");
  const options = await modal.locator("#conn-provider option").evaluateAll((els) => els.map((e) => e.value));
  expect(options).toEqual(["messages", "chat-completions"]);
  // A sender belongs to mail; an agent has none.
  await expect(modal.locator(".conn-f-sender")).toBeHidden();

  await modal.locator("#conn-model").fill("claude-sonnet-5");
  await modal.locator("[data-conn-save]").click();
  await done;

  const patch = await page.evaluate(() => window.__patch);
  expect(patch.model).toBe("claude-sonnet-5");
  expect(patch.provider).toBe("messages");
  // The credential reference travels too, so saving a model change does not clear it.
  expect(patch.credentialsRef).toBe("anthropic_api_key");
});

// Switching the wire format in the dialog re-shapes the form, because "which of these
// do I have to fill in" is a property of the format — and finding that out from a
// rejected save is a worse way to learn it.
test("switching the wire format changes the model example", async ({ page }) => {
  const done = page.evaluate(() => window.edit({
    id: "c1", name: "gw", kind: "agent", provider: "messages", model: "", credentialsRef: "k", enabled: true,
  }));
  const modal = page.locator(".conn-modal");
  await expect(modal.locator("#conn-model")).toHaveAttribute("placeholder", "claude-opus-5");
  await modal.locator("#conn-provider").selectOption("chat-completions");
  await expect(modal.locator("#conn-model")).toHaveAttribute("placeholder", "gpt-4o");
  await modal.locator("[data-conn-cancel]").click();
  await done;
});
