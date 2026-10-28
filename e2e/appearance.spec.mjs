// End-to-end coverage for the appearance — light, dark or following the system — and
// for what it does to the Modeler (api/web/appearance.js, the bootstrap in
// api/web/index.html, the top bar's switch in api/web/app.js, the dark palette in
// api/web/app.css; ADR-0507).
//
// The appearance is the fourth display preference (ADR-0459): stored with the account
// where there is one, mirrored in the browser (`atlas.prefs`) for the first paint and
// as the whole store where there is no sign-in. What is worth guarding is what a
// person sees. Today's installations must look exactly as they did, a dark choice
// must be on the page before the body is parsed, `system` must follow the operating
// system while an explicit choice ignores it, a broken preference must leave the
// console light rather than broken, the switch beside the favourites must turn the
// console at once and keep the choice for the person, and a choice made on another
// device must arrive with the account's record.
//
// The Modeler half is the reason the prototype existed. bpmn-js paints every shape
// with a fill and a stroke it writes inline, so a dark stylesheet alone leaves white
// boxes on a dark canvas. The renderer is given colours that read the appearance's
// tokens instead (DIAGRAM_RENDERER), and these tests hold the consequences: every
// shape the harness diagram carries is dark, labels and arrows stay readable, a switch
// re-colours an open diagram without importing it again, and the picture a document
// export rasterizes is still the light one.
import { test, expect } from "@playwright/test";

const MIRROR = "atlas.prefs";

// rgbOf turns any CSS colour the page can compute — rgb(), hsl(), color(srgb …) from a
// color-mix() — into [r, g, b] by painting it, so the assertions do not depend on how a
// browser chooses to serialize a colour.
const COLOR_HELPERS = `
  window.__rgbOf = (css) => {
    const c = document.createElement("canvas"); c.width = c.height = 1;
    const x = c.getContext("2d"); x.fillStyle = "#000"; x.fillStyle = css; x.fillRect(0, 0, 1, 1);
    const d = x.getImageData(0, 0, 1, 1).data; return [d[0], d[1], d[2]];
  };
  window.__lum = (rgb) => {
    const f = (v) => { const c = v / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
    return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
  };
  window.__contrast = (a, b) => {
    const la = window.__lum(a), lb = window.__lum(b);
    return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
  };
`;

// prefer mirrors a display record with this appearance before any script of the page
// runs, the way a returning visitor arrives. `undefined` leaves the storage empty; a
// string that is not a word of the record (`raw`) is written as it is.
function prefer(page, value, { raw = false } = {}) {
  return page.addInitScript(({ key, value, raw }) => {
    if (value === undefined) return;
    localStorage.setItem(key, raw ? value : JSON.stringify({ region: "", timeZone: "", language: "", appearance: value }));
  }, { key: MIRROR, value, raw });
}

const mirrored = (page) => page.evaluate((key) => {
  try { return JSON.parse(localStorage.getItem(key) || "{}").appearance; } catch { return undefined; }
}, MIRROR);

// ---------- The console shell ----------

// installMock answers the console's start-up calls. With `account` set, somebody is
// signed in and /me/preferences serves and stores their record (`account` is that
// record); without it, authentication is off and the resource answers 404, as the
// server does. The org-wide brand colour is `accent`. Every other call returns [].
// It returns the state, so a test can read what was stored.
function installMock(page, { accent = "", account = null } = {}) {
  const state = { record: account ? { userId: "usr_1", region: "", timeZone: "", language: "", appearance: "", ...account } : null, puts: [] };
  page.route("**/api/v1/**", (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: account
        ? { authEnabled: true, user: { id: "usr_1", username: "anna", roles: ["admin"] } }
        : { authEnabled: false, user: null } });
    }
    if (path.endsWith("/settings/theme")) return route.fulfill({ json: { accent } });
    if (path.endsWith("/me/preferences")) {
      if (!state.record) return route.fulfill({ status: 404, json: { error: "no signed-in identity on this instance" } });
      if (req.method() === "PUT") {
        const body = req.postDataJSON() || {};
        state.puts.push(body);
        state.record = { userId: "usr_1", ...body };
      }
      return route.fulfill({ json: state.record });
    }
    return route.fulfill({ json: [] });
  });
  return state;
}

