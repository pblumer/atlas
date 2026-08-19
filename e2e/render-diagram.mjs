// Renders a conformance fixture into its gallery PNG
// (api/web/conformance-diagrams/<name>.png) with the *vendored* bpmn-js, so a catalog
// image always matches what the Modeler draws — including the loop / multi-instance
// markers a reader is meant to recognize. Not a test: an asset tool, run by hand when a
// scenario is added or its diagram changes.
//
//   node server.mjs &                     # serves ../api/web plus the harness pages
//   node render-diagram.mjs ../conformance/models/standard-loop.bpmn \
//        ../api/web/conformance-diagrams/standard-loop.png
//
// The model needs hand-authored BPMN-DI (bpmn-js renders nothing without it), and the
// output frame matches the committed images: 1032x240, white, no bpmn.io watermark.
import { chromium } from "@playwright/test";
import { readFile } from "node:fs/promises";

const [, , model, out] = process.argv;
if (!model || !out) {
  console.error("usage: node render-diagram.mjs <model.bpmn> <out.png>");
  process.exit(2);
}
const xml = await readFile(model, "utf8");
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1100, height: 300 }, deviceScaleFactor: 2 });
await page.goto(`http://localhost:${process.env.PORT || 8199}/render-diagram-harness.html`);
await page.waitForFunction(() => window.__ready === true);
await page.evaluate((x) => window.__render(x), xml);
await page.waitForTimeout(400); // let the web font settle before the shot
await page.locator("#c").screenshot({ path: out, scale: "css" });
await browser.close();
console.log("wrote", out);
