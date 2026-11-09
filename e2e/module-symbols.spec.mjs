// e2e for the module symbols (module-icons.js) and the welcome illustration
// (welcome.svg, scripts/brand/welcome.py).
//
// Every app in the drawer leads with its module's symbol, and the symbol is a picture
// only: the link's name stays the app's name, which is what a screen reader announces
// and what the role tests read. The Console's welcome card shows the rocket whose
// sections are the modules; it has to load (an <img> that names a missing file draws a
// broken-image box, which no DOM query notices), and on a phone it gives way so the
// first actions stay in reach.
import { test, expect } from "@playwright/test";

const ADMIN = { id: "usr_root", username: "root", roles: ["admin"] };

async function boot(page, { width = 1280 } = {}) {
  await page.setViewportSize({ width, height: 800 });
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: true, user: ADMIN } }));
  await page.goto("/index.html#/console");
  await page.waitForFunction(() => document.querySelector("#drawer-apps")?.children.length > 0, null, { timeout: 15000 });
}

test("every app in the drawer leads with its module's symbol", async ({ page }) => {
  await boot(page);
  const apps = await page.evaluate(() => [...document.querySelectorAll("#drawer-apps a")].map((a) => {
    const svg = a.querySelector("svg.module-symbol");
    return {
      app: a.dataset.app,
      name: a.textContent.trim(),
      symbol: !!svg,
      first: a.firstElementChild === svg,
      hidden: svg?.getAttribute("aria-hidden"),
      tile: svg?.querySelector("rect")?.getAttribute("fill"),
    };
  }));
  expect(apps.length).toBeGreaterThan(5);
  const colours = new Set();
  for (const a of apps) {
    expect(a.symbol, `${a.app} has a symbol`).toBe(true);
    expect(a.first, `${a.app}'s symbol leads the entry`).toBe(true);
    expect(a.hidden, `${a.app}'s symbol is hidden from assistive technology`).toBe("true");
    expect(a.name, `${a.app}'s link still says only its name`).not.toBe("");
    colours.add(a.tile);
  }
  // One colour per module: two that look alike are no help in telling them apart.
  expect(colours.size).toBe(apps.length);
});

test("the welcome card shows the rocket, and it loads", async ({ page }) => {
  await boot(page);
  const art = page.locator(".welcome-card img.welcome-art");
  await expect(art).toBeVisible();
  await expect(art).toHaveAttribute("alt", "");
  await expect.poll(() => art.evaluate((img) => img.complete && img.naturalWidth)).toBeGreaterThan(0);
  // It sits beside the text, not under it, on a laptop's width.
  const [text, img] = await Promise.all([
    page.locator(".welcome-text").boundingBox(), art.boundingBox()]);
  expect(img.x).toBeGreaterThan(text.x + text.width - 1);
});

test("on a phone the welcome card leaves the rocket out", async ({ page }) => {
  await boot(page, { width: 390 });
  await expect(page.locator(".welcome-card img.welcome-art")).toBeHidden();
  await expect(page.locator(".welcome-card a.btn", { hasText: "Modeler öffnen" })).toBeVisible();
});

// The top bar says which app one is in by its name and, beside it, its symbol — the
// same one the drawer shows. The atlas mark (or an organisation's logo) stays where it
// is: the symbol joins it, it does not replace it.
const headerSymbol = (page) => page.evaluate(() => {
  const slot = document.querySelector(".topbrand #app-symbol");
  const svg = slot?.querySelector("svg.module-symbol");
  return {
    slot: !!slot,
    hidden: slot?.getAttribute("aria-hidden"),
    tile: svg?.querySelector("rect")?.getAttribute("fill") || null,
    mark: !!document.querySelector(".topbrand .mark"),
    name: document.getElementById("app-name").textContent,
  };
});
const drawerTile = (page, app) => page.evaluate((app) =>
  document.querySelector(`#drawer-apps a[data-app="${app}"] svg.module-symbol rect`)?.getAttribute("fill"), app);

test("the top bar shows the symbol of the app one is in", async ({ page }) => {
  await boot(page);
  for (const [hash, app, name] of [["#/console", "console", "Console"], ["#/tasks", "tasks", "Tasks"],
    ["#/operations", "operations", "Operations"], ["#/modeler", "modeler", "Modeler"]]) {
    await page.evaluate((h) => { location.hash = h; }, hash);
    await expect.poll(async () => (await headerSymbol(page)).name).toBe(name);
    const h = await headerSymbol(page);
    expect(h.slot, `${app}: the bar has a place for the symbol`).toBe(true);
    expect(h.hidden, `${app}: the symbol is hidden from assistive technology`).toBe("true");
    expect(h.mark, `${app}: the mark stays`).toBe(true);
    expect(h.tile, `${app}: the bar shows the app's own symbol`).toBe(await drawerTile(page, app));
  }
});

test("the login screen's top bar shows no app symbol", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) => route.fulfill({ json: { authEnabled: true, user: null } }));
  await page.goto("/index.html#/console");
  await expect.poll(async () => (await headerSymbol(page)).name).toBe("atlas");
  expect((await headerSymbol(page)).tile).toBeNull();
});

// Where the bar is tight the symbol gives its room to the view names (an
// administrator's Operations bar at 1024px had no pixel to spare); on a laptop's
// wider window and on a phone it is there.
for (const [width, shown] of [[1280, true], [1024, false], [390, true]]) {
  test(`at ${width}px the top bar's app symbol is ${shown ? "shown" : "left out"}`, async ({ page }) => {
    await boot(page, { width });
    const symbol = page.locator(".topbrand #app-symbol svg.module-symbol");
    if (shown) await expect(symbol).toBeVisible();
    else await expect(symbol).toBeHidden();
  });
}