// bootApp opens the real console and records, at the moment <body> is inserted, which
// appearance the document carried — that is what the first paint is drawn with.
async function bootApp(page, { hash = "", accent, account } = {}) {
  await page.addInitScript(() => {
    new MutationObserver((_, obs) => {
      if (!document.body) return;
      window.__appearanceAtBody = document.documentElement.getAttribute("data-appearance");
      obs.disconnect();
    }).observe(document, { childList: true, subtree: true });
  });
  await page.addInitScript(COLOR_HELPERS);
  await page.addInitScript(SET_APPEARANCE);
  const state = installMock(page, { accent, account });
  await page.goto("/index.html" + hash);
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
  return state;
}

const appearance = (page) => page.evaluate(() => document.documentElement.getAttribute("data-appearance"));

// __setAppearance draws the console in an appearance through the real module, without
// a round trip through the preferences.
const SET_APPEARANCE = `
  window.__setAppearance = async (value) => (await import("/appearance.js")).applyAppearance(value);
`;
const bodyBackground = (page) => page.evaluate(() => window.__rgbOf(getComputedStyle(document.body).backgroundColor));

test("with nothing stored the console keeps today's light appearance", async ({ page }) => {
  await prefer(page, undefined);
  await page.emulateMedia({ colorScheme: "dark" }); // the default is light, not "whatever the OS says"
  await bootApp(page);
  expect(await appearance(page)).toBe("light");
  expect(await bodyBackground(page)).toEqual([243, 244, 246]); // --bg, unchanged
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe("light");
});

test("a dark choice is on the document before the body is parsed", async ({ page }) => {
  await prefer(page, "dark");
  await bootApp(page);
  expect(await page.evaluate(() => window.__appearanceAtBody)).toBe("dark");
  expect(await appearance(page)).toBe("dark");
  const bg = await bodyBackground(page);
  expect(await page.evaluate((bg) => window.__lum(bg), bg)).toBeLessThan(0.02);
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme)).toBe("dark");
});

test("system follows the operating system, live, and an explicit choice ignores it", async ({ page }) => {
  await prefer(page, "system");
  await page.emulateMedia({ colorScheme: "dark" });
  await bootApp(page);
  expect(await page.evaluate(() => window.__appearanceAtBody)).toBe("dark");
  await page.emulateMedia({ colorScheme: "light" });
  await expect.poll(() => appearance(page)).toBe("light");
  await page.emulateMedia({ colorScheme: "dark" });
  await expect.poll(() => appearance(page)).toBe("dark");

  // An explicit light choice made in another tab wins over the dark system from then on.
  await page.evaluate((key) => {
    const value = JSON.stringify({ region: "", timeZone: "", language: "", appearance: "light" });
    localStorage.setItem(key, value);
    window.dispatchEvent(new StorageEvent("storage", { key, newValue: value }));
  }, MIRROR);
  await expect.poll(() => appearance(page)).toBe("light");
  await page.emulateMedia({ colorScheme: "light" });
  await page.emulateMedia({ colorScheme: "dark" });
  expect(await appearance(page)).toBe("light");
});

test("an unreadable preference leaves the console light, without an error", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await prefer(page, "neon");
  await bootApp(page);
  expect(await appearance(page)).toBe("light");
  expect(errors).toEqual([]);
});

test("a mirror that is not a record leaves the console light, without an error", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await prefer(page, "dark", { raw: true }); // the prototype's bare word, not JSON
  await bootApp(page);
  expect(await page.evaluate(() => window.__appearanceAtBody)).toBe("light");
  expect(errors).toEqual([]);
});

