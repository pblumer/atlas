// End-to-end coverage for the diagram's in/out card on a parallel fork
// (api/web/editor.js, ADR-0161 and ADR-0219).
//
// The case behind it: a fork ran "erstelle ein Ticket" and "alle Tickets holen" at the
// same time. Selecting either one listed *both* newTicket and tickets under out, because
// the card worked out what an element produced by diffing the variables it saw on entry
// against the ones that stood when it finished — and on a fork that window spans the
// sibling branch's writes. An operator read it as the fetch task having created a ticket.
//
// The second half of the file is about where the card goes and what it is allowed to
// cover once it is there, on the diagram that made both cases — see its own note below.
import { test, expect } from "@playwright/test";

const mount = async (page, query = "") => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto(`/io-card-harness.html${query}`);
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();
};

// The card is an overlay on the canvas, so it is read where it hangs rather than by
// element id: only one is ever drawn, for the selected element.
const outNames = (page) => page.locator(".io-ov .io-sec.out .io-row .io-n");
const select = (page, eik) => page.locator(`#history-list .ops-hrow[data-eik="${eik}"]`).click();

test("each branch of a fork claims only what it wrote", async ({ page }) => {
  await mount(page);

  await select(page, "1002"); // erstelle ein Ticket
  await expect(outNames(page)).toHaveText(["newTicket"]);

  await select(page, "1003"); // alle Tickets holen
  await expect(outNames(page)).toHaveText(["tickets"]);

  expect(page.__errors, "page errors").toEqual([]);
});

test("a gateway that produced nothing gets no card at all", async ({ page }) => {
  await mount(page);
  await select(page, "1002");
  await expect(page.locator(".io-ov")).toHaveCount(1); // a task that produced something
  // The join sees both variables and produced neither, so there is nothing to cover the
  // model with. Before attribution it claimed whatever had changed while it was open,
  // which is how a gateway came to carry an out list.
  await select(page, "1004");
  await expect(page.locator(".io-ov")).toHaveCount(0);
});

test("an instance recorded before attribution says why it cannot tell", async ({ page }) => {
  await mount(page, "?legacy=1");

  // Only the snapshots survive for such an instance, so the card falls back to their
  // difference — the very list that mixes the branches — and hangs the reason on the
  // section rather than quietly presenting it as this element's own work.
  await select(page, "1002");
  await expect(outNames(page)).toHaveText(["newTicket", "tickets"]);
  await expect(page.locator(".io-ov .io-sec.out")).toHaveAttribute("title", /before Atlas recorded which element wrote/);

  // The attributed instance carries no such caveat.
  await mount(page);
  await select(page, "1002");
  await expect(page.locator(".io-ov .io-sec.out")).not.toHaveAttribute("title", /./);
});

// --- Where the card hangs ---------------------------------------------------------
// The card is 240px of opaque surface and an event is 36px tall, so hung under its
// element it came down over whatever was drawn below. On the model that made the case —
// the identity lifecycle's event hub — that was the entire mutation branch: two shapes
// and both their captions, while the air above the event stood free. These drive the
// real geometry, because which spot is free is a question about a rendered diagram.
test.describe("the card's placement", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  const mountHub = async (page) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.__errors = errors;
    await page.goto("/io-card-hub-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => window.__mount());
    await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();
    await select(page, "1001"); // Service-Ereignis, the event whose card is six times its height
    await expect(page.locator(".io-ov")).toHaveCount(1);
  };

  // covers reports what the card's rectangle lands on, by element id — the shapes and
  // the captions bpmn-js drew, measured rather than predicted.
  const covers = (page, ids) => page.evaluate((wanted) => {
    const card = document.querySelector(".io-ov").getBoundingClientRect();
    const out = [];
    for (const g of document.querySelectorAll("g.djs-element[data-element-id]")) {
      const id = g.dataset.elementId;
      if (wanted && !wanted.some((w) => id === w || id.startsWith(`${w}_label`))) continue;
      const r = g.getBoundingClientRect();
      if (Math.min(card.right, r.right) - Math.max(card.left, r.left) > 0.5 &&
          Math.min(card.bottom, r.bottom) - Math.max(card.top, r.top) > 0.5) out.push(id);
    }
    return out;
  }, ids);

  test("the branch below the element stays readable", async ({ page }) => {
    await mountHub(page);
    // The two elements the card used to swallow whole, and the captions that name them.
    expect(await covers(page, ["c_mutation", "s_mutation"]), "the mutation branch").toEqual([]);
    expect(page.__errors, "page errors").toEqual([]);
  });

  test("what it does cover, it covers because there was nothing better", async ({ page }) => {
    await mountHub(page);
    // Not an empty list: on this diagram every one of the eight spots covers something,
    // and the card says so by taking the least of them rather than by not being drawn.
    // The assertion is that the price paid is a fraction of one task rather than a whole
    // branch — measured against the element's own area, which is what the chooser scores.
    const worst = await page.evaluate(() => {
      const card = document.querySelector(".io-ov").getBoundingClientRect();
      let most = 0;
      for (const g of document.querySelectorAll("g.djs-element[data-element-id]")) {
        const id = g.dataset.elementId;
        if (id.startsWith("c_service")) continue; // its own element and caption are free to cover
        const r = g.getBoundingClientRect();
        const x = Math.min(card.right, r.right) - Math.max(card.left, r.left);
        const y = Math.min(card.bottom, r.bottom) - Math.max(card.top, r.top);
        if (x > 0 && y > 0) most = Math.max(most, (x * y) / (r.width * r.height));
      }
      return most;
    });
    expect(worst, "the largest share of any one element hidden").toBeLessThan(0.5);
  });

  test("a badge it cannot avoid stays behind it", async ({ page }) => {
    await mountHub(page);
    // Badges are not part of what the placement dodges — they are 20px pills and they are
    // everywhere — so the spot it takes still has one under it. That is the case the
    // stacking rule is for, on the model it was reported from.
    await page.addStyleTag({ content: ".io-ov { pointer-events: auto; }" });
    const covered = await page.evaluate(() => {
      const card = document.querySelector(".io-ov").getBoundingClientRect();
      return [...document.querySelectorAll(".ops-badge")].map((badge) => {
        const b = badge.getBoundingClientRect();
        const x = [Math.max(card.left, b.left), Math.min(card.right, b.right)];
        const y = [Math.max(card.top, b.top), Math.min(card.bottom, b.bottom)];
        if (x[1] - x[0] <= 0.5 || y[1] - y[0] <= 0.5) return null;
        const top = document.elementFromPoint((x[0] + x[1]) / 2, (y[0] + y[1]) / 2);
        return { badge: badge.textContent.trim(), onTop: !!(top && top.closest(".io-ov")) };
      }).filter(Boolean);
    });
    expect(covered.length, "badges under the card").toBeGreaterThan(0);
    expect(covered.filter((c) => !c.onTop), "badges drawn on top of the card").toEqual([]);
  });
});
