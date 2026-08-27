// e2e for the secret shape hints (api/web/secret-shapes.js, ADR-0155) — the answer to
// a field that cannot show what it holds. A vault secret is a name and an opaque
// string; the only thing that knows what the string should be is the connector that
// resolves the reference. These tests pin that the form says so before the value is
// typed, and refuses a value that cannot be what the connector needs — the failure
// that otherwise surfaces hours later as an incident on a parked token.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/secret-shapes-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const name = (page, value) => page.locator("#name").fill(value);
const value = (page, v) => page.locator('[name="value"]').fill(v);
const save = (page) => page.locator("#save").click();
const error = (page) => page.locator("#error");

test("the name binds the field to a connector, and the connector says what the value is", async ({ page }) => {
  await name(page, "gmail_auth");
  const hint = page.locator(".secret-hint");
  await expect(hint).toContainText("Patrick Blumer");
  await expect(hint).toContainText("mail · gmail");
  await expect(hint).toContainText("Google OAuth credential bundle");
  // A bundle is edited as text, not behind a password mask: it has to be pasted and
  // read back before it is saved.
  await expect(page.locator("textarea[name=value]")).toBeVisible();
  await expect(page.locator(".secret-skeleton")).toContainText('"refreshToken"');
});

test("a plain-string secret gets no skeleton and stays masked", async ({ page }) => {
  await name(page, "aws-ses-token-01");
  await expect(page.locator(".secret-hint")).toContainText("SMTP password");
  await expect(page.locator(".secret-skeleton")).toHaveCount(0);
  await expect(page.locator("input[name=value][type=password]")).toBeVisible();
});

test("a name nothing references makes no claim about the value", async ({ page }) => {
  await name(page, "something_else");
  await expect(page.locator(".secret-hint")).toContainText("Not referenced by any connector");
  await value(page, "anything at all");
  await save(page);
  await expect(error(page)).toBeHidden();
});

// The outage this shipped for: the bare Google refresh token pasted where the bundle
// belongs. It parses as the number 1 followed by "//", which is why the server's
// message was about JSON syntax and said nothing about what was actually wrong.
test("the bare refresh token is refused, naming the connector and the shape", async ({ page }) => {
  await name(page, "gmail_auth");
  await value(page, "1//04it6ZnHhe84OCgYIARAAGAQSNwF-L9IrFix");
  await save(page);
  await expect(error(page)).toBeVisible();
  await expect(error(page)).toContainText("Patrick Blumer");
  await expect(error(page)).toContainText("not valid JSON");
  await expect(page.locator("#saved")).toBeHidden();
});

test("the skeleton can be inserted and filled in, and then it saves", async ({ page }) => {
  await name(page, "gmail_auth");
  await page.locator("[data-fill]").click();
  // toHaveValue, not toContainText: a textarea filled by script has the text in its
  // value, never in its markup.
  await expect(page.locator("textarea[name=value]")).toHaveValue(/"method": "refreshToken"/);
  await value(page, JSON.stringify({
    method: "refreshToken",
    clientId: "c.apps.googleusercontent.com",
    clientSecret: "GOCSPX-s",
    refreshToken: "1//04abc",
  }));
  await save(page);
  await expect(error(page)).toBeHidden();
  await expect(page.locator("#saved")).toHaveText("saved");
});

test("a bundle missing a field is refused by that field's name", async ({ page }) => {
  await name(page, "gmail_auth");
  await value(page, JSON.stringify({ method: "refreshToken", clientId: "c", refreshToken: "1//04abc" }));
  await save(page);
  await expect(error(page)).toContainText('"clientSecret"');
});

test("an unknown grant method is refused with the ones that exist", async ({ page }) => {
  await name(page, "gmail_auth");
  await value(page, JSON.stringify({ method: "password", clientId: "c" }));
  await save(page);
  await expect(error(page)).toContainText("refreshToken");
  await expect(error(page)).toContainText("serviceAccount");
});

// Without a tenant there is no endpoint to call, which the server reports much later
// (and much less helpfully) as "credential has no token URL".
test("a Microsoft bundle without a tenant is refused here rather than at the first send", async ({ page }) => {
  await name(page, "graph_bundle");
  await value(page, JSON.stringify({ method: "clientCredentials", clientId: "c", clientSecret: "s" }));
  await save(page);
  await expect(error(page)).toContainText("tenantId");

  await value(page, JSON.stringify({ method: "clientCredentials", tenantId: "t", clientId: "c", clientSecret: "s" }));
  await save(page);
  await expect(error(page)).toBeHidden();
});

test("a Remedy credential is checked against its own two fields", async ({ page }) => {
  await name(page, "remedy_creds");
  await expect(page.locator(".secret-hint")).toContainText("AR System username and password");
  await value(page, JSON.stringify({ username: "svc" }));
  await save(page);
  await expect(error(page)).toContainText('"password"');
});

// Jira takes either of two bundles — a Cloud API token or a Data Center personal
// access token — so the check cannot simply demand a field list. What it must not do
// is answer "wrong shape": the useful message names the field missing from whichever
// shape the operator was clearly aiming at.
test("a Jira credential is accepted in either shape, and a half-typed one names its own missing field", async ({ page }) => {
  await name(page, "jira_acme");
  await expect(page.locator(".secret-hint")).toContainText("Jira Cloud");

  await value(page, JSON.stringify({ email: "bot@acme.example" }));
  await save(page);
  await expect(error(page)).toContainText('"apiToken"');

  await value(page, JSON.stringify({ email: "bot@acme.example", apiToken: "ATATT" }));
  await save(page);
  await expect(error(page)).toBeHidden();

  // The Data Center shape is a different set of fields and equally valid.
  await value(page, JSON.stringify({ token: "pat" }));
  await save(page);
  await expect(error(page)).toBeHidden();
  await expect(page.locator("#saved")).toHaveText("saved");
});

test("a JSON list is not a bundle", async ({ page }) => {
  await name(page, "sharepoint_auth");
  await value(page, '["a","b"]');
  await save(page);
  await expect(error(page)).toContainText("not a list");
});