test("storage that refuses to be read leaves the console light, without an error", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(() => {
    Storage.prototype.getItem = () => { throw new Error("storage disabled"); };
  });
  await bootApp(page);
  expect(await appearance(page)).toBe("light");
  expect(errors).toEqual([]);
});

test("a cached brand colour stays the brand colour, with a soft tint that belongs to the dark surface", async ({ page }) => {
  // What theme.js caches for the federal red: the derived map, light soft tint included.
  await page.addInitScript(() => {
    localStorage.setItem("atlas.theme", JSON.stringify({
      color: "#d52b1e",
      vars: { "--accent": "#d52b1e", "--accent-hover": "#af2319", "--accent-soft": "#fbeae9", "--accent-ink": "#ffffff" },
    }));
  });
  await prefer(page, "dark");
  await bootApp(page, { accent: "#d52b1e" }); // the server confirms the cache
  const probe = await page.evaluate(() => {
    const el = document.createElement("div");
    el.style.cssText = "background-color: var(--accent); border-top: 1px solid var(--accent-soft)";
    document.body.appendChild(el);
    const cs = getComputedStyle(el);
    return { accent: window.__rgbOf(cs.backgroundColor), soft: window.__rgbOf(cs.borderTopColor) };
  });
  expect(probe.accent).toEqual([213, 43, 30]); // the brand is not the appearance's to change
  expect(await page.evaluate((rgb) => window.__lum(rgb), probe.soft)).toBeLessThan(0.05);
});

// ---------- The accent as text ----------

// A link, an active tab, a count in a pill: the accent is read as text on the
// console's surfaces. A brand colour chosen for a white page is often too dark for a
// dark one — the federal red stands 3.3:1 on the dark surface, a navy far less — so in
// dark the text takes a tone derived from the brand (theme.js accentOnDark) that keeps
// 4.5:1 on the surface and on the accent's own soft tint, and in light it is the brand
// colour itself, exactly as before.
const BRANDS = [
  ["the stock accent", ""],
  ["the federal red", "#d52b1e"],
  ["a navy", "#001f5c"],
  ["a violet", "#7c3aed"],
  ["a pale yellow", "#f5c400"],
];
const accentProbe = (page) => page.evaluate(() => {
  const read = (css) => {
    const el = document.createElement("span");
    el.style.cssText = css;
    document.body.appendChild(el);
    const cs = getComputedStyle(el);
    const out = { color: window.__rgbOf(cs.color), background: window.__rgbOf(cs.backgroundColor) };
    el.remove();
    return out;
  };
  const onSurface = read("color: var(--accent-text); background: var(--surface)");
  const onSoft = read("color: var(--accent-text); background: var(--accent-soft)");
  const brand = read("color: var(--accent)").color;
  // A link in the real console, to be sure the stylesheet reads the tone too.
  const link = [...document.querySelectorAll("#view a[href]")]
    .find((a) => !a.closest(".btn, button, .card-head") && !a.classList.contains("btn"));
  return {
    brand, text: onSurface.color,
    onSurface: window.__contrast(onSurface.color, onSurface.background),
    onSoft: window.__contrast(onSoft.color, onSoft.background),
    link: link ? window.__rgbOf(getComputedStyle(link).color) : null,
  };
});

for (const [name, accent] of BRANDS) {
  test(`${name} as text stays readable on the dark surfaces and unchanged in light`, async ({ page }) => {
    await prefer(page, "dark");
    await bootApp(page, { accent });
    // The server's brand is applied by syncFromServer after the first paint.
    if (accent) await expect.poll(() => page.evaluate(() => document.documentElement.style.getPropertyValue("--accent").trim())).toBe(accent);
    const dark = await accentProbe(page);
    expect(dark.onSurface, "on --surface").toBeGreaterThanOrEqual(4.5);
    expect(dark.onSoft, "on --accent-soft").toBeGreaterThanOrEqual(4.5);
    expect(dark.link).toEqual(dark.text);

    await page.evaluate(() => window.__setAppearance("light"));
    const light = await accentProbe(page);
    expect(light.text, "in light the text is the brand colour itself").toEqual(light.brand);
    expect(light.link).toEqual(light.brand);
  });
}

