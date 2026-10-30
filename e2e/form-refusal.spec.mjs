// End-to-end coverage for what the console says when a form refuses to submit
// (api/web/app.js, ADR-0028).
//
// Two screens mount a form-js form: completing a task and starting a process. Both
// used to answer a failed validation with "Please fix the highlighted fields", which
// assumes you can see the highlighting. In the inbox you could not — the form stays
// mounted while the Process tab is showing (deliberately, so Complete has its data
// either way), so pressing Complete from there marked fields on a pane you were not
// looking at, and there was no way to find out short of guessing to click Form.
//
// This drives the REAL app shell against a mocked /api/v1, with a real vendored form-js
// form, and holds what makes the refusal actionable on both screens: it puts the form
// in front of the person, it says which field, and it stays readable when a blank form
// refuses a dozen of them at once.
import { test, expect } from "@playwright/test";

const TASK = {
  key: 501, processInstanceKey: 9001, elementInstanceKey: 9501, processDefKey: 1,
  processId: "proc_kredit", elementId: "pruefen", name: "Antrag prüfen",
  formId: "kredit", priority: 50,
};

// One required field left empty and one already filled, so a failed submit has exactly
// one thing to name.
const FORM = {
  schema: {
    type: "default",
    id: "kredit",
    components: [
      { type: "textfield", key: "vorname", label: "Vorname" },
      { type: "textfield", key: "iban", label: "IBAN", validate: { required: true } },
    ],
  },
};

// Six required fields, all empty: what a blank form does. The message has to stay
// something a person can read.
const BIG_FORM = {
  schema: {
    type: "default",
    id: "kredit",
    components: ["Vorname", "Nachname", "IBAN", "Betrag", "Zweck", "Laufzeit"].map((label, i) => ({
      type: "textfield", key: "f" + i, label, validate: { required: true },
    })),
  },
};

function installMock(page, form = FORM) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    // The task list answers with a page, not a bare array (ADR-0378).
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: { items: [TASK], truncated: false, nextCursor: null } });
    if (path.includes("/api/v1/forms/")) return route.fulfill({ json: form });
    if (path.includes("/variables")) return route.fulfill({ json: { vorname: "Hans" } });
    return route.fulfill({ json: [] });
  });
}

async function openTask(page) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
  await page.locator('.tasks-item[data-key="501"]').click();
  // The form is mounted before anything else is asserted, so a failed submit below is
  // the form refusing rather than the form not being there yet.
  await expect(page.locator("#task-form .fjs-form")).toBeVisible({ timeout: 15000 });
}

test("completing from the Process tab shows the form it is complaining about", async ({ page }) => {
  installMock(page);
  await openTask(page);

  // Go to the Process tab, which is where the assignee checks what the instance carries
  // before deciding — and where this used to become a dead end.
  await page.locator('#task-dtabs button[data-dtab="process"]').click();
  await expect(page.locator("#pane-form")).toBeHidden();

  await page.locator("#task-complete").click();

  // The refusal has to be actionable: the form is what the person is now looking at.
  await expect(page.locator("#pane-form")).toBeVisible();
  await expect(page.locator('#task-dtabs button[data-dtab="form"]')).toHaveClass(/active/);
  // And it names the field, rather than pointing at a highlight.
  await expect(page.locator("#toast")).toContainText("IBAN");
});

test("the refusal names the field even when the form is already showing", async ({ page }) => {
  installMock(page);
  await openTask(page);

  await page.locator("#task-complete").click();
  await expect(page.locator("#toast")).toContainText("IBAN");
  // Still on the form; nothing moved out from under the person.
  await expect(page.locator("#pane-form")).toBeVisible();
});

test("a blank form is answered with the first few fields and a count", async ({ page }) => {
  installMock(page, BIG_FORM);
  await openTask(page);

  await page.locator("#task-complete").click();

  const toast = page.locator("#toast");
  await expect(toast).toContainText("Vorname");
  await expect(toast).toContainText("Betrag"); // the fourth, and the last one named
  await expect(toast).toContainText("und 2 weitere Felder");
  await expect(toast).not.toContainText("Laufzeit");
});

test("a complete with every required field filled is not refused", async ({ page }) => {
  installMock(page);
  const completed = [];
  page.route("**/api/v1/tasks/501/complete", async (route) => {
    completed.push(route.request().postDataJSON());
    return route.fulfill({ json: { key: 501 } });
  });
  await openTask(page);

  // form-js renders no name attribute — the field is reached the way the person
  // reaches it, by the label above it.
  await page.locator("#task-form").getByLabel("IBAN").fill("CH9300762011623852957");
  await page.locator("#task-complete").click();

  await expect.poll(() => completed.length, { timeout: 15000 }).toBe(1);
  expect(completed[0].variables.iban).toBe("CH9300762011623852957");
});

