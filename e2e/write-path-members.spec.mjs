// The members a write may target, offered rather than remembered
// (issue #905 part 1; ADR-0060 for the paths, ADR-0230 for the class that declares them).
//
// A data output association writes into one member of a structured object, and the
// path was free text: `customer.nmae` deploys, runs, and writes a member nobody will
// ever read. The class already declares what the members are.
//
// The claim these tests are most careful about is the one the field must *not* make.
// A data object is not a process variable — nothing binds one into the FEEL scope — so
// the members are a statement about the shape of the write target and about nothing
// else, and the panel has to say so where somebody is about to type an expression.
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

// The fixture's association writes `order`, whose class Order declares id, total, a
// customer typed as Customer, and an untyped tags.
async function selectWrite(page) {
  await page.evaluate(() => window.__select("doa_1"));
  const group = page.locator("[data-write-group]");
  await group.waitFor();
  if (!(await page.locator(".dw-to").first().isVisible())) await group.locator(".io-group-head").click();
  await page.locator(".dw-to").first().waitFor({ state: "visible" });
}

// The first row's picker. A write arrow carries a row per <assignment> now, so every
// assertion below says which row it means rather than naming one field.
const member = (page, i = 0) => page.locator(".dw-to").nth(i);

const targetMember = (page, i = 0) => page.evaluate((n) => {
  const bo = window.__atlasModeler.get("elementRegistry").get("doa_1").businessObject;
  const asg = (bo.assignment && bo.assignment[n]) || {};
  return (asg.to && asg.to.body) || "";
}, i);

const assignmentCount = (page) => page.evaluate(() => {
  const bo = window.__atlasModeler.get("elementRegistry").get("doa_1").businessObject;
  return (bo.assignment || []).length;
});

test("the members the class declares are what the path offers", async ({ page }) => {
  await selectWrite(page);
  const options = await member(page).locator("option").allTextContents();

  // The whole object first, then the members, then the escape.
  expect(options[0]).toContain("the whole object");
  expect(options.some((o) => o.includes("⚿ id"))).toBe(true);
  expect(options.some((o) => o.includes("total") && o.includes("number [0..1]"))).toBe(true);
  expect(options[options.length - 1]).toContain("not modelled yet");
  expect(page.__errors).toEqual([]);
});

test("a member whose type is a class offers what is inside it, one level down", async ({ page }) => {
  await selectWrite(page);
  // `customer` is typed as Customer, and Customer says what is inside one — so the
  // dotted path ADR-0060 allows can be picked rather than spelled.
  const group = member(page).locator('optgroup[label*="inside customer"]');
  await expect(group).toHaveCount(1);
  await expect(group.locator("option")).toHaveCount(1);
  await expect(group.locator("option")).toHaveText(/customer\.name/);

  // `tags` is untyped, so nothing is claimed about what is inside it. Guessing there
  // would be the false knowledge the derived model reports as a gap.
  await expect(member(page).locator('optgroup[label*="inside tags"]')).toHaveCount(0);
});

test("choosing a member writes it into the assignment", async ({ page }) => {
  await selectWrite(page);
  await member(page).selectOption("customer.name");
  await expect.poll(() => targetMember(page)).toBe("customer.name");
  expect(page.__errors).toEqual([]);
});

test("the whole object is a choice, not the absence of one", async ({ page }) => {
  await selectWrite(page);
  // The fixture starts on `total`; picking the empty option clears the path, which is
  // what "write the whole value" means in BPMN.
  await expect(member(page)).toHaveValue("total");
  await member(page).selectOption("");
  await expect.poll(() => targetMember(page)).toBe("");
});

test("a path the class does not declare is kept and named, not silently dropped", async ({ page }) => {
  await selectWrite(page);
  await member(page).selectOption({ label: "Another member, not modelled yet…" });
  const escape = page.locator("#f-dw-other-0");
  await expect(escape).toBeVisible();
  await escape.fill("versandart");
  await escape.blur();

  // It is written — a model is routinely drawn before the class catches up — and it
  // comes back in the list saying what it is, rather than looking like any other member.
  await expect.poll(() => targetMember(page)).toBe("versandart");
  await selectWrite(page);
  await expect(member(page)).toHaveValue("versandart");
  await expect(member(page).locator("option[selected]")).toContainText("not a member of Order");
  expect(page.__errors).toEqual([]);
});

test("the panel says the members are a shape, not what an expression can read", async ({ page }) => {
  await selectWrite(page);
  const panel = page.locator("#p-body");
  // The distinction the whole feature turns on. A data object is not a process
  // variable, so a reader about to type into FEEL value must not take the member list
  // beside it as the scope of that expression.
  await expect(panel).toContainText("shaped");
  await expect(panel).toContainText("variables");
  await expect(panel).toContainText("a data object is not one of them");
});

test("an object whose class nothing models leaves the path as free text", async ({ page }) => {
  // `note` declares no type, so there is nothing to offer and the field is what it
  // always was. A picker with no members would say the class declares none.
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    const el = m.get("elementRegistry").get("doa_1");
    m.get("modeling").updateProperties(el, { targetRef: m.get("elementRegistry").get("Ref_note").businessObject });
  });
  await selectWrite(page);
  await expect(member(page)).toHaveJSProperty("tagName", "INPUT");
  expect(page.__errors).toEqual([]);
});

