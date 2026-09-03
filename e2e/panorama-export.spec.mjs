import { test, expect } from "@playwright/test";

// Exporting the landscape (ADR-0211 §10). What is checked here is the *content of
// the artifact*, because that is where this feature can fail invisibly: a file that
// lost its observation stamp still looks like a landscape, and a file whose theme
// tokens failed to resolve looks like a design decision. Both are read out of the
// text the export actually produces.

test.beforeEach(async ({ page }) => {
  await page.goto("/panorama-export-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
});

// §10's central rule: an undated picture circulates long after it stopped being
// true and is believed because it looks like evidence. The date has to come from
// the server, so a payload without one says so instead of substituting this
// browser's clock — which would date the save rather than the reading.
test("the stamp dates the reading, and says so when the server did not", async ({ page }) => {
  const { dated, undated, offset } = await page.evaluate(() => {
    const at = 1_700_000_000;
    return {
      dated: window.exporter.stampLines({ observedAt: at, source: "atlas.example.test" }),
      undated: window.exporter.stampLines({ observedAt: 0, source: "atlas.example.test" }),
      offset: -new Date(at * 1000).getTimezoneOffset(),
    };
  });
  const sign = offset < 0 ? "-" : "+";
  const abs = Math.abs(offset);
  const zone = `UTC${sign}${String(Math.floor(abs / 60)).padStart(2, "0")}:${String(abs % 60).padStart(2, "0")}`;

  expect(dated[1].text).toContain("Observed ");
  expect(dated[1].text).toContain(zone);
  expect(dated[1].text).toContain("Source atlas.example.test");

  // Not a fallback time, and not a blank: the reader has to be able to tell "old"
  // from "unknown", and only the second one is what this is.
  expect(undated[1].text).toContain("Observation time not reported by this server");
  expect(undated[1].text).not.toMatch(/19[67]0-01-01/);
});

// A narrowed picture is the one most worth exporting and the most misleading to
// receive: it is a real landscape, and it is not the landscape.
test("a filtered or drilled export says which landscape it is", async ({ page }) => {
  const lines = await page.evaluate(() => ({
    whole: window.exporter.stampLines({ drawn: { nodes: 40 }, total: 40 }),
    filtered: window.exporter.stampLines({
      scope: { kind: "filter", term: "invoice" }, drawn: { nodes: 6 }, total: 40,
    }),
    drilled: window.exporter.stampLines({
      scope: { kind: "drill", name: "Billing", hops: "2" }, drawn: { nodes: 9 }, total: 40,
    }),
    everything: window.exporter.stampLines({
      scope: { kind: "drill", name: "Billing", hops: "all" }, drawn: { nodes: 40 }, total: 40,
    }),
  }));

  expect(lines.whole[0].text).toContain("the whole landscape");
  // "40 of 40" would read as a narrowing that did not happen.
  expect(lines.whole[1].text).toContain("40 node(s) drawn");
  expect(lines.whole[1].text).not.toContain("of 40");

  expect(lines.filtered[0].text).toContain("filtered by “invoice”");
  expect(lines.filtered[1].text).toContain("6 of 40 node(s) drawn");

  expect(lines.drilled[0].text).toContain("drilled into Billing, within 2 hop(s)");
  expect(lines.everything[0].text).toContain("within any hop(s)");
});

// Everything the picture cannot show has to travel with it. On screen these are in
// the legend beside the canvas; in a file there is no beside.
test("what the picture is not showing is written into it", async ({ page }) => {
  const text = await page.evaluate(() => window.exporter.stampLines({
    observedAt: 1_700_000_000,
    source: "atlas.example.test",
    restricted: 3,
    clustered: true,
    partial: true,
    unavailable: [
      { state: "unreachable", label: "Unreachable", reason: "No deployment target is configured." },
      { state: "stale", label: "Stale" },
    ],
  }).map((l) => l.text).join("\n"));

  expect(text).toContain("3 node(s) in this landscape are hidden by your access");
  expect(text).toContain("collapsed to applications");
  expect(text).toContain("floor rather than a verdict");
  expect(text).toContain("Not watched here: Unreachable, Stale.");
  expect(text).toContain("No deployment target is configured.");
});

// A picture with nothing to declare must not carry the declarations anyway: a
// standing warning is one nobody reads, including on the export where it is true.
test("a complete picture carries no incompleteness notes", async ({ page }) => {
  const lines = await page.evaluate(() => window.exporter.stampLines({
    observedAt: 1_700_000_000, source: "atlas.example.test", drawn: { nodes: 4 }, total: 4,
  }));
  expect(lines).toHaveLength(2);
});

// The notes are the honest half of the stamp, and SVG text does not reflow — so a
// long reason has to be broken into lines or it is simply cut off at the edge.
test("a long note is wrapped rather than cut off", async ({ page }) => {
  const rows = await page.evaluate(() => {
    const long = "word ".repeat(200).trim();
    return window.exporter.wrap(long, 40);
  });
  expect(rows.length).toBeGreaterThan(20);
  for (const row of rows) expect(row.length).toBeLessThanOrEqual(40);
  expect(rows.join(" ").split(" ")).toHaveLength(200);
});

// The artifact has to render where nobody has Atlas's stylesheet, which means the
// theme has to travel as literal values — and it has to be inert, because it is
// opened by people who did not make it.
test("the exported svg is self-contained and carries no behaviour", async ({ page }) => {
  const { svg, width, height } = await page.evaluate(() => {
    const canvas = window.landscape();
    return window.exporter.standaloneSVG(canvas, {
      css: window.exporter.exportStyles(canvas.outerHTML),
      stamp: window.exporter.stampLines({ observedAt: 1_700_000_000, source: "atlas.example.test" }),
      extent: "0 0 800 400",
    });
  });

  // Self-contained: the tokens the nodes name are resolved to literals, transitively
  // — --accent-soft is defined as var(--accent) in the harness, exactly as the real
  // theme defines its soft companions.
  expect(svg).toContain("--accent:#3355ff");
  expect(svg).toContain("--accent-soft:");
  expect(svg).toMatch(/--accent-soft:\s*#3355ff/);
  // The landscape's own rules travel; unrelated page styling does not.
  expect(svg).toContain(".mesh-body");
  expect(svg).not.toContain("not-mesh-at-all");

  // Inert: no script survives, and no event handler attribute either.
  expect(svg).not.toContain("<script");
  expect(svg).not.toContain("onclick");
  expect(await page.evaluate(() => window.exportedScriptRan)).toBeUndefined();

  // Sized, so a viewer that honours width/height opens it at a usable size, and
  // taller than the picture because the stamp is part of the file.
  expect(svg).toContain(`width="${width}"`);
  expect(height).toBeGreaterThan(width * (400 / 800));
  expect(svg).toContain("Observed ");
  expect(svg).toContain("Source atlas.example.test");
});

// Three differences between the picture on screen and the picture in a file, each
// of them because a file is neither zoomable-by-this-app nor animated.
test("the export is stilled, unzoomed, and names everything", async ({ page }) => {
  const svg = await page.evaluate(() => {
    const canvas = window.landscape();
    // Whatever the reader was looking at: zoomed in on the corner, most names
    // hidden because they were too small to read at that magnification.
    canvas.setAttribute("viewBox", "300 150 100 50");
    return window.exporter.standaloneSVG(canvas, {
      css: window.exporter.exportStyles(canvas.outerHTML),
      extent: "0 0 800 400",
    }).svg;
  });

  // The whole landscape, not the window onto it.
  expect(svg).toContain('viewBox="0 0 800 400"');
  expect(svg).not.toContain('viewBox="300 150 100 50"');
  expect(svg).not.toContain("mesh-zoomed");
  // Every name painted: the tier classes are about screen magnification, and a file
  // has none.
  expect(svg).toContain("mesh-names-all");
  expect(svg).not.toContain("mesh-names-anchors");
  // The heartbeat is stilled rather than exported mid-beat.
  expect(svg).not.toContain("mesh-beating");
  expect(svg).toContain("animation:none");
});

// The last check that the file is actually a file: a browser renders it. A
// malformed artifact — unescaped text, a broken nesting, a stylesheet that is not
// valid XML content — fails here and nowhere else.
test("the artifact renders, and encodes as a PNG", async ({ page }) => {
  const png = await page.evaluate(async () => {
    const canvas = window.landscape();
    const built = window.exporter.standaloneSVG(canvas, {
      css: window.exporter.exportStyles(canvas.outerHTML),
      // A name with the characters that break a serializer if anything is
      // concatenated rather than escaped.
      stamp: window.exporter.stampLines({
        observedAt: 1_700_000_000, source: "atlas.example.test",
        scope: { kind: "filter", term: `<b>"R&D" & 'ops'</b>` },
      }),
      extent: "0 0 800 400",
    });
    const blob = await window.exporter.rasterise(built, { scale: 1 });
    const head = new Uint8Array(await blob.slice(0, 8).arrayBuffer());
    return { type: blob.type, size: blob.size, head: [...head] };
  });

  expect(png.type).toBe("image/png");
  expect(png.size).toBeGreaterThan(1000);
  // The PNG signature, so this is an encoded image rather than an empty blob.
  expect(png.head).toEqual([137, 80, 78, 71, 13, 10, 26, 10]);
});

// The file name is not where the provenance lives — that is the stamp — but it is
// where two exports are told apart in a downloads folder.
test("the file name sorts by date", async ({ page }) => {
  const names = await page.evaluate(() => ({
    svg: window.exporter.exportName("svg", new Date(2026, 8, 2, 16, 7)),
    png: window.exporter.exportName("png", new Date(2026, 8, 2, 9, 0)),
  }));
  expect(names.svg).toBe("atlas-landscape-20260902-1607.svg");
  expect(names.png).toBe("atlas-landscape-20260902-0900.png");
});
