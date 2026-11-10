// A task form that hands a document out and takes a corrected one back (ADR-0548, S2):
// the file correction task of a quality check. A file field whose variable already
// names a document offers it for download; marked read-only it takes nothing; the
// second field uploads the corrected file and completes with its reference. These
// drive the REAL app shell against a mocked /api/v1.
import { test, expect } from "@playwright/test";

const FAILURES = { $document: "doc_" + "f".repeat(26), name: "mitarbeitende.failures.csv", contentType: "text/csv", size: 2048 };
const UPLOADED = "doc_" + "u".repeat(26);

const listing = (items) => ({ items, total: items.length, totalExact: true, truncated: false });

const TASK = {
  key: 301, processInstanceKey: 9301, processDefKey: 3, processId: "mitarbeitende-korrigieren",
  elementId: "datei_korrigieren", name: "Datei korrigieren", formId: "datei-korrigieren", priority: 50,
};

const FORM = {
  schema: {
    type: "default", id: "datei-korrigieren",
    components: [
      { key: "befundeDatei", type: "filepicker", label: "Befunde", readonly: true },
      { key: "korrekturen", type: "filepicker", label: "Korrigierte Datei", accept: ".csv" },
      { key: "begruendung", type: "textfield", label: "Begründung" },
    ],
  },
};

function installMock(page, sent) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    if (req.method() === "POST") {
      let body = null;
      try { body = JSON.parse(req.postData() || "null"); } catch { body = req.postData(); }
      sent.push({ path, body });
      if (path === "/api/v1/documents") {
        return route.fulfill({ status: 201, json: { $document: UPLOADED, name: url.searchParams.get("name"), contentType: "text/csv", size: 10 } });
      }
      return route.fulfill({ status: 204, body: "" });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: listing([TASK]) });
    if (path.endsWith("/api/v1/approvals")) return route.fulfill({ json: listing([]) });
    if (path.match(/\/api\/v1\/forms\//)) return route.fulfill({ json: FORM });
    if (path.match(/\/api\/v1\/instances\/\d+\/variables$/)) return route.fulfill({ json: { befundeDatei: FAILURES, begruendung: "" } });
    return route.fulfill({ json: [] });
  });
}

test("the failures are offered for download, and the corrected file goes up under its own key", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const sent = [];
  installMock(page, sent);
  await page.goto("/index.html#/tasks");
  await page.locator('.tasks-item[data-key="301"]').click();
  await expect(page.locator("#task-form input[type=file]")).toHaveCount(2, { timeout: 15000 });

  const link = page.locator("#task-form .task-doc a");
  await expect(link).toHaveCount(1);
  await expect(link).toHaveAttribute("href", "/api/v1/documents/" + FAILURES.$document);
  await expect(link).toContainText("mitarbeitende.failures.csv");
  // Read-only: the download field takes no file.
  await expect(page.locator("#task-form input[type=file]").first()).toBeDisabled();

  await page.locator("#task-form input[type=file]").nth(1)
    .setInputFiles({ name: "korrigiert.csv", mimeType: "text/csv", buffer: Buffer.from("a;_row\n1;2\n", "utf8") });
  await page.locator("#task-complete").click();

  await expect.poll(() => sent.some((r) => r.path.endsWith("/complete"))).toBe(true);
  const upload = sent.find((r) => r.path === "/api/v1/documents");
  expect(upload && upload.body).toBe("a;_row\n1;2\n");
  const vars = sent.find((r) => r.path.endsWith("/complete")).body.variables;
  expect(vars.korrekturen.$document).toBe(UPLOADED);
  // The document handed out is not written back, as a reference or otherwise.
  expect(vars.befundeDatei).toBeUndefined();
  expect(errors).toEqual([]);
});
