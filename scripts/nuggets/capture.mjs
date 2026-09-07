#!/usr/bin/env node
// Re-take every screenshot the handbook's training nuggets are built from, and
// rebuild the #nug-data block that drives them.
//
// Why this exists: the nuggets teach by showing the product, so their pictures
// are captures of a running Atlas rather than drawings. That buys recognition
// and costs staleness — a shot of a UI that has since moved still renders, and
// a ring drawn on a button that moved still looks deliberate. Nothing throws.
// So the shots are not artifacts somebody once made; they are output, and this
// is the command that produces them.
//
//   node scripts/nuggets/capture.mjs        (or: make nuggets)
//
// It builds the current tree, runs it on a throwaway data directory with auth
// off, seeds it with this repo's own order-to-cash example, takes each shot in
// scenes.mjs, measures every named target out of the live page, writes the
// images as WebP under api/web/nuggets/, and rewrites the data block in
// api/web/handbuch.html. Then it stops the server and deletes the data.
//
// Coordinates are never typed by hand. A scene says "highlight the Deploy
// button"; what that is in percentages of the image comes from the element's
// own bounding box, so a moved button is re-measured rather than re-guessed.

import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRequire } from "node:module";
import { SHOTS, NUGGETS } from "./scenes.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..", "..");
const OUT = join(ROOT, "api", "web", "nuggets");
const HANDBOOK = join(ROOT, "api", "web", "handbuch.html");
const PORT = Number(process.env.PORT) || 8321;
const BASE = `http://127.0.0.1:${PORT}`;
// The capture viewport. Its aspect ratio has to match the one the handbook's
// .nug-stage declares, or the shots are letterboxed inside their own frame.
const W = 1400, H = 820, IMG_W = 1200;

const log = (...a) => console.log("nuggets:", ...a);
const die = (m) => { console.error("nuggets: " + m); process.exit(1); };

// ---------------------------------------------------------------- the server
function buildAtlas(dir) {
  log("building the current tree…");
  const bin = join(dir, "atlas");
  const r = spawnSync("go", ["build", "-o", bin, "./cmd/atlas"], { cwd: ROOT, stdio: "inherit" });
  if (r.status !== 0) die("go build failed");
  return bin;
}

async function waitReady(timeoutMs = 60000) {
  const until = Date.now() + timeoutMs;
  while (Date.now() < until) {
    try {
      const r = await fetch(`${BASE}/api/v1/info`);
      if (r.ok) return await r.json();
    } catch { /* not up yet */ }
    await new Promise((r) => setTimeout(r, 400));
  }
  die("the server did not become ready");
}

// ------------------------------------------------------------------- seeding
const api = async (path, init = {}) => {
  const r = await fetch(BASE + path, init);
  if (!r.ok) die(`${init.method || "GET"} ${path} → HTTP ${r.status}`);
  return r.status === 204 ? null : r.json();
};

// The shots have to show something. An empty engine photographs as a set of
// empty tables, which teaches nothing and makes every caption a lie.
async function seed() {
  log("seeding: deploying order-to-cash…");
  const bpmn = readFileSync(join(ROOT, "examples", "order-to-cash.bpmn"));
  const dep = await api("/api/v1/deployments", {
    method: "POST", headers: { "Content-Type": "application/xml" }, body: bpmn,
  });
  const key = dep.key;

  // Baskets on both sides of the "total > 100 EUR?" gateway, so the diagram
  // shows tokens on both branches rather than a single path.
  const carts = [
    { kdnr: "MT-1004", customerType: "Business", positions: [{ name: "Serverschrank", price: 1290, qty: 1 }, { name: "Patchkabel", price: 12.5, qty: 24 }] },
    { kdnr: "MT-1005", customerType: "Private", positions: [{ name: "BPMN-Buch", price: 24.9, qty: 1 }] },
    { kdnr: "MT-1006", customerType: "Business", positions: [{ name: "Lizenz Atlas", price: 4800, qty: 1 }] },
    { kdnr: "MT-1007", customerType: "Private", positions: [{ name: 'Monitor 27"', price: 329, qty: 2 }] },
    { kdnr: "MT-1008", customerType: "Business", positions: [{ name: "Schulung BPMN", price: 1850, qty: 3 }] },
  ];
  for (const variables of carts) {
    await api(`/api/v1/processes/${key}/instances`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ variables }),
    });
  }

  // One built-in process too: its user task carries a real form, and a task
  // detail pane with "this task has no form" is a poor thing to teach from.
  const procs = await api("/api/v1/processes");
  const withForm = procs.find((p) => p.startFormId);
  if (withForm) {
    await api(`/api/v1/processes/${withForm.key}/instances`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ variables: { vorname: "Tina", nachname: "Keller", abteilung: "Einkauf", rolle: "Sachbearbeitung" } }),
    });
  }
  await new Promise((r) => setTimeout(r, 3000));
  const tasks = await api("/api/v1/tasks");
  log(`seeded: ${carts.length} order instances, ${tasks.length ?? 0} open tasks`);
}

