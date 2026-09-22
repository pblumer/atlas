// What the catalogue screen says about the portal it is not.
//
// The portal serves a release — a frozen copy — and the catalogue screen serves the
// live records. Between one publish and the next the two disagree, and the screen
// used to carry nothing about it but a table of dates.
//
// One direction of that disagreement cannot be seen anywhere else at all. A product
// taken out of a catalogue disappears from the product table immediately and goes
// on being offered from the release, so it is absent from every screen its
// maintainer has and present on the one they do not. That is the case these guard,
// and it is guarded here rather than in Go because the computation is already
// proved in api/catalog/unpublished_test.go: what is left is whether the page asks
// for the answer and puts it on screen.
import { test, expect } from "@playwright/test";

// diff is what the mock endpoint answers; null is "the read did not answer".
const open = async (page, diff) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((d) => { window.__unpublished = d; }, diff ?? null);
  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-cols tbody tr");
  expect(errors).toEqual([]);
};

const RELEASED = { released: true, releaseId: "rel_7", releasedAt: 1758000000 };

test("a product the portal still offers is named, though no other screen has it", async ({ page }) => {
  // The shape the reported corpse had: offered by a release, gone from the
  // catalogue, so the product table above cannot list it and never could.
  await open(page, {
    ...RELEASED, added: [], changed: [],
    removed: [{ id: "2027-benutzeraccount-intern", texts: { de: "Benutzerkonto intern" } }],
  });

  const card = page.locator(".portal-behind");
  await expect(card).toBeVisible();
  await expect(card.locator(".pill.warn").first()).toHaveText(/Unpublished changes/);
  await expect(card).toContainText("rel_7");

  const removed = card.locator(".portal-removed");
  await expect(removed).toContainText("Still offered on the portal (1)");
  await expect(removed).toContainText("Benutzerkonto intern");
  await expect(removed).toContainText("2027-benutzeraccount-intern");

  // The name comes from the frozen copy and the id is one the live fixture also
  // carries — so the assertion that matters is that the product table, which is
  // the screen this is meant to make up for, does not list it as removed anywhere.
  await expect(page.locator(".portal-added")).toHaveCount(0);
  await expect(page.locator(".portal-changed")).toHaveCount(0);
});

test("a catalogue the portal is serving as it stands says so rather than staying quiet", async ({ page }) => {
  await open(page, { ...RELEASED, added: [], removed: [], changed: [] });

  const card = page.locator(".portal-current");
  await expect(card).toBeVisible();
  await expect(card).toContainText("The portal is offering this catalogue exactly as it stands");
  await expect(card).toContainText("rel_7");
  await expect(page.locator(".portal-behind")).toHaveCount(0);
});

test("a read that did not answer draws neither sentence", async ({ page }) => {
  // Silence and not the green line: "nothing to publish" is a claim about the
  // portal, and a failed read is in no position to make it.
  await open(page, null);

  await expect(page.locator(".portal-current")).toHaveCount(0);
  await expect(page.locator(".portal-behind")).toHaveCount(0);
});

test("a catalogue never published draws neither sentence either", async ({ page }) => {
  // The release table underneath already says the portal shows this to nobody, and
  // every offered product would otherwise be listed as an addition — twenty-four
  // rows saying one thing.
  await open(page, { released: false, added: [{ id: "p2", texts: { de: "Produkt 2" } }], removed: [], changed: [] });

  await expect(page.locator(".portal-current")).toHaveCount(0);
  await expect(page.locator(".portal-behind")).toHaveCount(0);
});

test("an addition that will not become orderable carries its state, and an edit is listed apart", async ({ page }) => {
  await open(page, {
    ...RELEASED, removed: [],
    added: [{ id: "p2", texts: { de: "Produkt 2" }, state: "draft" },
      { id: "p3", texts: { de: "Produkt 3" }, state: "active" }],
    changed: [{ id: "p4", texts: { de: "Produkt 4" }, state: "active" }],
  });

  const added = page.locator(".portal-added");
  await expect(added).toContainText("Not on the portal yet (2)");
  // One pill and not two: publishing an active product does put it in front of
  // people, and marking that row as well would be noise on the ordinary case.
  await expect(added.locator(".pill.warn")).toHaveCount(1);
  await expect(added.locator("li", { hasText: "Produkt 2" })).toContainText("Draft");

  await expect(page.locator(".portal-changed")).toContainText("Edited since the release (1)");
  await expect(page.locator(".portal-changed")).toContainText("Produkt 4");
});
