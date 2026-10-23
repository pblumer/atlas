// e2e for the directory field (api/web/remote-select.js, vendor/form-js/src/atlas/
// viewer.jsx, ADR-0473): a form-js select whose options come from a remote data source.
// Driven on the real vendored form-js viewer with a transport the test controls, so each
// property below is shown rather than assumed: nothing is asked before somebody types, a
// search waits for the department it is bound to, a slow answer cannot overwrite a newer
// one, a withdrawn request is aborted, the keyboard works, the chosen id — not its label —
// is what the form submits, and choosing is not completing.
//
// Every property is shown twice: for the select the editor writes, and for a directory
// field saved as a textfield, which forms written before the select still hold.
import { test, expect } from "@playwright/test";

const ANNA = "11111111-0000-0000-0000-000000000001";
const BEAT = "11111111-0000-0000-0000-000000000002";
const CLAUDIA = "11111111-0000-0000-0000-000000000003";
const people = [
  { value: ANNA, label: "Anna Müller", description: "anna.mueller@example.com", details: { department: "IT" } },
  { value: BEAT, label: "Annette Beat", description: "annette@example.com", details: { department: "IT" } },
  { value: CLAUDIA, label: "Anneliese Weber", description: "anneliese@example.com", details: { department: "IT" } },
];

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.goto("/remote-select-harness.html?lang=de");
  await page.waitForFunction(() => window.__ready === true);
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

