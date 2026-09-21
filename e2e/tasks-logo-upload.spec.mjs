// Picking a logo in a task form (api/web/app.js).
//
// A form-js file picker holds the chosen File in the browser and nothing else, so
// completing the task has to do something with it. Until now there was one answer:
// read it as text into `csvText` for the CSV import (ADR-0087). That answer does
// not fit an image, and the obvious extension — base64 into a process variable —
// is the one this must not do. engine/budget.go says why in the comment on
// DefaultMaxVariable: past a megabyte "it is a document, and a document in a
// token's scope is rewritten into the log on every touch". A logo is capped at half
// a megabyte, about 683 KB once base64 has grown it, and every step the process
// takes afterwards would write it into the log again.
//
// So the image goes straight to the catalogue's own logo endpoint, and the task's
// variables say which catalogue. These drive the REAL app shell against a mocked
// /api/v1 and check exactly that: what is PUT, what is NOT in the completion, and
// what happens when the upload fails.
import { test, expect } from "@playwright/test";

const listing = (items) => ({ items, total: items.length, totalExact: true, truncated: false });

// Two tasks with a file field. One names a catalogue, one does not — which is the
// whole of how the page tells a logo from a CSV.
const TASKS = [
  {
    key: 201, processInstanceKey: 9101, processDefKey: 1, processId: "proc_produkt_erfassung",
    elementId: "theme_erfassen", name: "Erscheinungsbild festlegen", formId: "pe-theme", priority: 50,
  },
  {
    key: 202, processInstanceKey: 9102, processDefKey: 2, processId: "proc_import",
    elementId: "upload", name: "CSV hochladen", formId: "csv-upload", priority: 50,
  },
];

const FORMS = {
  "pe-theme": {
    schema: {
      type: "default", id: "pe-theme",
      components: [
        { key: "akzentfarbe", type: "textfield", label: "Akzentfarbe" },
        { key: "logo", type: "filepicker", label: "Logo", accept: ".png,.svg" },
      ],
    },
  },
  "csv-upload": {
    schema: {
      type: "default", id: "csv-upload",
      components: [{ key: "csvText", type: "filepicker", label: "CSV-Datei", accept: ".csv" }],
    },
  },
};

// The instance variables each task is filled from. logoKatalog is the one the form
// does not render and completion still needs.
const VARS = {
  9101: { akzentfarbe: "#3355ff", katalogId: "cat_arbeitsplatz", logoKatalog: "cat_arbeitsplatz" },
  9102: {},
};

function installMock(page, sent, opts = {}) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    if (req.method() === "PUT" || req.method() === "POST") {
      let body = null;
      try { body = JSON.parse(req.postData() || "null"); } catch { body = req.postData(); }
      sent.push({ method: req.method(), path, body, contentType: req.headers()["content-type"] });
      if (path.endsWith("/logo") && opts.logoFails) {
        return route.fulfill({ status: 413, json: { error: "the logo exceeds the 524288 byte limit" } });
      }
      return route.fulfill({ status: 204, body: "" });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: listing(TASKS) });
    if (path.endsWith("/api/v1/approvals")) return route.fulfill({ json: listing([]) });
    const form = path.match(/\/api\/v1\/forms\/(.+)$/);
    if (form) return route.fulfill({ json: FORMS[decodeURIComponent(form[1])] || { schema: {} } });
    const vars = path.match(/\/api\/v1\/instances\/(\d+)\/variables$/);
    if (vars) return route.fulfill({ json: VARS[vars[1]] || {} });
    return route.fulfill({ json: [] });
  });
}

async function openTask(page, key) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
  await page.locator(`.tasks-item[data-key="${key}"]`).click();
  // Attached and not visible: form-js hides the native input behind its own button
  // (class fjs-hidden), and setInputFiles drives the hidden one directly.
  await expect(page.locator("#task-form input[type=file]")).toBeAttached({ timeout: 15000 });
}

// A one-pixel PNG, as real bytes: the page reads the File's type and length, so a
// text file with a .png name would prove nothing about either.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64");

