// The Data area's Import button, driven through the real Console (ADR-0232).
//
// The case behind it, reported from the running server: the button was there and a
// click did nothing at all. Its handler called a helper that a later change to the
// Console had removed — a reference error thrown inside an async listener, which no
// Go test can see and which leaves no mark on the page. So the assertion that matters
// is the crudest one: a click opens the file dialog, and a chosen file produces the
// report. Everything after that is the flow itself.
import { test, expect } from "@playwright/test";

const XMI = `<?xml version="1.0" encoding="UTF-8"?>
<uml:Model xmlns:xmi="http://www.omg.org/spec/XMI/20131001"
           xmlns:uml="http://www.omg.org/spec/UML/20131001" xmi:id="_m" name="Sales">
  <packagedElement xmi:type="uml:Class" xmi:id="_order" name="Order"/>
  <packagedElement xmi:type="uml:Interface" xmi:id="_pay" name="Payable"/>
</uml:Model>`;

const PREVIEW = {
  format: "xmi",
  notes: [{ level: "dropped", element: "Payable", message: "Payable is a uml:Interface, which Atlas does not author." }],
  validation: { valid: true, findings: [] },
  preview: { name: "Sales", classes: [{ id: "c1", name: "Order" }], associations: [], stores: [] },
};

// stubAPI answers the Console's boot calls and records every import request, which is
// what pins the two-step contract: the preview and the import are the same call, and
// only the second one stores.
function stubAPI(page, applications, imports) {
  page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: false, user: null } }));
  page.route("**/api/v1/applications**", (route) => route.fulfill({ json: applications }));
  page.route("**/api/v1/infomodel/import", (route) => {
    const body = route.request().postDataJSON();
    imports.push(body);
    if (body.dryRun) return route.fulfill({ json: PREVIEW });
    return route.fulfill({
      json: {
        format: "xmi", notes: PREVIEW.notes,
        model: { id: "m-1", name: body.name, applicationId: body.applicationId, classes: 1, associations: 0 },
      },
    });
  });
}

const app = (id, name) => ({ id, name, myRole: "owner", protected: false });

async function openData(page) {
  await page.goto("/index.html#/data");
  await expect(page.locator('[data-act="import-im"]')).toBeVisible({ timeout: 20000 });
}

// chooseFile clicks Import and answers the file dialog. If the click throws instead of
// opening one, this is where the test fails — which is the whole point of it.
async function chooseFile(page, name = "sales.xmi") {
  const [chooser] = await Promise.all([
    page.waitForEvent("filechooser", { timeout: 10000 }),
    page.locator('[data-act="import-im"]').click(),
  ]);
  await chooser.setFiles({ name, mimeType: "application/xml", buffer: Buffer.from(XMI) });
}

test("the button opens a file dialog, and the file produces a report before anything is stored", async ({ page }) => {
  const imports = [];
  stubAPI(page, [app("app-1", "Sales")], imports);
  await openData(page);
  await chooseFile(page);

  const report = page.locator("#im-import-report");
  await expect(report).toBeVisible();
  await expect(report.locator("#im-import-counts")).toContainText("1 class");
  await expect(report.locator("#im-import-counts")).toContainText("Nothing is stored until you import");
  // The account of what the subset would not take is the substance of the report.
  await expect(report.locator("tbody tr[data-level='dropped']")).toHaveCount(1);
  await expect(report.locator("tbody tr")).toContainText("Payable");
  // The name the document carries is offered, not the file name.
  await expect(report.locator("#im-import-name")).toHaveValue("Sales");

  expect(imports).toHaveLength(1);
  expect(imports[0].dryRun).toBe(true);
  expect(imports[0].applicationId).toBe("app-1");
  expect(imports[0].document).toContain("uml:Model");
});

test("confirming stores the model, with the name the reader left in the field", async ({ page }) => {
  const imports = [];
  stubAPI(page, [app("app-1", "Sales")], imports);
  await openData(page);
  await chooseFile(page);

  await page.locator("#im-import-name").fill("Sales vocabulary");
  await page.locator("#im-import-report [data-import]").click();

  await expect(page.locator("#im-import-report")).toHaveCount(0);
  await expect.poll(() => imports.length).toBe(2);
  expect(imports[1].dryRun).toBeUndefined();
  expect(imports[1].name).toBe("Sales vocabulary");
  // A stored model is opened, so the reader lands on the classes they just imported.
  await expect.poll(() => page.url()).toContain("#/data/m/m-1");
});

test("cancelling stores nothing", async ({ page }) => {
  const imports = [];
  stubAPI(page, [app("app-1", "Sales")], imports);
  await openData(page);
  await chooseFile(page);

  await page.locator("#im-import-report [data-close]").click();
  await expect(page.locator("#im-import-report")).toHaveCount(0);
  expect(imports).toHaveLength(1);
  expect(imports[0].dryRun).toBe(true);
});

// With one application there is nothing to ask; with several the reader is asked which
// one owns the model — in the dialog, never by counting a list (pickmodal.js).
test("several applications are offered as a choice before the preview is asked for", async ({ page }) => {
  const imports = [];
  stubAPI(page, [app("app-1", "Sales"), app("app-2", "Logistics")], imports);
  await openData(page);
  await chooseFile(page);

  const picker = page.locator(".modal-ov select#pick-opt");
  await expect(picker).toBeVisible();
  expect(imports).toHaveLength(0); // nothing is asked of the server until the owner is known
  await picker.selectOption("app-2");
  await page.locator(".modal-ov [data-ok]").click();

  await expect(page.locator("#im-import-report")).toBeVisible();
  expect(imports).toHaveLength(1);
  expect(imports[0].applicationId).toBe("app-2");
});

// The import report used to open with the focus still behind it — no focus() call
// and no autofocus, so a keyboard reader landed on the page under the dialog while
// looking at the dialog. It is on the shared dialog now
// (ADR-draft-shared-ui-primitives), which puts the focus on the first thing a person
// would edit: the name they are about to store the model under.
test("the report takes the focus, on the field that is there to be edited", async ({ page }) => {
  stubAPI(page, [app("app-1", "Sales")], []);
  await openData(page);
  await chooseFile(page);

  await expect(page.locator("#im-import-report")).toBeVisible();
  await expect(page.locator("#im-import-name")).toBeFocused();
});