// ---------- The switch beside the favourites ----------

const toggle = (page) => page.locator("#appearance-toggle");

test("the switch sits beside the favourites, and is there with nobody signed in", async ({ page }) => {
  await bootApp(page);
  // Next to the favourites menu, which itself is hidden until somebody signs in: the
  // switch is not, because a browser without an account keeps the choice itself.
  expect(await page.evaluate(() => document.getElementById("shortcuts-dropdown").nextElementSibling.id))
    .toBe("appearance-toggle");
  await expect(toggle(page)).toBeVisible();
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "false");
  await expect(toggle(page)).toHaveAttribute("aria-label", "Dunkle Darstellung");
  await expect(toggle(page)).toHaveAttribute("title", /Vorschau/);
});

test("the switch turns the console dark at once and keeps the choice for the account", async ({ page }) => {
  const state = await bootApp(page, { account: { region: "ch", timeZone: "Europe/Zurich" } });
  await toggle(page).click();
  expect(await appearance(page)).toBe("dark");
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
  await expect(toggle(page)).toHaveAttribute("title", "Helle Darstellung einschalten");
  // The whole record goes back, with only the appearance changed: the switch must not
  // clear the number format and the zone the person chose on the Display screen.
  await expect.poll(() => state.puts.length).toBe(1);
  expect(state.puts[0]).toMatchObject({ region: "ch", timeZone: "Europe/Zurich", appearance: "dark" });
  expect(await mirrored(page)).toBe("dark");

  // And back.
  await toggle(page).click();
  expect(await appearance(page)).toBe("light");
  await expect.poll(() => state.puts.length).toBe(2);
  expect(state.puts[1]).toMatchObject({ appearance: "light" });
});

test("without an account the switch keeps the choice in this browser", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await bootApp(page);
  await toggle(page).click();
  expect(await appearance(page)).toBe("dark");
  expect(await mirrored(page)).toBe("dark");
  // No account to save it for is not a failure: nothing says it was not saved.
  await expect(page.locator(".toast.err")).toHaveCount(0);
  await page.reload();
  await page.waitForFunction(() => document.querySelector("#view") && document.querySelector("#view").children.length > 0);
  expect(await page.evaluate(() => window.__appearanceAtBody)).toBe("dark");
  expect(errors).toEqual([]);
});

test("a choice made on another device arrives with the account's record", async ({ page }) => {
  await prefer(page, "light"); // what this browser remembered
  await bootApp(page, { account: { appearance: "dark" } });
  await expect.poll(() => appearance(page)).toBe("dark");
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
  expect(await mirrored(page)).toBe("dark");
});

test("pressing the switch while following the system makes a choice", async ({ page }) => {
  await prefer(page, "system");
  await page.emulateMedia({ colorScheme: "dark" });
  await bootApp(page);
  await expect(toggle(page)).toHaveAttribute("aria-pressed", "true");
  await toggle(page).click();
  // Light, because that is what was asked for — and the system turning dark again
  // no longer moves it.
  expect(await appearance(page)).toBe("light");
  expect(await mirrored(page)).toBe("light");
  await page.emulateMedia({ colorScheme: "light" });
  await page.emulateMedia({ colorScheme: "dark" });
  expect(await appearance(page)).toBe("light");
});