// Several writes on one arrow (ADR-draft-a-write-arrow-may-set-several-members).
//
// This is the half the feature exists for. Before it, a step that captures a form's
// worth of fields had two options and both were bad: one arrow per field, which turns
// the diagram into a cable harness, or one arrow carrying a FEEL context literal,
// which draws well and tells the model nothing — the members inside an expression
// cannot be read at deploy time, so the write goes unchecked and the class derives as
// having no members at all.
const addWrite = async (page) => {
  await page.locator("[data-write-group] .io-group-add").click();
};

test("a second write can be added to the same arrow", async ({ page }) => {
  await selectWrite(page);
  expect(await assignmentCount(page)).toBe(1);

  await addWrite(page);
  await member(page, 1).selectOption("customer.name");
  await page.locator(".dw-from").nth(1).fill("=customerName");
  await page.locator(".dw-from").nth(1).blur();

  await expect.poll(() => assignmentCount(page)).toBe(2);
  expect(await targetMember(page, 0)).toBe("total");
  expect(await targetMember(page, 1)).toBe("customer.name");
  expect(page.__errors).toEqual([]);
});

test("the arrow count does not grow with the number of members written", async ({ page }) => {
  // The whole point of the shape: the drawing stays a drawing. Two writes, one arrow.
  await selectWrite(page);
  await addWrite(page);
  await member(page, 1).selectOption("customer.name");
  await expect.poll(() => assignmentCount(page)).toBe(2);

  const arrows = await page.evaluate(() => {
    const bo = window.__atlasModeler.get("elementRegistry").get("Activity_1").businessObject;
    return (bo.get("dataOutputAssociations") || []).length;
  });
  expect(arrows).toBe(1);
});

test("the group says how many writes the arrow carries", async ({ page }) => {
  // A write arrow used to say what it did on the canvas, one arrow per member. Now it
  // says it here, and this badge is the only place it says it without being opened.
  await selectWrite(page);
  const badge = page.locator("[data-write-group] .io-group-count");
  await expect(badge).toHaveText("1");
  await addWrite(page);
  await expect(badge).toHaveText("2");
});

test("deleting a write removes that assignment and keeps the others", async ({ page }) => {
  await selectWrite(page);
  await addWrite(page);
  await member(page, 1).selectOption("customer.name");
  await expect.poll(() => assignmentCount(page)).toBe(2);

  await page.locator("[data-write-group] .io-map-del").first().click();
  await expect.poll(() => assignmentCount(page)).toBe(1);
  expect(await targetMember(page, 0)).toBe("customer.name");
  expect(page.__errors).toEqual([]);
});

test("each row's escape field belongs to its own row", async ({ page }) => {
  // Two rows sharing an id is an escape that opens the wrong row — and writes the name
  // the author typed into a member they were not editing.
  await selectWrite(page);
  await addWrite(page);
  await member(page, 1).selectOption({ label: "Another member, not modelled yet…" });

  await expect(page.locator("#f-dw-other-1")).toBeVisible();
  await expect(page.locator("#f-dw-other-0")).toBeHidden();
  await page.locator("#f-dw-other-1").fill("versandart");
  await page.locator("#f-dw-other-1").blur();

  await expect.poll(() => targetMember(page, 1)).toBe("versandart");
  expect(await targetMember(page, 0)).toBe("total");
  expect(page.__errors).toEqual([]);
});

test("the panel says several writes are one change, applied in order", async ({ page }) => {
  // Order is load-bearing — two writes to the same member mean the later one — and the
  // single event is why this was not built by fanning the writes out into one arrow
  // each behind the author's back.
  await selectWrite(page);
  const panel = page.locator("#p-body");
  await expect(panel).toContainText("in order");
  await expect(panel).toContainText("one");
});

test("a row added after a deletion does not inherit another row's escape field", async ({ page }) => {
  // The panel does not re-render on a delete (that would tear down a half-typed field),
  // so the rows left behind keep the indices they had. Numbering a new row by the list's
  // length would hand it an id another row is still using, and the escape would open —
  // and write into — the wrong member.
  await selectWrite(page);
  await addWrite(page);
  await addWrite(page);
  await expect(page.locator("[data-write-group] [data-write]")).toHaveCount(3);

  await page.locator("[data-write-group] .io-map-del").nth(1).click();
  await addWrite(page);

  const ids = await page.evaluate(() => [...document.querySelectorAll("[data-write-group] [data-write]")]
    .map((c) => c.dataset.i));
  expect(new Set(ids).size).toBe(ids.length);

  const last = ids.length - 1;
  await member(page, last).selectOption({ label: "Another member, not modelled yet…" });
  await expect(page.locator(`#f-dw-other-${ids[last]}`)).toBeVisible();
  await expect(page.locator("#f-dw-other-0")).toBeHidden();
  expect(page.__errors).toEqual([]);
});
