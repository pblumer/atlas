// Tiny static server for the token-simulation e2e test. It serves the repo's real web
// assets (../api/web) at the root — so the harness loads the production bpmn-js bundle and
// token-simulation.js — plus the two test-only files (harness.html, model.bpmn) that live
// here in e2e/ and must NOT be embedded into the app (api/web is go:embed'd).
import http from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize, sep } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url)).replace(/[/\\]+$/, "");
const WEB = join(here, "..", "api", "web");
const PORT = Number(process.env.PORT) || 8199;

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".bpmn": "application/xml; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
  ".eot": "application/vnd.ms-fontobject",
};

const send = (res, code, body, type) => {
  res.writeHead(code, { "content-type": type || "text/plain; charset=utf-8" });
  res.end(body);
};

const server = http.createServer(async (req, res) => {
  let file;
  try {
    const p = decodeURIComponent(new URL(req.url, "http://localhost").pathname);
    if (p === "/" || p === "/harness.html") file = join(here, "harness.html");
    else if (p.endsWith("-harness.html") || p.endsWith(".bpmn")) {
      // Test-only harness pages and models live here in e2e/ (never in api/web, which
      // is go:embed'd into the binary). Block path traversal.
      const rel = normalize(p).replace(/^([/\\])+/, "");
      file = join(here, rel);
      if (file !== here && !file.startsWith(here + sep)) return send(res, 403, "forbidden");
    } else {
      // Everything else resolves inside api/web; block path traversal.
      const rel = normalize(p).replace(/^([/\\])+/, "");
      file = join(WEB, rel);
      if (file !== WEB && !file.startsWith(WEB + sep)) return send(res, 403, "forbidden");
    }
    const buf = await readFile(file);
    send(res, 200, buf, MIME[extname(file)] || "application/octet-stream");
  } catch {
    send(res, 404, "not found");
  }
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`e2e static server on http://127.0.0.1:${PORT}`);
});
