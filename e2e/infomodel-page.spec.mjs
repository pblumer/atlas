// The class diagram gets the window (ADR-0237).
//
// The console's content sits in a centred 1120px column, which is right for a list
// and wrong for a drawing: the canvas fits the whole model into what it is given, so
// every pixel the column withholds comes off every box and every line between them.
// The diagram route drops the column; the list of models beside it keeps it, because
// a list read across a 2000px screen is a worse list.
import { test, expect } from "@playwright/test";

const MODEL = {
  id: "m1", applicationId: "a1", name: "Sales", documentation: "", revision: 1,
  classes: [{
    id: "c1", name: "Customer", stereotype: "businessObject", identity: ["nr"], x: 40, y: 40,
    attributes: [{ name: "nr", type: "string", multiplicity: "1" }],
  }],
  stores: [], associations: [], validation: { valid: true, findings: [] },
};

const SUBSET = {
  version: 1, notation: "UML 2.5 class diagram (subset)",
  stereotypes: [{ stereotype: "businessObject", label: "Business object", hasIdentity: true,
    hasAttributes: true, meaning: "Something the business tracks." }],
  associationKinds: [{ kind: "association", label: "Association", directed: false, rule: "Two things are related." }],
  primitives: [{ type: "string", label: "Text", jsonType: "string" }],
  storeModes: [], limits: [],
  multiplicities: [{ multiplicity: "1", label: "Exactly one", required: true, collection: false }],
  matrix: { "businessObject>businessObject": ["association"] },
};

test.beforeEach(async ({ page }) => {
  await page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ json: { authEnabled: false, user: null } }));
  await page.route("**/api/v1/infomodel/subset", (route) => route.fulfill({ json: SUBSET }));
  await page.route("**/api/v1/infomodel/models/m1", (route) => route.fulfill({ json: MODEL }));
  await page.setViewportSize({ width: 1400, height: 800 });
});

test("an open model reaches both edges of the window", async ({ page }) => {
  await page.goto("/index.html#/data/m/m1");
  await expect(page.locator("#im-canvas")).toBeVisible({ timeout: 20000 });

  const room = await page.evaluate(() => {
    const editor = document.querySelector(".im-editor").getBoundingClientRect();
    const canvas = document.querySelector("#im-canvas").getBoundingClientRect();
    const panel = document.querySelector("#im-side").getBoundingClientRect();
    return {
      width: window.innerWidth, editorW: editor.width, editorLeft: editor.left,
      canvasLeft: canvas.left, panelRight: Math.round(panel.right),
    };
  });
  // No column and no page gutter: the editor is the window, and the sheet starts at
  // its left edge rather than 22px in.
  expect(room.editorW).toBe(room.width);
  expect(room.editorLeft).toBe(0);
  expect(room.canvasLeft).toBe(0);
  expect(room.panelRight).toBe(room.width);
});

test("the list of models keeps the reading column", async ({ page }) => {
  await page.goto("/index.html#/data");
  await expect(page.locator("main")).toBeVisible({ timeout: 20000 });
  const column = await page.evaluate(() => ({
    cls: document.body.className,
    max: getComputedStyle(document.querySelector("main")).maxWidth,
  }));
  expect(column.cls).not.toContain("infomodel-mode");
  expect(column.max).toBe("1120px");
});
