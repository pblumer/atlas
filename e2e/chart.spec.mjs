// End-to-end coverage for the chart (ADR-0555 §4): the form component that draws one
// dataset of a report variable. It runs on the real vendored form-js viewer
// (chart-harness.html). What it must hold: each of the five types draws its marks from
// the report, every value can be read as a table too, a report that cannot be drawn is
// said in words rather than left blank, and the form never submits the report back.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1100, height: 1400 } });

async function mount(page, data, opts) {
  await page.evaluate(([d, o]) => window.__mount(d, o), [data, opts || {}]);
  await page.waitForFunction(() => window.__mounted === true);
}

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/chart-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

test("each type draws its marks from the report", async ({ page }) => {
  await mount(page, { report: await page.evaluate(() => window.__report) });
  const chart = (type) => page.locator(`.atlas-chart[data-chart-type="${type}"]`).first();

  await expect(chart("kpi").locator("svg text").first()).toHaveText("3");
  // The change is stated in neutral ink: the chart cannot know whether fewer is better.
  await expect(chart("kpi").locator("svg text.atlas-chart-muted")).toContainText("▼ −2");

  // Two series of three categories: six bars, each with its value as a tooltip.
  await expect(chart("bar").locator("svg rect.atlas-chart-s0")).toHaveCount(3);
  await expect(chart("bar").locator("svg rect.atlas-chart-s1")).toHaveCount(3);
  await expect(chart("bar").locator("svg rect.atlas-chart-s0 title").first()).toHaveText("Eintritt · gestartet: 42");
  await expect(chart("bar").locator(".atlas-chart-legend li")).toHaveText(["gestartet", "beendet"]);

  // Shares: three parts of two categories, the zero part not drawn.
  await expect(chart("stackedBar").locator("svg rect[class^='atlas-chart-s']")).toHaveCount(5);
  // Without a region the neutral marks: a point before the decimals.
  await expect(chart("stackedBar").locator("svg rect.atlas-chart-s0 title").first()).toContainText("(85.7 %)");

  // A gap in the values breaks the line: two runs, and one series takes the accent.
  await expect(chart("line").locator("svg polyline.atlas-chart-accent")).toHaveCount(2);
  await expect(chart("line").locator(".atlas-chart-legend")).toHaveCount(0);

  await expect(chart("bullet").locator("svg line.atlas-chart-target")).toHaveCount(1);
  await expect(chart("bullet").locator("svg rect.atlas-chart-accent title")).toHaveText("3 / 2");

  // The report's own words are said once, under the first chart that draws it.
  await expect(page.locator(".atlas-chart-notes")).toHaveCount(1);
  await expect(chart("kpi").locator(".atlas-chart-notes")).toContainText("Statistik auf die neuesten 10000 Instanzen begrenzt");
  await expect(chart("kpi").locator(".atlas-chart-notes")).toContainText("Stand ");
  // A category name is shown whole when there is room for it.
  await expect(chart("bar").locator("svg text", { hasText: "Eintritt" })).toHaveCount(1);
});

test("every chart can be read as a table", async ({ page }) => {
  await mount(page, { report: await page.evaluate(() => window.__report) });
  const bar = page.locator('.atlas-chart[data-chart-type="bar"]');
  await bar.locator(".atlas-chart-toggle").click();
  await expect(bar.locator("svg")).toHaveCount(0);
  await expect(bar.locator("table thead th")).toHaveText(["", "gestartet", "beendet"]);
  await expect(bar.locator("table tbody tr").first()).toHaveText(/Eintritt\s*42\s*40/);
  await bar.locator(".atlas-chart-toggle").click();
  await expect(bar.locator("svg")).toHaveCount(1);
});

test("numbers are written in the reader's region, never the browser's locale", async ({ page }) => {
  const report = await page.evaluate(() => window.__report);
  report.datasets.byProcess.series[0].values = [1234.5, 17, 63];
  const schema = { type: "default", components: [{ type: "chart", id: "c", key: "report", dataset: "byProcess", chartType: "bar" }] };
  for (const [marks, want] of [[undefined, "1\u202F234.5"], [{ group: ".", decimal: "," }, "1.234,5"], [{ group: "'", decimal: "." }, "1'234.5"]]) {
    await mount(page, { report }, { schema, marks });
    await page.locator(".atlas-chart-toggle").click();
    await expect(page.locator(".atlas-chart table tbody tr").first().locator("td").first()).toHaveText(want);
  }
});

