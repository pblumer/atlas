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

test("the type a data object declares is shown, and the choices are the modelled classes", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  await expect(page.locator("#f-itemtype")).toHaveValue("Order");
  // The choices are the application's modelled classes — fetched, not invented.
  const options = await page.locator("#f-itemtype option").evaluateAll((els) => els.map((e) => e.value));
  expect(options).toEqual(["", "Customer", "Order", ""]); // — none —, the two classes, the escape
});

test("a type nothing models is marked, and is still allowed to stand", async ({ page }) => {
  await selectDataObject(page, "Ref_claim");
  // It keeps an option of its own rather than being dropped on sight: reading a
  // document must never quietly rewrite it, and the reason it is off the list rides
  // along, because that is the whole point of showing it.
  await expect(page.locator("#f-itemtype")).toHaveValue("Claim");
  await expect(page.locator("#f-itemtype option[selected]")).toHaveText("Claim — not modelled yet");
  // Marked, because it is the gap the information model exists to close — and not
  // refused, because a diagram is routinely drawn before the vocabulary it names.
  await expect(page.locator("#p-body")).toContainText("No class called");
  await expect(page.locator("#p-body")).toContainText("a deploy is not refused for it");

  // A modelled one carries no such note.
  await selectDataObject(page, "Ref_order");
  await expect(page.locator("#p-body")).not.toContainText("No class called");
});

// What the field is for is the link between a data object and the class it is, and
// that link used to be made by remembering a name and typing it: the vocabulary was in
// an invisible <datalist>, and nothing said what the name you typed actually meant.
test("the classes the application models are offered, with what tells them apart", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  const pick = page.locator("#f-itemtype");
  await expect(pick).toBeVisible();
  // The business key rides along, because it is the fact that tells two similarly
  // named classes apart and the thing somebody is trying to recall.
  const labels = await pick.locator("option").evaluateAll((els) => els.map((e) => e.textContent.trim()));
  expect(labels).toEqual(["— none —", "Customer", "Order · key id", "Another class, not modelled yet…"]);
  // Grouped by the model they live in, so a name says where it comes from.
  await expect(pick.locator("optgroup")).toHaveAttribute("label", "Sales data");

  await selectDataObject(page, "Ref_note");
  await expect(page.locator("#f-itemtype")).toHaveValue("");
  await page.locator("#f-itemtype").selectOption("Customer");
  await expect(page.locator("#f-itemtype")).toHaveValue("Customer");
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_note"[^>]*itemSubjectRef="ItemDefinition_Customer"/);
  expect(page.__errors).toEqual([]);
});

test("the class the type points at is shown, so it can be checked without leaving", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  const card = page.locator(".im-card");
  await expect(card.locator(".im-card-name")).toHaveText("Order");
  await expect(card.locator(".im-card-stereo")).toHaveText("«businessObject»");
  await expect(card.locator(".im-card-attrs li")).toHaveCount(2);
  // The business key is marked here exactly as the canvas marks it: it is what makes
  // two of these the same one, and the reason to check the class at all.
  await expect(card.locator("li.key .n")).toHaveText("⚿ id");
  await expect(card.locator("li").nth(1)).toContainText("number [0..1]");
  // And a way back to where it is authored, in a new tab — the diagram has unsaved
  // work in it, so this must not navigate away from it.
  const open = card.locator(".im-card-open");
  await expect(open).toHaveAttribute("href", "#/data/m/m1");
  await expect(open).toHaveAttribute("target", "_blank");

  // A class with no business key shows its members and no key mark.
  await selectDataObject(page, "Ref_note");
  await page.locator("#f-itemtype").selectOption("Customer");
  await expect(page.locator(".im-card .im-card-name")).toHaveText("Customer");
  await expect(page.locator(".im-card li.key")).toHaveCount(0);
});

test("a type nothing models yet can be modelled from here", async ({ page }) => {
  await selectDataObject(page, "Ref_claim");
  await expect(page.locator("#p-body")).toContainText("No class called");
  await expect(page.locator(".im-card")).toHaveCount(0);

  // The remedy used to be: leave the diagram, find the model, add the class, come back.
  await page.locator("#f-itemtype-create").click();
  await expect(page.locator(".im-card .im-card-name")).toHaveText("Claim");
  await expect(page.locator("#p-body")).not.toContainText("No class called");

  // It was written to the model, as a business object with nothing invented for it:
  // the business key is the author's to choose, and guessing one would be worse than
  // leaving it open.
  const puts = await page.evaluate(() => window.__put);
  expect(puts).toHaveLength(1);
  expect(puts[0].revision).toBe(3); // written against the revision it was read at
  const added = puts[0].classes.find((c) => c.name === "Claim");
  expect(added.stereotype).toBe("businessObject");
  expect(added.attributes).toEqual([]);
  expect(added.identity).toEqual([]);
  // Placed where the canvas would place it, not on top of the class before it.
  expect(added.x).toBe(560);
  expect(page.__errors).toEqual([]);
});

