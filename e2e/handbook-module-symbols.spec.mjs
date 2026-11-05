// e2e for the module symbols in the handbook (handbuch.html, "The apps at a glance").
//
// Each app's card leads with its symbol, filled by a module script from
// module-icons.js — the drawing the Console's drawer and top bar use. A script that
// fails to load leaves empty slots and no error anyone sees, so this reads what the
// page actually drew and compares it with the module table itself.
import { test, expect } from "@playwright/test";

test("every app card in the handbook leads with the app's symbol", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/handbuch.html#willkommen");
  const slots = page.locator("#willkommen [data-module]");
  await expect.poll(() => page.locator("#willkommen [data-module] svg.module-symbol").count()).toBeGreaterThan(5);
  const drawn = await slots.evaluateAll((els) => els.map((el) => ({
    id: el.dataset.module,
    tile: el.querySelector("svg.module-symbol rect")?.getAttribute("fill") || null,
    hidden: el.getAttribute("aria-hidden"),
    leads: el.parentElement.firstElementChild === el,
  })));
  const table = await page.evaluate(async () => (await import("./module-icons.js")).MODULE_ICONS);
  expect(drawn.map((d) => d.id).sort()).toEqual(Object.keys(table).sort());
  for (const d of drawn) {
    expect(d.tile, `${d.id}: the card shows the module's own colour`).toBe(table[d.id].colour);
    expect(d.hidden, `${d.id}: the symbol is hidden from assistive technology`).toBe("true");
    expect(d.leads, `${d.id}: the symbol leads its card`).toBe(true);
  }
});
