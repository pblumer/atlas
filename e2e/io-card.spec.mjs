// End-to-end coverage for the diagram's in/out card on a parallel fork
// (api/web/editor.js, ADR-0161 and ADR-0219).
//
// The case behind it: a fork ran "erstelle ein Ticket" and "alle Tickets holen" at the
// same time. Selecting either one listed *both* newTicket and tickets under out, because
// the card worked out what an element produced by diffing the variables it saw on entry
// against the ones that stood when it finished — and on a fork that window spans the
// sibling branch's writes. An operator read it as the fetch task having created a ticket.
//
// And the case behind the last test: what the card covers stayed on top of it. diagram-js
// paints overlays in the order their elements first received one, so the execution badge
// of a shape the card happens to cover was drawn over the card — a neighbour's "11"
// standing among the card's own rows, reading as one of its values.
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

// --- What the card covers stays covered ------------------------------------------
// The canvas is the small half of this view, next to the transport bar and the history
// tree, so a default-sized window shrinks the diagram until the legend below it is what
// a click at those coordinates finds. This one gets a taller window.
test.describe("the card's stacking", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("nothing pokes through the card it covers", async ({ page }) => {
    await mount(page);
    await select(page, "1002"); // the upper branch — its card hangs over the join and the branch below

    // The card takes no pointer events, by design: a click on it must reach the element
    // underneath. Hit-testing and painting follow the same stacking order, though, so
    // lending it pointer events for the length of this test lets the browser itself answer
    // "what is on top here" instead of the test re-deriving z-index rules.
    await page.addStyleTag({ content: ".io-ov { pointer-events: auto; }" });
    await expect(page.locator(".ops-badge")).toHaveCount(3);

    const covered = await page.evaluate(() => {
      const box = (el) => el.getBoundingClientRect();
      const card = box(document.querySelector(".io-ov"));
      return [...document.querySelectorAll(".ops-badge")].map((badge) => {
        const b = box(badge);
        // The overlapping patch of the two, and its middle — the point where the question
        // "which of these two is drawn on top" is actually asked.
        const x = [Math.max(card.left, b.left), Math.min(card.right, b.right)];
        const y = [Math.max(card.top, b.top), Math.min(card.bottom, b.bottom)];
        if (x[1] - x[0] <= 0.5 || y[1] - y[0] <= 0.5) return null;
        const top = document.elementFromPoint((x[0] + x[1]) / 2, (y[0] + y[1]) / 2);
        return { badge: badge.textContent.trim(), onTop: !!(top && top.closest(".io-ov")) };
      }).filter(Boolean);
    });

    // The fixture has to keep producing the collision, or the assertion below proves nothing.
    expect(covered.length, "badges under the card").toBeGreaterThan(0);
    expect(covered.filter((c) => !c.onTop), "badges drawn on top of the card").toEqual([]);

    expect(page.__errors, "page errors").toEqual([]);
  });
});