test("a report that cannot be drawn is said, not left blank", async ({ page }) => {
  const report = await page.evaluate(() => window.__report);
  const cases = [
    [{}, "Kein Bericht in «report»."],
    [{ report: { schema: "other/2", datasets: {} } }, "«report» ist ein Bericht im Format «other/2»"],
    [{ report: { ...report, datasets: { byProcess: { categories: ["a"], series: [{ name: "x", values: [1, 2] }] } } } }, "keine gültige Reihe"],
    [{ report: { ...report, datasets: { byProcess: { categories: Array.from({ length: 51 }, (_, i) => "c" + i), series: [{ name: "x", values: Array(51).fill(1) }] } } } }, "mehr als 50 Kategorien"],
  ];
  const schema = { type: "default", components: [{ type: "chart", id: "c", key: "report", dataset: "byProcess", chartType: "bar" }] };
  for (const [data, text] of cases) {
    await mount(page, data, { schema });
    await expect(page.locator(".atlas-chart-error")).toContainText(text);
    await expect(page.locator(".atlas-chart svg")).toHaveCount(0);
  }
  // A report that arrives as JSON text is read like an object.
  await mount(page, { report: JSON.stringify(report) }, { schema });
  await expect(page.locator(".atlas-chart svg rect.atlas-chart-s0")).toHaveCount(3);
});

test("the form never submits the report, and speaks English when asked", async ({ page }) => {
  const report = await page.evaluate(() => window.__report);
  await mount(page, { report }, { locale: "en" });
  await page.locator(".fjs-form-field input[type=checkbox]").check();
  const sent = await page.evaluate(() => window.__submit());
  expect(sent).toEqual({ gelesen: true });
  await expect(page.locator('.atlas-chart[data-chart-type="bar"] .atlas-chart-toggle')).toHaveText("As table");
  await expect(page.locator('.atlas-chart[data-chart-type="kpi"] .atlas-chart-notes')).toContainText("As of ");
});

test.describe("in the form editor", () => {
  test.use({ viewport: { width: 1500, height: 1000 } });

  test("the chart is in the palette, and its properties follow its type", async ({ page }) => {
    await page.goto("/form-editor-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => window.__mountChart());
    const canvasField = page.locator('.fjs-editor-container [data-id="Field_chart"]').first();
    await expect(canvasField).toBeVisible({ timeout: 25000 });
    await expect(page.locator('.fjs-palette-field[data-field-type="chart"]')).toBeVisible();
    // No report in the editor: the chart says so in its place.
    await expect(canvasField.locator(".atlas-chart-error")).toHaveText("Kein Bericht in «report».");

    await canvasField.click();
    const props = page.locator(".fjs-properties-panel");
    const general = props.locator('[data-group-id="group-general"]');
    const entries = general.locator(".bio-properties-panel-group-entries");
    if (!(await entries.evaluate((el) => el.classList.contains("open")))) {
      await general.locator(".bio-properties-panel-group-header").click();
    }
    await expect(props.locator("#bio-properties-panel-atlasChart-dataset")).toHaveValue("byProcess");
    await expect(props.locator("#bio-properties-panel-atlasChart-orientation")).toHaveCount(1);
    await expect(props.locator("#bio-properties-panel-atlasChart-target")).toHaveCount(0);
    const key = props.locator("#bio-properties-panel-atlasChart-key");
    await key.fill("wochen bericht");
    await expect(props.locator('[data-entry-id="atlasChart-key"]')).toHaveClass(/has-error/);
    await key.fill("woche.bericht");
    await key.press("Enter");

    await props.locator("#bio-properties-panel-atlasChart-type").selectOption("bullet");
    await expect(props.locator("#bio-properties-panel-atlasChart-target")).toHaveCount(1);
    await expect(props.locator("#bio-properties-panel-atlasChart-orientation")).toHaveCount(0);
    const target = props.locator("#bio-properties-panel-atlasChart-target");
    await target.fill("12");
    await target.press("Enter");

    await page.evaluate(() => window.__save());
    await expect.poll(() => page.evaluate(() => window.__saved && window.__saved.schema)).toBeTruthy();
    const saved = await page.evaluate(() => window.__saved.schema);
    const comp = (typeof saved === "string" ? JSON.parse(saved) : saved).components.find((c) => c.id === "Field_chart");
    expect(comp).toMatchObject({ type: "chart", key: "woche.bericht", dataset: "byProcess", chartType: "bullet", target: 12 });
  });
});