// ---------- Starting a process by its start form (#/tasks/start) -------------------
//
// One pane here, so there is nothing to bring forward — but "which field?" is the same
// question, and the two screens share the answer.

const PROC = {
  key: 77, processId: "proc_kredit", name: "Kreditantrag", version: 3,
  startFormId: "kredit", executable: true,
};

function installStartMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/processes")) return route.fulfill({ json: [PROC] });
    if (path.includes("/api/v1/forms/")) return route.fulfill({ json: FORM });
    return route.fulfill({ json: [] });
  });
}

async function openStartForm(page) {
  await page.goto("/index.html#/tasks/start");
  await page.locator('.tasks-item[data-key="77"]').click();
  await expect(page.locator("#start-form .fjs-form")).toBeVisible({ timeout: 15000 });
}

test("a start form that is not filled in names the field, and starts nothing", async ({ page }) => {
  installStartMock(page);
  const started = [];
  await page.route("**/api/v1/processes/77/instances", async (route) => {
    started.push(route.request().postDataJSON());
    return route.fulfill({ json: { key: 1234 } });
  });
  await openStartForm(page);

  await page.locator("#start-go").click();

  await expect(page.locator("#toast")).toContainText("IBAN");
  expect(started).toHaveLength(0);
});

test("a filled start form starts the process with what was entered", async ({ page }) => {
  installStartMock(page);
  const started = [];
  await page.route("**/api/v1/processes/77/instances", async (route) => {
    started.push(route.request().postDataJSON());
    return route.fulfill({ json: { key: 1234 } });
  });
  await openStartForm(page);

  await page.locator("#start-form").getByLabel("IBAN").fill("CH9300762011623852957");
  await page.locator("#start-go").click();

  await expect.poll(() => started.length, { timeout: 15000 }).toBe(1);
  expect(started[0].variables.iban).toBe("CH9300762011623852957");
});

// MFE0 / ADR-0514: render actual form-js fields, not just the responsive
// list/detail placeholders. Browser viewport emulation cannot certify real
// iOS/Android keyboards, 200% browser zoom, screen readers or foldable hinges.
async function expectNoDocumentOverflow(page) {
  await expect.poll(() => page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth
  )).toBeLessThanOrEqual(1);
}

test("MFE0 phone task: failed validation preserves edits through an adaptive layout change", async ({ page }) => {
  installMock(page);
  const completions = [];
  await page.route("**/api/v1/tasks/501/complete", async (route) => {
    completions.push(route.request().postDataJSON());
    await route.fulfill({ json: { key: 501 } });
  });
  await page.setViewportSize({ width: 320, height: 640 });
  await openTask(page);

  const form = page.locator("#task-form");
  await form.getByLabel("Vorname").fill("Lena");
  await page.locator("#task-complete").click();
  await expect(page.locator("#toast")).toContainText("IBAN");
  expect(completions).toHaveLength(0); // Client-side validation never sent a command.
  await expect(form.getByLabel("Vorname")).toHaveValue("Lena");
  await expectNoDocumentOverflow(page);

  // Cross the existing ADR-0417 breakpoint in both directions without
  // remounting the form or silently discarding a partially entered task.
  await page.setViewportSize({ width: 1024, height: 768 });
  await expect(form.getByLabel("Vorname")).toHaveValue("Lena");
  await page.setViewportSize({ width: 320, height: 640 });
  await expect(form.getByLabel("Vorname")).toHaveValue("Lena");
  await form.getByLabel("IBAN").fill("CH9300762011623852957");
  await page.locator("#task-complete").click();
  await expect.poll(() => completions.length).toBe(1);
  expect(completions[0].variables.iban).toBe("CH9300762011623852957");
  expect(completions[0].variables.vorname).toBe("Lena");
});

