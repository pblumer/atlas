// End-to-end coverage for the diagram's in/out card on a parallel fork
// (api/web/editor.js, ADR-0161 and ADR-0219).
//
// The case behind it: a fork ran "erstelle ein Ticket" and "alle Tickets holen" at the
// same time. Selecting either one listed *both* newTicket and tickets under out, because
// the card worked out what an element produced by diffing the variables it saw on entry
// against the ones that stood when it finished — and on a fork that window spans the
// sibling branch's writes. An operator read it as the fetch task having created a ticket.
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