async function pick(page, name, type, data) {
  await page.locator("#task-form input[type=file]")
    .setInputFiles({ name, mimeType: type, buffer: data });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__sent = [];
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

test("the image goes to the catalogue the task names, under its own media type", async ({ page }) => {
  installMock(page, page.__sent);
  await openTask(page, 201);
  await pick(page, "marke.png", "image/png", PNG);
  await page.locator("#task-complete").click();

  await expect.poll(() => page.__sent.length).toBeGreaterThan(1);
  const put = page.__sent.find((r) => r.method === "PUT");
  expect(put, "no PUT was sent").toBeTruthy();
  expect(put.path).toBe("/api/v1/catalogs/cat_arbeitsplatz/logo");
  // The endpoint decides by Content-Type which format it is validating against, so
  // sending the file's own type is not a detail.
  expect(put.contentType).toBe("image/png");
});

test("the bytes are not in the completion", async ({ page }) => {
  // The property the whole design rests on. A base64 logo in the variables would
  // pass every assertion above and still be the defect: half a megabyte rewritten
  // into the write-ahead log at every step the process takes afterwards.
  installMock(page, page.__sent);
  await openTask(page, 201);
  await pick(page, "marke.png", "image/png", PNG);
  await page.locator("#task-complete").click();

  await expect.poll(() => page.__sent.some((r) => r.path.endsWith("/complete"))).toBe(true);
  const complete = page.__sent.find((r) => r.path.endsWith("/complete"));
  const vars = (complete.body || {}).variables || {};
  expect(vars.csvText, "an image was read as CSV text").toBeUndefined();
  for (const [key, value] of Object.entries(vars)) {
    if (typeof value === "string" && value.length > 512) {
      throw new Error(`variable ${key} carries ${value.length} characters — the image is in the completion`);
    }
  }
  // What it does carry is the form's own field.
  expect(vars.akzentfarbe).toBe("#3355ff");
});

test("a failed upload leaves the task open and says why", async ({ page }) => {
  // Completing while the logo did not arrive is a process that believes the
  // catalogue is branded. The completion must not be sent at all.
  installMock(page, page.__sent, { logoFails: true });
  await openTask(page, 201);
  await pick(page, "riesig.png", "image/png", PNG);
  await page.locator("#task-complete").click();

  await expect(page.locator("#toast")).toContainText("Logo konnte nicht hochgeladen werden");
  await expect(page.locator("#toast")).toContainText("524288");
  expect(page.__sent.some((r) => r.path.endsWith("/complete")),
    "the task was completed although its logo failed").toBe(false);
  // And the task is still there to try again with.
  await expect(page.locator("#task-form input[type=file]")).toBeAttached();
});

test("a task that names no catalogue still uploads a CSV as text", async ({ page }) => {
  // The other answer, unchanged. Without this the new branch would be a silent
  // regression of the import ADR-0087 describes.
  installMock(page, page.__sent);
  await openTask(page, 202);
  await pick(page, "zeilen.csv", "text/csv", Buffer.from("a;b\n1;2\n", "utf8"));
  await page.locator("#task-complete").click();

  await expect.poll(() => page.__sent.some((r) => r.path.endsWith("/complete"))).toBe(true);
  const complete = page.__sent.find((r) => r.path.endsWith("/complete"));
  expect((complete.body || {}).variables.csvText).toBe("a;b\n1;2\n");
  expect(page.__sent.some((r) => r.method === "PUT"), "a CSV was PUT as a logo").toBe(false);
});

test("switching tasks does not carry the catalogue to the next one", async ({ page }) => {
  // The variables a form was filled from are kept beside it, so completion can
  // read the one the form does not render. This states that they belong to the
  // task on screen and not to the one before it.
  //
  // It passes today even with the teardown removed, and that is worth saying: a
  // mount always overwrites them, so the stale value cannot currently be reached.
  // The case is here because it is the one a later change would break silently —
  // the first read of these variables from outside a mounted form turns this from
  // a tautology into the assertion that stops somebody's logo landing on another
  // catalogue.
  installMock(page, page.__sent);
  await openTask(page, 201);
  await expect(page.locator("#task-form input[type=file]")).toBeAttached();

  // Now the task that names no catalogue.
  await page.locator('.tasks-item[data-key="202"]').click();
  await expect(page.locator("#task-form input[type=file]")).toBeAttached();
  await pick(page, "zeilen.csv", "text/csv", Buffer.from("a;b\n1;2\n", "utf8"));
  await page.locator("#task-complete").click();

  await expect.poll(() => page.__sent.some((r) => r.path.endsWith("/complete"))).toBe(true);
  expect(page.__sent.some((r) => r.method === "PUT"),
    "the previous task's catalogue was still in scope and the CSV was PUT as its logo").toBe(false);
  const complete = page.__sent.find((r) => r.path.endsWith("/complete"));
  expect((complete.body || {}).variables.csvText).toBe("a;b\n1;2\n");
});
