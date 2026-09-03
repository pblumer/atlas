// End-to-end coverage for the "pick one, then name it" dialog (api/web/pickmodal.js).
//
// The case behind it, reported from the running server: an application created a
// minute earlier was not in the dialog that asked which application to store an
// information model in. It had not failed to load — the dialog was a window.prompt
// whose body was the choices as numbered text, and a browser truncates a prompt body
// past a handful of lines and ends it with an ellipsis. The newest application sorts
// last, so it was exactly the one cut off, and the ellipsis was the only hint.
//
// So the test that matters is the long list: every option has to be reachable, and
// the last one especially.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/pick-modal-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const result = (page) => page.locator("#result");

test("the newest application is in the list and can be chosen", async ({ page }) => {
  await page.locator("#open").click();
  const select = page.locator("#pick-opt");
  await expect(select).toBeVisible();
  // Every application is an option — no truncation, no ellipsis, nothing to count.
  await expect(select.locator("option")).toHaveCount(30);
  await expect(select.locator("option").last()).toHaveText("proc_new_cutomer");

  await select.selectOption({ label: "proc_new_cutomer" });
  await page.locator("[data-ok]").click();
  await expect(result(page)).toHaveText(/"value":"app-new"/);
  expect(page.__errors).toEqual([]);
});

test("the suggested name follows the picker until it is typed over", async ({ page }) => {
  await page.locator("#open").click();
  const name = page.locator("#pick-name");
  await expect(name).toHaveValue("Application 1 data");

  // Changing the application re-suggests, so the name belongs to what is selected.
  await page.locator("#pick-opt").selectOption({ label: "proc_new_cutomer" });
  await expect(name).toHaveValue("proc_new_cutomer data");

  // Once somebody types their own, the picker stops overwriting it.
  await name.fill("Kundenstamm");
  await page.locator("#pick-opt").selectOption({ label: "Application 7" });
  await expect(name).toHaveValue("Kundenstamm");

  await page.locator("[data-ok]").click();
  await expect(result(page)).toHaveText(/"value":"app-7".*"name":"Kundenstamm"/);
});

test("an empty name is refused rather than sent", async ({ page }) => {
  await page.locator("#open").click();
  await page.locator("#pick-name").fill("   ");
  await page.locator("[data-ok]").click();
  // Still open, nothing resolved: a nameless model is not what anybody meant.
  await expect(page.locator("#pick-opt")).toBeVisible();
  await expect(result(page)).toHaveText("—");
});

test("Escape, Cancel and a click outside all resolve to nothing", async ({ page }) => {
  await page.locator("#open").click();
  await page.keyboard.press("Escape");
  await expect(result(page)).toHaveText("cancelled");
  await expect(page.locator("#pick-opt")).toHaveCount(0);

  await page.locator("#open").click();
  await page.locator("[data-cancel]").click();
  await expect(result(page)).toHaveText("cancelled");

  await page.locator("#open").click();
  await page.locator(".modal-ov").click({ position: { x: 5, y: 5 } });
  await expect(result(page)).toHaveText("cancelled");
});

test("a pick with no name field asks only what it needs", async ({ page }) => {
  // Promoting a release picks a target and nothing else — the release is frozen, so
  // there is no name to give and the dialog does not invent a field for one.
  await page.locator("#open-plain").click();
  await expect(page.locator("#pick-name")).toHaveCount(0);
  await expect(page.locator("[data-ok]")).toHaveText("Promote");
  await page.locator("#pick-opt").selectOption({ label: "Production" });
  await page.locator("[data-ok]").click();
  await expect(result(page)).toHaveText(/"value":"t2"/);
});
