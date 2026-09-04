// End-to-end coverage for a user task's documentation in the Tasks app (api/web/app.js,
// ADR-0025 amended). The element's <bpmn:documentation> is the modeler's instruction for
// whoever picks the task up, so the detail pane leads with it — above the metadata rows
// and the form. The API side is covered by the Go suite; this drives the REAL app shell
// against a mocked /api/v1 to verify the UI wiring: the block appears for a documented
// task, renders the author's Markdown, and is absent (not stale) for one that carries
// none.
//
// The instruction is rendered as Markdown (ADR-0250), so a
// checklist arrives as a list. markdown.spec.mjs covers the renderer itself — including
// that its output cannot script the page; what is checked here is the wiring: that this
// surface renders rather than escapes, and that prose written before Markdown existed
// still reads the way its author left it.
import { test, expect } from "@playwright/test";

// Plain prose, exactly as it was written before the field understood Markdown: two
// paragraphs, no markers anywhere.
const DOC = "Vergleiche die Angaben mit den Anlagen.\n\nFehlen Unterlagen, lehne ab und informiere den Antragsteller.";

// The same instruction as an author writes it today.
const MARKED_UP = [
  "**Vor der Freigabe** prüfen:",
  "",
  "- Ausweis liegt bei",
  "- Betrag stimmt mit `betrag` überein",
  "",
  "## Wenn etwas fehlt",
  "",
  "> Im Zweifel ablehnen.",
].join("\n");

const TASKS = [
  {
    key: 101, processInstanceKey: 9001, elementInstanceKey: 9101, processDefKey: 1,
    processId: "freigabe", elementId: "review", name: "Antrag prüfen",
    documentation: DOC, priority: 50,
  },
  {
    key: 102, processInstanceKey: 9001, elementInstanceKey: 9102, processDefKey: 1,
    processId: "freigabe", elementId: "sign", name: "Unterschreiben", priority: 50,
  },
  {
    key: 103, processInstanceKey: 9001, elementInstanceKey: 9103, processDefKey: 1,
    processId: "freigabe", elementId: "check", name: "Betrag prüfen",
    documentation: MARKED_UP, priority: 50,
  },
];

// installMock answers /auth/me as single-user so the app never gates, serves the two
// tasks above, and returns a benign [] for every other call the shell makes on boot.
function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: TASKS });
    return route.fulfill({ json: [] });
  });
}

// bootTasks loads the real shell straight into the inbox and waits for the list.
async function bootTasks(page) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
}

const select = async (page, key) => page.locator(`.tasks-item[data-key="${key}"]`).click();

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
});

test("a documented task leads its detail with the modeler's instruction", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  const doc = page.locator(".tasks-doc");
  await expect(doc).toBeVisible();
  await expect(doc.locator("h2")).toHaveText("What to do");
  await expect(doc.locator(".md p").first()).toHaveText("Vergleiche die Angaben mit den Anlagen.");
  await expect(doc.locator(".md p").nth(1)).toContainText("Fehlen Unterlagen, lehne ab");
  expect(page.__errors).toEqual([]);
});

test("the instruction is read before the metadata and the form", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  // The block sits between the title and the field rows — the order the assignee reads.
  const order = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll(".tasks-detail-head, .tasks-doc, .tasks-fields")];
    return nodes.map((n) => n.className.split(" ")[0]);
  });
  expect(order).toEqual(["tasks-detail-head", "tasks-doc", "tasks-fields"]);

  // The author's paragraph break survives — prose, not a squashed single line. It is a
  // second <p> now rather than a preserved newline, which is the same promise kept by
  // the renderer instead of by `white-space`.
  await expect(page.locator(".tasks-doc .md p")).toHaveCount(2);
  expect(page.__errors).toEqual([]);
});

test("an undocumented task shows no block, not the previous task's text", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);
  await expect(page.locator(".tasks-doc")).toBeVisible();

  await select(page, 102);
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Unterschreiben");
  await expect(page.locator(".tasks-doc")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a marked-up instruction reaches the assignee as structure, not as markers", async ({ page }) => {
  await bootTasks(page);
  await select(page, 103);

  const doc = page.locator(".tasks-doc");
  await expect(doc.locator(".md strong")).toHaveText("Vor der Freigabe");
  await expect(doc.locator(".md ul li")).toHaveCount(2);
  await expect(doc.locator(".md ul li").nth(1)).toContainText("Betrag stimmt mit betrag überein");
  await expect(doc.locator(".md code")).toHaveText("betrag");
  await expect(doc.locator(".md blockquote")).toContainText("Im Zweifel ablehnen.");
  await expect(doc.locator(".md h2")).toHaveText("Wenn etwas fehlt");
  const heading = await doc.locator(".md h2").evaluate((el) => getComputedStyle(el).textTransform);
  expect(heading, "the modeler's heading is not the block's label").toBe("none");

  // The author's own heading must not be dressed as the block's label: "WHAT TO DO" is
  // the app talking, and a heading inside the instruction is the modeler talking.
  const label = await doc.locator("> h2").evaluate((el) => getComputedStyle(el).textTransform);
  expect(label).toBe("uppercase");

  // None of the markers themselves are left on screen for the person doing the work.
  const shown = await doc.locator(".md").innerText();
  expect(shown).not.toContain("**");
  expect(shown).not.toContain("`");
  expect(shown).not.toContain("> ");
  expect(page.__errors).toEqual([]);
});
