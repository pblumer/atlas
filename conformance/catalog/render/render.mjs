// Renders every conformance fixture that carries BPMN-DI to a PNG under ../diagrams/,
// using Atlas's own vendored bpmn-js modeler (../../../api/web). The committed diagrams
// are the source of truth for the catalog; run this to regenerate them after editing a
// model's layout. Needs: npm ci here, then a Chromium (npx playwright install chromium,
// or set CHROMIUM_PATH to a pre-installed one).
import http from "node:http"; import { readFile, readdir } from "node:fs/promises";
import { extname, join, normalize, basename } from "node:path"; import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
const here = fileURLToPath(new URL(".", import.meta.url)).replace(/[/\\]+$/, "");
const WEB = fileURLToPath(new URL("../../../api/web", import.meta.url)).replace(/[/\\]+$/, "");
const MODELS = fileURLToPath(new URL("../../models", import.meta.url)).replace(/[/\\]+$/, "");
const OUT = fileURLToPath(new URL("../diagrams", import.meta.url)).replace(/[/\\]+$/, "");
const MIME = { ".html":"text/html", ".js":"text/javascript", ".css":"text/css", ".json":"application/json", ".bpmn":"application/xml", ".svg":"image/svg+xml", ".woff":"font/woff", ".woff2":"font/woff2", ".ttf":"font/ttf", ".eot":"application/vnd.ms-fontobject" };
const server = http.createServer(async (req, res) => { try {
  const p = decodeURIComponent(new URL(req.url, "http://x").pathname); let f;
  if (p === "/" || p === "/harness.html") f = join(here, "harness.html");
  else if (p.startsWith("/model/")) f = join(MODELS, normalize(p.slice("/model/".length)).replace(/^[/\\]+/, ""));
  else f = join(WEB, normalize(p).replace(/^[/\\]+/, ""));
  const b = await readFile(f); res.writeHead(200, { "content-type": MIME[extname(f)] || "application/octet-stream" }); res.end(b);
} catch { res.writeHead(404).end("nf"); } });
await new Promise(r => server.listen(0, "127.0.0.1", r));
const port = server.address().port;
const files = (await readdir(MODELS)).filter(f => f.endsWith(".bpmn"));
const withDI = []; for (const f of files) if ((await readFile(join(MODELS, f), "utf8")).includes("BPMNDiagram")) withDI.push(basename(f, ".bpmn"));
const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
const page = await browser.newPage({ deviceScaleFactor: 2 });
for (const m of withDI.sort()) {
  await page.goto(`http://127.0.0.1:${port}/harness.html?model=${m}`);
  await page.waitForFunction(() => window.__ready === true || window.__error, null, { timeout: 15000 });
  const err = await page.evaluate(() => window.__error || null); if (err) { console.log(m, "ERR", err.slice(0, 120)); continue; }
  const box = await page.evaluate(() => { const g = document.querySelector("#canvas svg .viewport"); const r = g.getBoundingClientRect(); return { x:r.x, y:r.y, width:r.width, height:r.height }; });
  const pad = 20;
  await page.screenshot({ path: join(OUT, m + ".png"), clip: { x: Math.max(0, box.x-pad), y: Math.max(0, box.y-pad), width: box.width+pad*2, height: box.height+pad*2 } });
  console.log(m, "OK");
}
await browser.close(); server.close();
