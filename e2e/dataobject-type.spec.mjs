// End-to-end coverage for a data object's declared type in the Modeler
// (ADR-0230, slice 3).
//
// The case behind it: BPMN's itemSubjectRef is the slot where a data object says
// what kind of thing it is, and until there was an information model there was
// nothing to put in it — so the Modeler never offered the field at all. Now the
// field exists, is suggested from the application's modelled classes, and has to
// survive the round trip: itemSubjectRef is a *reference* attribute in the bpmn
// moddle, not a plain string, so the only proof that a typed name lands in the XML
// the deploy sends is to set it and read that XML back.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/dataobject-type-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.waitForFunction(() => !!window.__atlasModeler, null, { timeout: 20000 });
});

// Property groups start collapsed on open except General, so selecting a data
// object and reaching its fields means opening "Data object" first.
async function selectDataObject(page, id) {
  await page.evaluate((ref) => window.__select(ref), id);
  const head = page.locator(".pgroup-head", { hasText: "Data object" });
  await head.waitFor();
  if (!(await page.locator("#f-itemtype").isVisible())) await head.click();
  await page.locator("#f-itemtype").waitFor({ state: "visible" });
}

test("the type a data object declares is shown and suggested from the model", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  await expect(page.locator("#f-itemtype")).toHaveValue("Order");
  // The suggestions are the application's modelled classes — fetched, not invented.
  await expect(page.locator("#f-itemtype-list option")).toHaveCount(2);
  const options = await page.locator("#f-itemtype-list option").evaluateAll((els) => els.map((e) => e.value));
  expect(options).toEqual(["Customer", "Order"]);
});

test("a type nothing models is marked, and is still allowed to stand", async ({ page }) => {
  await selectDataObject(page, "Ref_claim");
  await expect(page.locator("#f-itemtype")).toHaveValue("Claim");
  // Marked, because it is the gap the information model exists to close — and not
  // refused, because a diagram is routinely drawn before the vocabulary it names.
  await expect(page.locator("#p-body")).toContainText("No class called");
  await expect(page.locator("#p-body")).toContainText("a deploy is not refused for it");

  // A modelled one carries no such note.
  await selectDataObject(page, "Ref_order");
  await expect(page.locator("#p-body")).not.toContainText("No class called");
});

test("setting the type lands in the exported XML, on the data object itself", async ({ page }) => {
  await selectDataObject(page, "Ref_note");
  await expect(page.locator("#f-itemtype")).toHaveValue("");
  await page.locator("#f-itemtype").fill("Customer");
  await page.locator("#f-itemtype").dispatchEvent("change");

  const xml = await page.evaluate(() => window.__xml());
  // itemSubjectRef is a reference, so the attribute carries the <itemDefinition>'s
  // id and the *name* travels in its structureRef — which is what lets a class
  // called "Line item" be expressed at all, and what the compiler resolves.
  expect(xml).toMatch(/<bpmn:itemDefinition[^>]*id="ItemDefinition_Customer"[^>]*structureRef="Customer"/);
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_note"[^>]*itemSubjectRef="ItemDefinition_Customer"/);
  // On the <dataObject>, not on the reference: the reference is a view of the
  // object, and the compiler reads the type off the object.
  expect(xml).not.toMatch(/<bpmn:dataObjectReference[^>]*id="Ref_note"[^>]*itemSubjectRef=/);
  // And the panel re-read it, so the note about an unmodelled class is gone.
  await expect(page.locator("#p-body")).not.toContainText("No class called");
  expect(page.__errors).toEqual([]);
});

test("a type a tool wrote in its own namespace reads as the name, not the GUID", async ({ page }) => {
  // BPMN gives an <itemDefinition> no name of its own, so structureRef is the only
  // slot the specification offers — and MID Innovator does not use it: it writes a
  // bare GUID id with <bpanda:property name="Name" value="Incident"/> beside it.
  // Reading only the id showed that GUID as the declared type of every data object in
  // an imported model. The compiler resolves it the same way, so the panel and the
  // Problems list agree.
  await selectDataObject(page, "Ref_incident");
  await expect(page.locator("#f-itemtype")).toHaveValue("Incident");
  expect(page.__errors).toEqual([]);
});

test("a data object that lost its declaration is repaired on the way in", async ({ page }) => {
  // A data object is two elements: the <dataObject> that declares it and carries its
  // type, and the <dataObjectReference> that puts it on the canvas with its name, its
  // data state and its shape. Only the second is drawn, so a model can arrive having
  // lost the first — a box that reads "Kunde [received]" to anybody looking at it and
  // names nothing the engine can find. It was refused at deploy over an id nobody had
  // typed, and a type set in the panel had nowhere to be written and vanished on save.
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DataObject_0s4i37q"[^>]*name="Kunde"/);
  expect(xml).toMatch(/<bpmn:dataObjectReference[^>]*id="Ref_kunde"[^>]*dataObjectRef="DataObject_0s4i37q"/);

  // And with the declaration back, the type has somewhere to live: setting it lands
  // on the data object and survives the round trip, like any other.
  await selectDataObject(page, "Ref_kunde");
  await expect(page.locator("#f-itemtype")).toHaveValue("");
  await page.locator("#f-itemtype").fill("Customer");
  await page.locator("#f-itemtype").dispatchEvent("change");
  const after = await page.evaluate(() => window.__xml());
  expect(after).toMatch(/<bpmn:dataObject[^>]*id="DataObject_0s4i37q"[^>]*itemSubjectRef="ItemDefinition_Customer"/);
  expect(page.__errors).toEqual([]);
});

test("clearing the type removes the attribute rather than emptying it", async ({ page }) => {
  await selectDataObject(page, "Ref_claim");
  await page.locator("#f-itemtype").fill("");
  await page.locator("#f-itemtype").dispatchEvent("change");

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_claim"/);
  expect(xml).not.toMatch(/id="DO_claim"[^>]*itemSubjectRef/);
});

test("the Problems panel asks about the application the draft is filed under", async ({ page }) => {
  // Without the application id the server has no vocabulary to resolve a declared
  // type against, so the panel would silently lose half of what it can now say.
  await page.waitForFunction(() => (window.__validated || []).length > 0, null, { timeout: 20000 });
  const urls = await page.evaluate(() => window.__validated);
  expect(urls.every((u) => u.includes("applicationId=app-1"))).toBe(true);
});

test("a hand-written shorthand type survives being opened and saved", async ({ page }) => {
  // The bug this guards: BPMN's itemSubjectRef is a reference, and the moddle drops
  // one it cannot resolve — so a model carrying the shorthand itemSubjectRef="Order"
  // with no <itemDefinition> came back untyped after a round trip through the
  // Modeler, silently. The declarations are repaired on import instead.
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_order"[^>]*itemSubjectRef="Order"/);
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_claim"[^>]*itemSubjectRef="Claim"/);
  // The repair declares what the shorthand implied, so the reference resolves.
  expect(xml).toMatch(/<bpmn:itemDefinition[^>]*id="Order"[^>]*structureRef="Order"/);
  // An object that declared no type still declares none.
  expect(xml).not.toMatch(/id="DO_note"[^>]*itemSubjectRef/);
});
