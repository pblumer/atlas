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
  const head = page.locator(".pgroup-head", { hasText: "Writes data object" });
  await head.waitFor();
  if (!(await page.locator("#f-assoc-to").isVisible())) await head.click();
  await page.locator("#f-assoc-to").waitFor({ state: "visible" });
}

const targetMember = (page) => page.evaluate(() => {
  const bo = window.__atlasModeler.get("elementRegistry").get("doa_1").businessObject;
  const asg = (bo.assignment && bo.assignment[0]) || {};
  return (asg.to && asg.to.body) || "";
});

test("the members the class declares are what the path offers", async ({ page }) => {
  await selectWrite(page);
  const options = await page.locator("#f-assoc-to option").allTextContents();

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
  const group = page.locator('#f-assoc-to optgroup[label*="inside customer"]');
  await expect(group).toHaveCount(1);
  await expect(group.locator("option")).toHaveCount(1);
  await expect(group.locator("option")).toHaveText(/customer\.name/);

  // `tags` is untyped, so nothing is claimed about what is inside it. Guessing there
  // would be the false knowledge the derived model reports as a gap.
  await expect(page.locator('#f-assoc-to optgroup[label*="inside tags"]')).toHaveCount(0);
});

test("choosing a member writes it into the assignment", async ({ page }) => {
  await selectWrite(page);
  await page.locator("#f-assoc-to").selectOption("customer.name");
  await expect.poll(() => targetMember(page)).toBe("customer.name");
  expect(page.__errors).toEqual([]);
});

test("the whole object is a choice, not the absence of one", async ({ page }) => {
  await selectWrite(page);
  // The fixture starts on `total`; picking the empty option clears the path, which is
  // what "write the whole value" means in BPMN.
  await expect(page.locator("#f-assoc-to")).toHaveValue("total");
  await page.locator("#f-assoc-to").selectOption("");
  await expect.poll(() => targetMember(page)).toBe("");
});

test("a path the class does not declare is kept and named, not silently dropped", async ({ page }) => {
  await selectWrite(page);
  await page.locator("#f-assoc-to").selectOption({ label: "Another member, not modelled yet…" });
  const escape = page.locator("#f-assoc-to-other");
  await expect(escape).toBeVisible();
  await escape.fill("versandart");
  await escape.blur();

  // It is written — a model is routinely drawn before the class catches up — and it
  // comes back in the list saying what it is, rather than looking like any other member.
  await expect.poll(() => targetMember(page)).toBe("versandart");
  await selectWrite(page);
  await expect(page.locator("#f-assoc-to")).toHaveValue("versandart");
  await expect(page.locator("#f-assoc-to option[selected]")).toContainText("not a member of Order");
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
  await expect(page.locator("#f-assoc-to")).toHaveJSProperty("tagName", "INPUT");
  expect(page.__errors).toEqual([]);
});