test("setting the type lands in the exported XML, on the data object itself", async ({ page }) => {
  await selectDataObject(page, "Ref_note");
  await expect(page.locator("#f-itemtype")).toHaveValue("");
  await page.locator("#f-itemtype").selectOption("Customer");

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
  await page.locator("#f-itemtype").selectOption("Customer");
  const after = await page.evaluate(() => window.__xml());
  expect(after).toMatch(/<bpmn:dataObject[^>]*id="DataObject_0s4i37q"[^>]*itemSubjectRef="ItemDefinition_Customer"/);
  expect(page.__errors).toEqual([]);
});

test("clearing the type removes the attribute rather than emptying it", async ({ page }) => {
  await selectDataObject(page, "Ref_claim");
  await page.locator("#f-itemtype").selectOption("");

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

// A class the vocabulary has never heard of still has to be nameable: a diagram is
// routinely drawn before the model it names exists (ADR-0230), and a deploy is not
// refused for it. The list is the field now, so the escape is the last entry of the
// list rather than the field staying free text.
test("a class nothing models yet can still be named, from the list itself", async ({ page }) => {
  await selectDataObject(page, "Ref_note");
  await expect(page.locator("#f-itemtype-other")).toBeHidden();
  await page.locator("#f-itemtype").selectOption({ label: "Another class, not modelled yet…" });

  const other = page.locator("#f-itemtype-other");
  await expect(other).toBeVisible();
  await expect(other).toBeFocused(); // one gesture, or it is not an escape
  await other.fill("Shipment");
  await other.dispatchEvent("change");

  // It lands where a picked class lands, and is marked the way a typed one always was.
  await expect(page.locator("#f-itemtype")).toHaveValue("Shipment");
  await expect(page.locator("#p-body")).toContainText("No class called");
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_note"[^>]*itemSubjectRef="ItemDefinition_Shipment"/);
  expect(page.__errors).toEqual([]);
});

test("reaching for the escape and changing your mind leaves the type alone", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  await page.locator("#f-itemtype").selectOption({ label: "Another class, not modelled yet…" });
  await page.locator("#f-itemtype-other").dispatchEvent("change"); // blurred empty
  await expect(page.locator("#f-itemtype")).toHaveValue("Order");
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObject[^>]*id="DO_order"[^>]*itemSubjectRef="Order"/);
});

// The data state was free text for as long as there was nothing to check it against.
// A class declares the states its instances move through now (ADR-0259), so the field
// offers them — which is the difference between `order [approved]` and the `[aproved]`
// that is typed once and then never matches anything again.
test("the states a data object may be in are the ones its class declares", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  const st = page.locator("#f-datastate");
  await expect(st).toHaveValue("approved");
  const labels = await st.locator("option").evaluateAll((els) => els.map((e) => e.textContent.trim()));
  // Where instances start and where they end are marked, because a list of names alone
  // does not say which of them a new object is.
  expect(labels).toEqual([
    "— none —", "received · start", "approved", "shipped · final", "Another state, not declared yet…",
  ]);
  // And the panel says where the list comes from, with the way to add to it.
  await expect(page.locator("#p-body")).toContainText("declares in its lifecycle");
});

test("choosing a state lands on the reference, which is where BPMN keeps it", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  await page.locator("#f-datastate").selectOption("shipped");
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObjectReference[^>]*id="Ref_order"[\s\S]*?<bpmn:dataState[^>]*name="shipped"/);
  // On the reference and not on the object: two references to one object are the same
  // datum at two points of its life, which is the whole reason the state lives there.
  expect(xml).not.toMatch(/<bpmn:dataObject[^>]*id="DO_order"[^>]*dataState/);
  expect(page.__errors).toEqual([]);
});

test("a class that declares no lifecycle leaves the state free text", async ({ page }) => {
  // Silence everywhere is the normal case: a class without a lifecycle behaves exactly
  // as it did before there was such a thing.
  await selectDataObject(page, "Ref_claim");
  await expect(page.locator("#f-datastate")).toHaveJSProperty("tagName", "INPUT");
  await page.locator("#f-datastate").fill("filed");
  await page.locator("#f-datastate").dispatchEvent("change");
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toMatch(/<bpmn:dataObjectReference[^>]*id="Ref_claim"[\s\S]*?<bpmn:dataState[^>]*name="filed"/);
});

test("a state the class does not declare can be named, and the list says so", async ({ page }) => {
  await selectDataObject(page, "Ref_order");
  await page.locator("#f-datastate").selectOption({ label: "Another state, not declared yet…" });
  const other = page.locator("#f-datastate-other");
  await expect(other).toBeVisible();
  await other.fill("archived");
  await other.dispatchEvent("change");

  await expect(page.locator("#f-datastate")).toHaveValue("archived");
  const labels = await page.locator("#f-datastate option").evaluateAll((els) => els.map((e) => e.textContent.trim()));
  expect(labels).toContain("archived — not a state of Order");
  expect(page.__errors).toEqual([]);
});