test("the sign-in screen is drawn in the appearance this browser remembers", async ({ page }) => {
  await prefer(page, "dark");
  await page.addInitScript(COLOR_HELPERS);
  page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ status: 401, json: { error: "sign in" } });
    if (path.endsWith("/settings/theme")) return route.fulfill({ json: { accent: "" } });
    return route.fulfill({ status: 401, json: { error: "sign in" } });
  });
  await page.goto("/index.html");
  await expect(page.locator("#login-form")).toBeVisible({ timeout: 15000 });
  expect(await appearance(page)).toBe("dark");
  const card = await page.locator("#login-form").evaluate((f) => window.__rgbOf(getComputedStyle(f.closest(".card")).backgroundColor));
  expect(await page.evaluate((rgb) => window.__lum(rgb), card)).toBeLessThan(0.05);
});

test("signing in applies the account's appearance before the first screen", async ({ page }) => {
  await page.addInitScript(COLOR_HELPERS);
  let signedIn = false;
  page.route("**/api/v1/**", (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/login")) { signedIn = true; return route.fulfill({ json: { ok: true } }); }
    if (path.endsWith("/auth/me")) {
      return signedIn
        ? route.fulfill({ json: { authEnabled: true, user: { id: "usr_1", username: "anna", roles: ["admin"] } } })
        : route.fulfill({ status: 401, json: { error: "sign in" } });
    }
    if (path.endsWith("/settings/theme")) return route.fulfill({ json: { accent: "" } });
    if (path.endsWith("/me/preferences")) {
      return signedIn
        ? route.fulfill({ json: { userId: "usr_1", region: "", timeZone: "", language: "", appearance: "dark" } })
        : route.fulfill({ status: 401, json: { error: "sign in" } });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html");
  await expect(page.locator("#login-form")).toBeVisible({ timeout: 15000 });
  expect(await appearance(page)).toBe("light"); // this browser remembered nothing
  await page.locator('#login-form input[name="username"]').fill("anna");
  await page.locator('#login-form input[name="password"]').fill("annapassword123");
  await page.locator("#login-form").evaluate((f) => f.requestSubmit());
  await expect(page.locator("#login-form")).toHaveCount(0, { timeout: 15000 });
  // Anna's record says dark, and she gets it now, not on her next reload.
  expect(await appearance(page)).toBe("dark");
});

// The customer-facing pages keep the light console until they are audited
// (ADR-0507's declared fallback): a person's dark choice in the Console must not
// turn the shop or a public start form dark around content nobody has checked.
for (const pagePath of ["/shop.html", "/public-form.html", "/oauth-consent.html"]) {
  test(`${pagePath} stays light whatever the Console's appearance`, async ({ page }) => {
    await prefer(page, "dark");
    page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
    await page.goto(pagePath);
    await page.waitForLoadState("domcontentloaded");
    expect(await page.evaluate(() => document.documentElement.getAttribute("data-appearance"))).not.toBe("dark");
    expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--surface").trim())).toBe("#ffffff");
  });
}

// ---------- The Modeler ----------

async function mountModeler(page, value) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await prefer(page, value);
  await page.addInitScript(COLOR_HELPERS);
  await page.goto("/appearance-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator('[data-element-id="Task_1"]')).toBeVisible({ timeout: 20000 });
}

// paint reads what the canvas actually draws: the first visual of every shape, the
// label of a task, the arrowhead of a flow, the canvas and the palette behind them.
const paint = (page) => page.evaluate(() => {
  const rgb = window.__rgbOf;
  const fills = {};
  for (const g of document.querySelectorAll("#canvas .djs-shape")) {
    const id = g.getAttribute("data-element-id");
    const first = g.querySelector(".djs-visual > :first-child");
    if (first && first.tagName !== "text") fills[id] = rgb(getComputedStyle(first).fill);
  }
  const task = document.querySelector('#canvas [data-element-id="Task_1"] .djs-visual');
  const marker = document.querySelector("#canvas marker path");
  return {
    fills,
    taskFill: rgb(getComputedStyle(task.querySelector("rect")).fill),
    taskStroke: rgb(getComputedStyle(task.querySelector("rect")).stroke),
    label: rgb(getComputedStyle(task.querySelector("text")).fill),
    arrow: rgb(getComputedStyle(marker).fill),
    canvas: rgb(getComputedStyle(document.querySelector("#canvas")).backgroundColor),
    palette: rgb(getComputedStyle(document.querySelector("#canvas .djs-palette")).backgroundColor),
    reference: rgb("hsl(225, 10%, 15%)"), // bpmn-js's own stroke and label colour
  };
});

test("light draws the diagram in exactly bpmn-js's own colours", async ({ page }) => {
  await mountModeler(page, undefined);
  const p = await paint(page);
  expect(p.taskFill).toEqual([255, 255, 255]);
  expect(p.taskStroke).toEqual(p.reference);
  expect(p.label).toEqual(p.reference);
  for (const [id, fill] of Object.entries(p.fills)) {
    if (id === "Pool_1" || id === "Note_1") continue; // a pool and an annotation are not filled boxes
    expect(fill, id).toEqual([255, 255, 255]);
  }
  expect(page.__errors).toEqual([]);
});

test("dark draws every shape, its label and its arrows from the dark palette", async ({ page }) => {
  await mountModeler(page, "dark");
  const p = await paint(page);
  const lum = (rgb) => page.evaluate((rgb) => window.__lum(rgb), rgb);
  const contrast = (a, b) => page.evaluate(([a, b]) => window.__contrast(a, b), [a, b]);

  // One white box among dark ones is the failure this exists for: a shape bpmn-js
  // paints without the configured fill.
  for (const [id, fill] of Object.entries(p.fills)) {
    expect(await lum(fill), `${id} is drawn light`).toBeLessThan(0.05);
  }
  expect(await lum(p.canvas)).toBeLessThan(0.02);
  expect(await lum(p.palette)).toBeLessThan(0.05);
  // WCAG 2.1: 4.5:1 for the label a person reads, 3:1 for the outline and the arrowhead.
  expect(await contrast(p.label, p.taskFill)).toBeGreaterThanOrEqual(4.5);
  expect(await contrast(p.taskStroke, p.taskFill)).toBeGreaterThanOrEqual(3);
  expect(await contrast(p.taskStroke, p.canvas)).toBeGreaterThanOrEqual(3);
  expect(await contrast(p.arrow, p.canvas)).toBeGreaterThanOrEqual(3);
  expect(page.__errors).toEqual([]);
});

// The bpmn.io licence: the watermark "must stay fully visible". It is neither moved nor
// recoloured — only what is behind it changes — so it is held at 4.5:1 against what
// is actually behind it, in the colour the library drew it in.
const watermark = (page, scope) => page.evaluate((scope) => {
  const link = document.querySelector(`${scope} .bjs-powered-by`);
  const path = link && link.querySelector("path");
  if (!path) return null;
  let behind = [0, 0, 0];
  for (let el = link; el; el = el.parentElement) {
    const bg = getComputedStyle(el).backgroundColor;
    if (bg && bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent") { behind = window.__rgbOf(bg); break; }
  }
  const r = link.getBoundingClientRect();
  const logo = window.__rgbOf(getComputedStyle(path).fill);
  return { visible: r.width > 0 && r.height > 0, contrast: window.__contrast(logo, behind) };
}, scope);

test("the bpmn.io watermark stays fully visible on the dark canvas, as drawn", async ({ page }) => {
  await mountModeler(page, "dark");
  const w = await watermark(page, "#canvas");
  expect(w && w.visible).toBe(true);
  expect(w.contrast).toBeGreaterThanOrEqual(4.5);
  // Drawn in the library's own colour, not one this stylesheet chose.
  expect(await page.evaluate(() => window.__rgbOf(getComputedStyle(document.querySelector("#canvas .bjs-powered-by")).color)))
    .toEqual([64, 64, 64]);
});

test("switching the appearance re-colours an open diagram without importing it again", async ({ page }) => {
  await mountModeler(page, "light");
  await page.evaluate(() => {
    window.__imports = 0;
    window.__atlasModeler.on("import.done", () => { window.__imports++; });
  });
  expect((await paint(page)).taskFill).toEqual([255, 255, 255]);
  await page.evaluate(() => window.__applyAppearance("dark"));
  const dark = await paint(page);
  expect(await page.evaluate((rgb) => window.__lum(rgb), dark.taskFill)).toBeLessThan(0.05);
  expect(await page.evaluate(() => window.__imports)).toBe(0);
});

test("in dark the exported picture is still the light one, and the model carries no colour", async ({ page }) => {
  await mountModeler(page, "dark");
  const out = await page.evaluate(async () => {
    const { svg } = await window.__atlasModeler.saveSVG();
    const { xml } = await window.__atlasModeler.saveXML({ format: true });
    // Rasterize the way doc-graphics.js does: the SVG as an image of its own, which
    // sees none of the page's stylesheet.
    const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml;charset=utf-8" }));
    const img = await new Promise((ok, fail) => { const i = new Image(); i.onload = () => ok(i); i.onerror = fail; i.src = url; });
    const vb = /viewBox="([-\d.]+) ([-\d.]+) ([\d.]+) ([\d.]+)"/.exec(svg).slice(1).map(Number);
    const c = document.createElement("canvas");
    c.width = img.naturalWidth; c.height = img.naturalHeight;
    const ctx = c.getContext("2d");
    ctx.drawImage(img, 0, 0);
    const at = (x, y) => {
      const px = Math.round(((x - vb[0]) / vb[2]) * c.width), py = Math.round(((y - vb[1]) / vb[3]) * c.height);
      const d = ctx.getImageData(px, py, 1, 1).data; return [d[0], d[1], d[2], d[3]];
    };
    URL.revokeObjectURL(url);
    // Inside Task_1 near its corner (clear of the label), and on its top border.
    return { inside: at(262, 172), border: at(300, 160), colours: /bioc:|color:(background|border)-color/.test(xml) };
  });
  expect(out.inside).toEqual([255, 255, 255, 255]);
  expect(await page.evaluate((rgb) => window.__lum(rgb), out.border.slice(0, 3))).toBeLessThan(0.1);
  expect(out.colours).toBe(false);
});

test("code tokens stay readable on the editor's surface in both appearances", async ({ page }) => {
  for (const value of ["light", "dark"]) {
    await mountModeler(page, value);
    const failing = await page.evaluate(() => {
      const box = document.querySelector("#code-sample");
      const bg = window.__rgbOf(getComputedStyle(box).backgroundColor);
      return [...box.querySelectorAll("[class^='tok-'], [class^='jtok-']")]
        .map((s) => ({ cls: s.className, ratio: window.__contrast(window.__rgbOf(getComputedStyle(s).color), bg) }))
        .filter((t) => t.ratio < 4.5)
        .map((t) => `${t.cls} ${t.ratio.toFixed(2)}`);
    });
    expect(failing, value).toEqual([]);
  }
});

// ---------- The DMN editor ----------

test("dark draws the decision requirements graph from the same palette", async ({ page }) => {
  await prefer(page, "dark");
  await page.addInitScript(COLOR_HELPERS);
  installMock(page);
  await page.goto("/index.html#/modeler/dmn/new/p/app-1");
  const shape = page.locator(".dmn-canvas .djs-shape .djs-visual > rect").first();
  await expect(shape).toBeVisible({ timeout: 20000 });
  const fill = await shape.evaluate((el) => window.__lum(window.__rgbOf(getComputedStyle(el).fill)));
  expect(fill).toBeLessThan(0.05);
  // dmn-js draws its watermark in black: on the dark canvas it needs the light ground.
  const w = await watermark(page, ".dmn-canvas");
  expect(w && w.visible).toBe(true);
  expect(w.contrast).toBeGreaterThanOrEqual(4.5);
});
