// End-to-end coverage for the shape of the Modeler's editor bar (api/web/editor.js).
//
// The bar used to carry seven buttons in one weight — Token simulation, Variables,
// Auto-layout, Save, Export XML, Documentation, Deploy — so shipping a diagram to a
// server looked exactly like re-flowing its boxes, and on a narrower window `flex-wrap`
// dropped a few of them into a second row mid-group. It now carries Save and Deploy,
// with the rest behind a "…" menu (ADR-0229).
//
// What is worth guarding is not the arrangement for its own sake but what a rearrangement
// quietly breaks: that every control still exists and is reachable, that each kept the id
// its behaviour is wired to, that a toggle in a menu still says whether it is on — a menu
// row cannot lean on looking pressed the way a button can — and that the one control that
// puts the editor into a *mode* can still be got out of from inside that mode.
import { test, expect } from "@playwright/test";

const MENU_IDS = ["sim-toggle", "autolayout", "export", "docexport"];

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/editor-bar-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#bar-more")).toBeVisible();
});

test.afterEach(async ({ page }) => {
  expect(page.__errors, "the bar must not throw").toEqual([]);
});

test("the bar carries Variables, Save, Deploy and the menu trigger, and nothing else", async ({ page }) => {
  const onBar = page.locator(".editor-bar > .btn, .editor-bar > .dropdown > button");
  await expect(onBar).toHaveCount(4);
  await expect(onBar.nth(0)).toHaveId("vars-toggle");
  await expect(onBar.nth(1)).toHaveId("save");
  await expect(onBar.nth(2)).toHaveId("deploy");
  await expect(onBar.nth(3)).toHaveId("bar-more");

  // Deploy is the one act here that leaves the browser, and it is the only filled
  // button. `neutral` is what makes a .btn white, so its absence is the assertion.
  await expect(page.locator("#deploy")).not.toHaveClass(/neutral/);
  await expect(page.locator("#save")).toHaveClass(/neutral/);

  // One row, whatever the window: the bar wraps (app.css `.editor-bar`), and seven
  // buttons at this viewport is what used to make it wrap. Children are centred on their
  // row and differ in height, so it is the centre line that tells rows apart, not the top.
  const rows = await page.locator(".editor-bar").evaluate((bar) => {
    const mids = [...bar.children]
      .filter((c) => c.offsetParent !== null)
      .map((c) => { const r = c.getBoundingClientRect(); return Math.round((r.top + r.bottom) / 2); });
    return new Set(mids).size;
  });
  expect(rows).toBe(1);
});

test("the menu holds the rest, and every control kept the id its behaviour is wired to", async ({ page }) => {
  await expect(page.locator("#bar-menu")).toBeHidden();
  await expect(page.locator("#bar-more")).toHaveAttribute("aria-expanded", "false");

  await page.locator("#bar-more").click();
  await expect(page.locator("#bar-menu")).toBeVisible();
  await expect(page.locator("#bar-more")).toHaveAttribute("aria-expanded", "true");

  for (const id of MENU_IDS) await expect(page.locator(`#bar-menu #${id}`)).toBeVisible();
  await expect(page.locator("#bar-menu button")).toHaveCount(MENU_IDS.length);
});

test("the menu closes on the trigger, on an outside click and on Escape", async ({ page }) => {
  const menu = page.locator("#bar-menu");

  await page.locator("#bar-more").click();
  await expect(menu).toBeVisible();
  await page.locator("#bar-more").click();
  await expect(menu).toBeHidden();

  await page.locator("#bar-more").click();
  await expect(menu).toBeVisible();
  await page.locator(".editor-bar .etabs button[data-tab='design']").click();
  await expect(menu).toBeHidden();

  await page.locator("#bar-more").click();
  await expect(menu).toBeVisible();
  await page.locator("#autolayout").press("Escape");
  await expect(menu).toBeHidden();
  await expect(page.locator("#bar-more")).toBeFocused();

  // Escape also works from the trigger, which is where focus sits right after opening.
  await page.locator("#bar-more").click();
  await expect(menu).toBeVisible();
  await page.locator("#bar-more").press("Escape");
  await expect(menu).toBeHidden();
});

test("Variables is a two-state button whose look follows aria-pressed", async ({ page }) => {
  const toggle = page.locator("#vars-toggle");
  const panel = page.locator("#vars-panel");
  // Read the fill with the pointer parked away from the bar: `.btn.neutral:hover` paints
  // its own background, and that is not the state under test.
  const tint = async () => {
    await page.mouse.move(0, 0);
    return toggle.evaluate((b) => getComputedStyle(b).backgroundColor);
  };

  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  await expect(panel).toBeHidden();
  const off = await tint();

  await toggle.click();
  await expect(panel).toBeVisible();
  await expect(toggle).toHaveAttribute("aria-pressed", "true");
  // The pressed look is drawn from the attribute, so a state the button announces and a
  // state it shows cannot come apart. Comparing the fills proves the rule actually bites.
  expect(await tint(), "the pressed button must not look like the unpressed one").not.toBe(off);

  await toggle.click();
  await expect(panel).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  expect(await tint()).toBe(off);

  // Closing the panel by its own ✕ puts the button back in step too.
  await toggle.click();
  await expect(panel).toBeVisible();
  await page.locator("#vars-close").click();
  await expect(panel).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
});

test("F4 toggles Variables, including while a field has focus", async ({ page }) => {
  const panel = page.locator("#vars-panel");

  await page.keyboard.press("F4");
  await expect(panel).toBeVisible();
  await expect(page.locator("#vars-toggle")).toHaveAttribute("aria-pressed", "true");

  await page.keyboard.press("F4");
  await expect(panel).toBeHidden();

  // The point of the shortcut is checking what a variable is called *while* writing the
  // expression that uses it, so unlike F8 it does not stand down for a focused field.
  await page.locator("#vars-toggle").click();
  await page.locator("#vars-filter").fill("kunde");
  await page.locator("#vars-filter").press("F4");
  await expect(panel).toBeHidden();
  await expect(page.locator("#vars-toggle")).toHaveAttribute("aria-pressed", "false");
});

test("simulation is a mode you can leave without going back to the menu", async ({ page }) => {
  await expect(page.locator("#sim-bar")).toBeHidden();

  await page.locator("#bar-more").click();
  await page.locator("#sim-toggle").click();
  await expect(page.locator("#sim-bar")).toBeVisible();
  await expect(page.locator(".editor")).toHaveClass(/sim-active/);

  // The mode hides the modeling palette, so its own bar has to carry the way out —
  // otherwise the only exit is a toggle behind the menu (ADR-0229).
  await page.locator("#sim-exit").click();
  await expect(page.locator("#sim-bar")).toBeHidden();
  await expect(page.locator(".editor")).not.toHaveClass(/sim-active/);

  await page.locator("#bar-more").click();
  await expect(page.locator("#sim-toggle")).toHaveAttribute("aria-pressed", "false");
});

test("Auto-layout still answers F8 from behind the menu", async ({ page }) => {
  const laidOut = () => page.evaluate(() =>
    window.__calls.some((c) => c.method === "POST" && /\/layout$/.test(c.url)));
  expect(await laidOut()).toBe(false);

  await page.keyboard.press("F8");

  // The shortcut clicks the button wherever it sits, so moving it into the menu must not
  // have moved the keyboard with it — and the menu stays shut throughout.
  await expect.poll(laidOut, { timeout: 5000 }).toBe(true);
  await expect(page.locator("#bar-menu")).toBeHidden();
});