for (const kind of ["select", "textfield"]) {
  test.describe(`as a ${kind}`, () => {
    async function mount(page, opts = {}) {
      await page.evaluate((o) => window.__mount(o), { kind, ...opts });
      await page.waitForFunction(() => window.__mounted === true);
    }

    // The directory field's own element: the select's, or the textfield's it is attached to.
    const fieldEl = (page) => page.locator(kind === "select" ? ".fjs-form-field-select.atlas-remote-select" : ".fjs-form-field-textfield.atlas-rs-host");

    const search = (page) => page.locator(".atlas-rs-search");
    const status = (page) => page.locator(".atlas-rs-status");
    const options = (page) => page.locator(".atlas-rs-list li[role=option]");
    const reqs = (page) => page.evaluate(() => window.__reqs());

    async function chooseDepartment(page, label) {
      await page.locator(".fjs-form-field-select:not(.atlas-remote-select) .fjs-input-group").click();
      await page.locator(".fjs-dropdownlist-item", { hasText: label }).first().click();
    }

    async function respond(page, i, items, next) {
      await page.evaluate(([i, items, next]) => window.__respond(i, items, next), [i, items, next]);
    }

    test("the field renders as a search, and asks nothing on its own", async ({ page }) => {
      await mount(page);
      await expect(fieldEl(page).locator(".atlas-rs")).toHaveCount(1);
      await expect(page.locator(".atlas-rs")).toHaveCount(1);
      if (kind === "select") {
        // The select's own element holds the search; nothing is attached from outside.
        expect(await page.evaluate(() => window.__attached)).toBe(0);
        await expect(search(page)).toHaveAttribute("id", /-Field_user$/);
      } else {
        // The textfield that carries the value is hidden; its label points at the search.
        const carrier = page.locator("input.fjs-input[id$='-Field_user']");
        await expect(carrier).toBeHidden();
        expect(await page.evaluate(() => window.__attached)).toBe(1);
      }
      await expect(search(page)).toBeVisible();
      await expect(search(page)).toHaveAttribute("role", "combobox");
      await expect(search(page)).toHaveAttribute("aria-expanded", "false");
      // The field's label names the search box.
      await expect(page.getByRole("combobox", { name: "Person", exact: true })).toHaveClass(/atlas-rs-search/);
      // Nothing is preloaded: not one request until somebody types.
      expect(await reqs(page)).toEqual([]);
    });

    test("a search waits for the department it is bound to", async ({ page }) => {
      await mount(page);
      await search(page).fill("Ann");
      await expect(status(page)).toHaveText("Bitte zuerst Abteilung wählen.");
      expect(await reqs(page)).toEqual([]);
      await chooseDepartment(page, "IT");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      expect((await reqs(page))[0]).toEqual({ field: "user", body: { search: "Ann", filters: { department: "IT" } }, aborted: false });
    });

    test("loading, results, an empty answer and a failure each say so", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await expect(status(page)).toHaveText("Suche läuft…");
      await expect(search(page)).toHaveAttribute("aria-busy", "true");
      await respond(page, 0, people);
      await expect(options(page)).toHaveCount(3);
      await expect(status(page)).toHaveText("3 Treffer");
      await expect(options(page).first()).toContainText("Anna Müller");
      await expect(options(page).first()).toContainText("anna.mueller@example.com");
      await expect(options(page).first()).toContainText("IT");
      await expect(search(page)).toHaveAttribute("aria-expanded", "true");

      await search(page).fill("Zzz");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(2);
      await respond(page, 1, []);
      await expect(status(page)).toHaveText("Keine Treffer.");
      await expect(options(page)).toHaveCount(0);

      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(3);
      await page.evaluate(() => window.__fail(2, "provider_throttled", 7));
      await expect(status(page)).toHaveText("Das Verzeichnis bremst die Anfragen gerade. Bitte in 7 Sekunden erneut versuchen.");
      await expect(status(page)).toHaveAttribute("role", "alert");

      await search(page).fill("Anna");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(4);
      await page.evaluate(() => window.__fail(3, "worker_unavailable"));
      await expect(status(page)).toContainText("kein Worker aktiv");
    });

    test("a term shorter than the field's minimum is not searched", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("A");
      await expect(status(page)).toHaveText("Bitte mindestens 2 Zeichen eingeben.");
      expect(await reqs(page)).toEqual([]);
    });

    test("typing is debounced into one request", async ({ page }) => {
      await mount(page, { debounceMs: 300, manualTimers: true });
      await chooseDepartment(page, "IT");
      await search(page).pressSequentially("Anna", { delay: 0 });
      // Four keystrokes, one waiting timer: each keystroke withdrew the one before.
      expect(await page.evaluate(() => window.__pendingTimers())).toBe(1);
      expect(await reqs(page)).toEqual([]);
      expect(await page.evaluate(() => window.__elapse())).toEqual([300]);
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      expect((await reqs(page))[0].body.search).toBe("Anna");
    });

    test("a newer term withdraws the older request and a late answer is ignored", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await search(page).fill("Anne");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(2);
      const [first, second] = await reqs(page);
      expect(first.aborted).toBe(true);
      expect(second.aborted).toBe(false);
      await respond(page, 1, [people[1], people[2]]);
      await expect(options(page)).toHaveCount(2);
      // The withdrawn request answers late; nothing it says reaches the list.
      await respond(page, 0, people);
      // One turn of the page's event loop: every continuation the late answer could have
      // started has run by then.
      await page.evaluate(() => new Promise((r) => setTimeout(r, 0)));
      await expect(options(page)).toHaveCount(2);
      await expect(options(page).first()).toContainText("Annette Beat");
    });

    test("the keyboard moves through the results and Enter chooses without submitting", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await respond(page, 0, people);
      await expect(options(page)).toHaveCount(3);
      await search(page).press("ArrowDown");
      await expect(options(page).nth(1)).toHaveAttribute("aria-selected", "true");
      const active = await search(page).getAttribute("aria-activedescendant");
      expect(active).toBe(await options(page).nth(1).getAttribute("id"));
      await search(page).press("End");
      await expect(options(page).nth(2)).toHaveAttribute("aria-selected", "true");
      await search(page).press("Home");
      await search(page).press("Enter");
      await expect(page.locator(".atlas-rs-selected")).toBeVisible();
      await expect(page.locator(".atlas-rs-selected-label")).toHaveText("Anna Müller");
      await expect(search(page)).toHaveAttribute("aria-expanded", "false");
      expect(await page.evaluate(() => window.__carrier())).toBe(ANNA);
      // Choosing is not completing: the form was not submitted.
      expect(await page.evaluate(() => window.__submitEvents)).toBe(0);
      // Escape closes the list and nothing else.
      await search(page).press("ArrowDown");
      await expect(search(page)).toHaveAttribute("aria-expanded", "true");
      await search(page).press("Escape");
      await expect(search(page)).toHaveAttribute("aria-expanded", "false");
      expect(await page.evaluate(() => window.__carrier())).toBe(ANNA);
    });

    test("the form submits the chosen id, not its label, and a missing choice is refused", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      const empty = await page.evaluate(() => window.__submit());
      expect(empty.errors).toContain("Field_user");
      await expect(fieldEl(page).locator(".fjs-form-field-error")).toContainText(/required/i);

      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await respond(page, 0, people);
      await options(page).nth(2).click();
      const sent = await page.evaluate(() => window.__submit());
      expect(sent.errors).toEqual([]);
      expect(sent.data.user).toBe(CLAUDIA);
      expect(sent.data.abteilung).toBe("IT");
      expect(JSON.stringify(sent.data)).not.toContain("Anneliese");
    });

    test("changing the department clears a choice it no longer admits, and the stale results", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await respond(page, 0, people);
      await options(page).first().click();
      expect(await page.evaluate(() => window.__carrier())).toBe(ANNA);

      await chooseDepartment(page, "Finanzen");
      await expect(page.locator(".atlas-rs-selected")).toBeHidden();
      expect(await page.evaluate(() => window.__carrier())).toBe("");
      await expect(status(page)).toContainText("Suche läuft…");
      // The term still stands, so the search runs again — in the new department.
      const all = await reqs(page);
      expect(all[all.length - 1].body.filters).toEqual({ department: "Finance" });
      await expect(options(page)).toHaveCount(0);
    });

    test("load more appends the next page through the cursor the server handed out", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await respond(page, 0, people.slice(0, 2), "cursor-1");
      await expect(status(page)).toHaveText("2 Treffer (weitere vorhanden)");
      await page.locator(".atlas-rs-more").click();
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(2);
      expect((await reqs(page))[1].body).toEqual({ search: "Ann", filters: { department: "IT" }, cursor: "cursor-1" });
      await respond(page, 1, people.slice(2));
      await expect(options(page)).toHaveCount(3);
      await expect(page.locator(".atlas-rs-more")).toBeHidden();
    });

    test("a task reopened with a choice shows who it is", async ({ page }) => {
      await mount(page, { data: { abteilung: "IT", user: ANNA } });
      await expect(page.locator(".atlas-rs-selected")).toBeVisible();
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      expect((await reqs(page))[0].body).toEqual({ value: ANNA, filters: { department: "IT" } });
      await respond(page, 0, [people[0]]);
      await expect(page.locator(".atlas-rs-selected-label")).toHaveText("Anna Müller");
      const sent = await page.evaluate(() => window.__submit());
      expect(sent.data.user).toBe(ANNA);
    });

    test("a reopened choice the directory no longer holds says so", async ({ page }) => {
      await mount(page, { data: { abteilung: "IT", user: ANNA } });
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await respond(page, 0, []);
      await expect(page.locator(".atlas-rs-selected")).toHaveClass(/atlas-rs-unknown/);
      await expect(page.locator(".atlas-rs-selected-desc")).toContainText("nicht mehr");
    });

    test("the clear button removes the choice", async ({ page }) => {
      await mount(page);
      await chooseDepartment(page, "IT");
      await search(page).fill("Ann");
      await expect.poll(() => reqs(page).then((r) => r.length)).toBe(1);
      await respond(page, 0, people);
      await options(page).first().click();
      await page.locator(".atlas-rs-clear").click();
      await expect(page.locator(".atlas-rs-selected")).toBeHidden();
      expect(await page.evaluate(() => window.__carrier())).toBe("");
    });

    test("the preview searches made-up people and says so", async ({ page }) => {
      await mount(page, { preview: true });
      await expect(page.locator(".atlas-rs-preview")).toContainText("Beispieldaten");
      await chooseDepartment(page, "IT");
      await search(page).fill("an");
      await expect(options(page).first()).toBeVisible();
      const labels = await options(page).locator(".atlas-rs-label").allTextContents();
      expect(labels).toContain("Anna Müller");
      for (const d of await options(page).locator(".atlas-rs-details").allTextContents()) {
        expect(d).toContain("IT");
      }
      // The preview never reaches the transport a real task uses.
      expect(await reqs(page)).toEqual([]);
    });

  });
}
