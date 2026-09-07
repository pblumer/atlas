// End-to-end coverage for what happens when completing a task fails validation
// (api/web/app.js, ADR-0028).
//
// The detail pane has two tabs, and the form is mounted even while the Process tab is
// showing — deliberately, so Complete has its data either way. The consequence nobody
// had looked at: pressing Complete from the Process tab validated a form the person
// could not see, and answered "Please fix the highlighted fields". Highlighted where?
// The fields are on the other tab. There was no way to find out what was wrong short
// of guessing to click Form.
//
// This drives the REAL app shell against a mocked /api/v1, with a real vendored form-js
// form, and holds the three things that make the refusal actionable: it puts the form in
// front of the person, it says which field, and it stays readable when a blank form
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
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: [TASK] });
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