test("MFE0 phone Start: the form and pending values survive changing orientation", async ({ page }) => {
  installStartMock(page);
  const starts = [];
  await page.route("**/api/v1/processes/77/instances", async (route) => {
    starts.push(route.request().postDataJSON());
    await route.fulfill({ json: { key: 1234 } });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await openStartForm(page);

  const form = page.locator("#start-form");
  await form.getByLabel("IBAN").fill("CH9300762011623852957");
  await expectNoDocumentOverflow(page);
  await page.setViewportSize({ width: 844, height: 390 });
  await expect(form.getByLabel("IBAN")).toHaveValue("CH9300762011623852957");
  await expectNoDocumentOverflow(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(form.getByLabel("IBAN")).toHaveValue("CH9300762011623852957");

  // A resize is presentation only: it must neither start the process nor
  // change the submitted form data.
  expect(starts).toHaveLength(0);
  await page.locator("#start-go").click();
  await expect.poll(() => starts.length).toBe(1);
  expect(starts[0].variables.iban).toBe("CH9300762011623852957");
});

test("MFE0 320px phone: the real form-js validation names missing required fields", async ({ page }) => {
  installMock(page, BIG_FORM);
  await page.setViewportSize({ width: 320, height: 640 });
  await openTask(page);
  await expectNoDocumentOverflow(page);
  await page.locator("#task-complete").click();
  const toast = page.locator("#toast");
  await expect(toast).toContainText("Vorname");
  await expect(toast).toContainText("und 2 weitere Felder");
  await expect(page.locator("#task-form .fjs-form")).toBeVisible();
  await expectNoDocumentOverflow(page);
});

// MFE0 / ADR-0514: schema-authored two-column layout. Form-js owns the
// schema and its data, while the Tasks/Start hosts must adapt it for a phone.
const TWO_COLUMN_FORM = {
  schema: {
    type: "default",
    id: "kredit",
    components: [
      { type: "textfield", key: "vorname", label: "Vorname", layout: { row: "Row_1", columns: 8 } },
      { type: "textfield", key: "iban", label: "IBAN", validate: { required: true },
        layout: { row: "Row_1", columns: 8 } },
    ],
  },
};

test("MFE0 two-column form-js task remains usable at 320px and keeps desktop layout", async ({ page }) => {
  installMock(page, TWO_COLUMN_FORM);
  await page.setViewportSize({ width: 320, height: 640 });
  await openTask(page);
  const fields = page.locator("#task-form");
  const first = fields.getByLabel("Vorname");
  const second = fields.getByLabel("IBAN");
  await expect(first).toBeVisible();
  await expect(second).toBeVisible();

  // Two fields in one schema-authored row must become vertically readable on
  // a phone, and both input boxes must remain inside the visible screen.
  const phoneFirst = await first.boundingBox();
  const phoneSecond = await second.boundingBox();
  expect(phoneSecond.y).toBeGreaterThanOrEqual(phoneFirst.y + phoneFirst.height - 1);
  expect(phoneFirst.x).toBeGreaterThanOrEqual(0);
  expect(phoneSecond.x).toBeGreaterThanOrEqual(0);
  expect(phoneFirst.x + phoneFirst.width).toBeLessThanOrEqual(321);
  expect(phoneSecond.x + phoneSecond.width).toBeLessThanOrEqual(321);
  await expectNoDocumentOverflow(page);

  await first.fill("Lena");
  await second.fill("CH9300762011623852957");
  await page.setViewportSize({ width: 1024, height: 768 });
  await expect(first).toHaveValue("Lena");
  await expect(second).toHaveValue("CH9300762011623852957");
  const wideFirst = await first.boundingBox();
  const wideSecond = await second.boundingBox();
  expect(Math.abs(wideFirst.y - wideSecond.y)).toBeLessThanOrEqual(2);
  await expectNoDocumentOverflow(page);

  await page.setViewportSize({ width: 320, height: 640 });
  await expect(first).toHaveValue("Lena");
  await expect(second).toHaveValue("CH9300762011623852957");
});

test("MFE0 two-column Process Start fields stack on a phone without losing input", async ({ page }) => {
  const form = TWO_COLUMN_FORM;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/processes")) return route.fulfill({ json: [PROC] });
    if (path.includes("/api/v1/forms/")) return route.fulfill({ json: form });
    return route.fulfill({ json: [] });
  });
  await page.setViewportSize({ width: 320, height: 640 });
  await openStartForm(page);
  const first = page.locator("#start-form").getByLabel("Vorname");
  const second = page.locator("#start-form").getByLabel("IBAN");
  const before = await first.boundingBox();
  const after = await second.boundingBox();
  expect(after.y).toBeGreaterThanOrEqual(before.y + before.height - 1);
  expect(after.x + after.width).toBeLessThanOrEqual(321);
  await expectNoDocumentOverflow(page);

  await first.fill("Lena");
  await page.setViewportSize({ width: 1024, height: 768 });
  await expect(first).toHaveValue("Lena");
  await page.setViewportSize({ width: 320, height: 640 });
  await expect(first).toHaveValue("Lena");
});