// ------------------------------------------------------------------ shooting
// Chromium encodes the WebP: Playwright only writes png and jpeg, and a png of
// a UI is three to five times the size for no visible gain. This keeps the
// script free of an image dependency.
async function toWebp(page, pngBuffer) {
  const dataUrl = "data:image/png;base64," + pngBuffer.toString("base64");
  const out = await page.evaluate(async ([src, width, quality]) => {
    const img = new Image();
    img.src = src;
    await img.decode();
    const c = document.createElement("canvas");
    c.width = width;
    c.height = Math.round(img.height * width / img.width);
    c.getContext("2d").drawImage(img, 0, 0, c.width, c.height);
    return c.toDataURL("image/webp", quality);
  }, [dataUrl, IMG_W, 0.84]);
  if (!out.startsWith("data:image/webp")) die("this Chromium cannot encode WebP");
  return Buffer.from(out.split(",")[1], "base64");
}

async function capture(page) {
  const measured = {};
  for (const shot of SHOTS) {
    await page.goto(BASE + "/" + shot.route, { waitUntil: "networkidle" });
    await page.waitForTimeout(shot.wait ?? 2200);
    if (shot.act) await shot.act(page);

    for (const [name, sel] of Object.entries(shot.targets || {})) {
      const el = page.locator(sel).first();
      if (!(await el.count())) die(`shot "${shot.id}": nothing matches target "${name}" (${sel})`);
      const r = await el.boundingBox();
      if (!r) die(`shot "${shot.id}": target "${name}" has no box — is it hidden?`);
      // Percentages of the viewport, which is also percentages of the image:
      // the shot is the viewport, scaled.
      (measured[shot.id] ||= {})[name] = {
        x: +(r.x / W * 100).toFixed(2), y: +(r.y / H * 100).toFixed(2),
        w: +(r.width / W * 100).toFixed(2), h: +(r.height / H * 100).toFixed(2),
      };
    }
    const png = await page.screenshot();
    writeFileSync(join(OUT, shot.id + ".webp"), await toWebp(page, png));
    const marks = Object.keys(shot.targets || {}).length;
    log(`  ${shot.id}${marks ? ` (${marks} target${marks > 1 ? "s" : ""})` : ""}`);
  }
  return measured;
}

// ------------------------------------------------------------- the data block
// A scene names a target; this turns the name into the rectangle that was just
// measured, and puts the cursor in the middle of it.
function buildData(measured) {
  const nuggets = NUGGETS.map((n) => ({
    id: n.id,
    ...(n.role ? { role: n.role } : {}),
    title: n.title,
    lead: n.lead,
    scenes: n.scenes.map((s, i) => {
      const out = { t: s.t, cap: s.cap, img: s.img };
      if (s.focus) {
        const box = measured[s.img]?.[s.focus];
        if (!box) die(`${n.id}#${i}: no measurement for ${s.img}.${s.focus}`);
        out.focus = box;
        out.at = { x: +(box.x + box.w / 2).toFixed(2), y: +(box.y + box.h / 2).toFixed(2) };
      }
      if (s.tap) {
        if (!out.at) die(`${n.id}#${i}: taps but names no target to tap`);
        out.tap = true;
      }
      return out;
    }),
  }));
  return JSON.stringify({ nuggets }, null, 0);
}

function writeHandbook(json) {
  const open = '<script type="application/json" id="nug-data">';
  let s = readFileSync(HANDBOOK, "utf8");
  const i = s.indexOf(open);
  if (i < 0) die("no #nug-data block in handbuch.html");
  const j = s.indexOf("</script>", i);
  s = s.slice(0, i) + open + json + s.slice(j);
  writeFileSync(HANDBOOK, s);
  log(`wrote ${(json.length / 1024).toFixed(1)} KB into handbuch.html`);
}

// --------------------------------------------------------------------- main
const dir = mkdtempSync(join(tmpdir(), "atlas-nuggets-"));
let server;
try {
  const bin = buildAtlas(dir);
  mkdirSync(OUT, { recursive: true });
  server = spawn(bin, ["serve", "--data-dir", join(dir, "data"), "--auth=false",
    "--addr", `127.0.0.1:${PORT}`], { stdio: ["ignore", "pipe", "pipe"] });
  server.on("error", (e) => die("could not start the server: " + e.message));
  const info = await waitReady();
  log(`serving ${info.product} ${info.version} on ${BASE}`);

  await seed();

  // Playwright is a devDependency of e2e/, not of the repo root, so resolve it
  // from there rather than expecting a second copy beside this script.
  let chromium;
  try {
    const req = createRequire(join(ROOT, "e2e", "package.json"));
    const mod = await import(pathToFileURL(req.resolve("@playwright/test")).href);
    // The package is CommonJS, so its named exports arrive under default when
    // it is pulled in with import().
    chromium = mod.chromium ?? mod.default?.chromium;
  } catch (e) {
    die("@playwright/test could not be loaded (" + e.message + ") — run `npm ci` in e2e/ first");
  }
  if (!chromium) die("@playwright/test loaded but exposes no chromium");
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: W, height: H } });
  log(`taking ${SHOTS.length} shots…`);
  const measured = await capture(page);
  await browser.close();

  writeHandbook(buildData(measured));
  log("done — review the diff, then commit the images and the handbook together");
} finally {
  if (server) server.kill("SIGTERM");
  rmSync(dir, { recursive: true, force: true });
}
