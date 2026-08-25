// e2e for the Entra connector's per-operation attribute capture mask
// (api/web/entra-attrmask.js), which the Modeler shows instead of the raw attributes
// JSON for the body-carrying operations (ADR-0172, amended). It proves the mask
// assembles the important fields into the single `attributes` JSON the compiler
// consumes — coercing a boolean field to a real JSON boolean (not the string "true"),
// nesting the password into a passwordProfile, merging a "Weitere Attribute" escape
// hatch, refusing to corrupt the body on malformed escape-hatch JSON, and round-tripping
// an existing body back into the fields.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/entra-attrmask-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("create-user: fields assemble into attributes JSON with correct types", async ({ page }) => {
  await page.fill("#f-emask-displayName", "=vorname + \" \" + nachname");
  await page.fill("#f-emask-mailNickname", "=mailNick");
  await page.fill("#f-emask-userPrincipalName", "=upn");
  await page.fill("#f-emask-password", "=tempPasswort");
  await page.fill("#f-emask-forceChange", "true");
  await page.fill("#f-emask-accountEnabled", "true");
  await page.fill("#f-emask-usageLocation", "CH");

  const o = await page.evaluate(() => window.__parsed());
  // A boolean field becomes a real JSON boolean, not the string "true".
  expect(o.accountEnabled).toBe(true);
  // The FEEL display name (a string beginning with "=") is preserved verbatim.
  expect(o.displayName).toBe('=vorname + " " + nachname');
  // The password and its force-change flag nest into a passwordProfile.
  expect(o.passwordProfile).toEqual({ password: "=tempPasswort", forceChangePasswordNextSignIn: true });
  expect(o.usageLocation).toBe("CH");
});

test("a FEEL boolean stays a string; the escape hatch merges; the mask wins on clash", async ({ page }) => {
  await page.fill("#f-emask-accountEnabled", "=profil.accountEnabled");
  let o = await page.evaluate(() => window.__parsed());
  expect(o.accountEnabled).toBe("=profil.accountEnabled");

  // Extra keys the mask does not own ride in the escape hatch and merge in.
  await page.fill("#f-emask-extra", '{ "jobTitle": "Dev", "displayName": "FromExtra" }');
  await page.fill("#f-emask-displayName", "FromField");
  o = await page.evaluate(() => window.__parsed());
  expect(o.jobTitle).toBe("Dev");
  // A mask field wins over the same key in the escape hatch.
  expect(o.displayName).toBe("FromField");
});

test("malformed escape-hatch JSON shows an error and does not corrupt the body", async ({ page }) => {
  await page.fill("#f-emask-displayName", "Anna");
  const good = await page.evaluate(() => window.__val());
  expect(good).toContain("Anna");

  // A broken extra object flags an error and leaves the last good value in place.
  await page.fill("#f-emask-extra", "{ not json ");
  await expect(page.locator("#f-emask-err")).toBeVisible();
  expect(await page.evaluate(() => window.__val())).toBe(good);

  // Repairing it clears the error and resumes assembling.
  await page.fill("#f-emask-extra", '{ "jobTitle": "Dev" }');
  await expect(page.locator("#f-emask-err")).toBeHidden();
  expect(await page.evaluate(() => window.__parsed()).then((o) => o.jobTitle)).toBe("Dev");
});

test("an existing body round-trips back into the fields, unknown keys into the escape hatch", async ({ page }) => {
  await page.evaluate(() =>
    window.__mount(
      "create-user",
      '{"displayName":"Bestehend","accountEnabled":false,"passwordProfile":{"password":"=pw","forceChangePasswordNextSignIn":true},"jobTitle":"Extra"}',
    ),
  );
  await expect(page.locator("#f-emask-displayName")).toHaveValue("Bestehend");
  await expect(page.locator("#f-emask-accountEnabled")).toHaveValue("false");
  await expect(page.locator("#f-emask-password")).toHaveValue("=pw");
  await expect(page.locator("#f-emask-forceChange")).toHaveValue("true");
  // A key the mask does not own lands in the escape hatch, not lost.
  expect(await page.evaluate(() => document.getElementById("f-emask-extra").value)).toContain("jobTitle");
});

test("create-group: the Unified toggle emits groupTypes:[\"Unified\"]", async ({ page }) => {
  await page.evaluate(() => window.__mount("create-group", ""));
  await page.fill("#f-emask-displayName", "Projekt A");
  await page.fill("#f-emask-mailNickname", "projekt-a");
  await page.fill("#f-emask-unified", "true");
  const o = await page.evaluate(() => window.__parsed());
  expect(o.groupTypes).toEqual(["Unified"]);
  expect(o.displayName).toBe("Projekt A");
});
