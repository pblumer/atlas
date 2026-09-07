// The recipes chapter (api/web/handbuch.html, "Rezepte") ships its models without
// BPMN-DI to keep the XML in the card readable, so the coordinates come from
// POST /api/v1/layout. That endpoint carries RoleModeler while the handbook itself is
// public — so the reader most likely to open the page, one who is not signed in, is
// exactly the one the endpoint refuses. What the chapter says about that refusal is
// the whole subject here: a 401 or a 403 is a statement about the reader's session,
// never about the model, and the note must not claim Atlas failed to lay the model out.
//
// The static e2e harness runs no Go server, so /api/v1 is mocked: what is verified is
// which note each answer produces, and that one refusal settles the chapter instead of
// firing a request per card.
//
// Both languages are written into every note and the page's [data-l] rule hides one, so
// the assertions below read the VISIBLE half — what the reader is actually shown.
import { test, expect } from "@playwright/test";

// layoutMock answers the layout endpoint with `status` and counts the attempts, so a
// test can assert both the note and that the chapter stopped asking.
function layoutMock(page, status, calls) {
  page.route("**/api/v1/layout", async (route) => {
    calls.push(route.request().method());
    return route.fulfill({ status, json: { error: "denied" } });
  });
}

// openRecipes pins the language, scrolls the chapter into reach — diagrams render
// lazily — and waits for the first card to have settled on a note rather than the
// "loading" placeholder. Returns that first note.
async function openRecipes(page, lang = "en") {
  await page.goto("/handbuch.html#rezepte");
  await page.click("#lang-" + lang);
  await page.locator("#rezepte .recipe").first().scrollIntoViewIfNeeded();
  const note = page.locator("#rezepte .rz-diagram.failed").first();
  await expect(note).toBeVisible({ timeout: 15000 });
  return note;
}

test("a reader who is not signed in is told to sign in, not that the model cannot be laid out", async ({ page }) => {
  layoutMock(page, 401, []);
  const note = await openRecipes(page, "en");
  await expect(note.locator("[data-l]:visible")).toHaveText(/needs you signed in to the app/);
  await expect(note).not.toContainText(/cannot lay this pattern out/);
  // and the way out is one click, not a paragraph the reader has to act on themselves
  await expect(note.locator('a[href="/#/console"]:visible')).toHaveText("Sign in");
});

test("the German half says the same thing", async ({ page }) => {
  layoutMock(page, 401, []);
  const note = await openRecipes(page, "de");
  await expect(note.locator("[data-l]:visible")).toHaveText(/musst du in der App angemeldet sein/);
  await expect(note.locator('a[href="/#/console"]:visible')).toHaveText("Anmelden");
});

test("a session without the modeler role is told about the role, and is offered no sign-in link", async ({ page }) => {
  layoutMock(page, 403, []);
  const note = await openRecipes(page, "en");
  await expect(note.locator("[data-l]:visible")).toContainText(/needs your account to hold the/);
  await expect(note.locator("code:visible")).toHaveText("modeler");
  // Signing in again is not the fix when you are already signed in.
  await expect(note.locator('a[href="/#/console"]:visible')).toHaveCount(0);
});

test("a server-side failure still reads as a layout limit, which is what it is", async ({ page }) => {
  layoutMock(page, 500, []);
  const note = await openRecipes(page, "en");
  await expect(note.locator("[data-l]:visible")).toHaveText(/cannot lay this pattern out completely yet/);
});

test("one refusal settles the chapter: the remaining cards do not ask again", async ({ page }) => {
  const calls = [];
  layoutMock(page, 401, calls);
  await openRecipes(page, "en");

  // Every card carries a note …
  const failed = page.locator("#rezepte .rz-diagram.failed");
  await expect.poll(() => failed.count(), { timeout: 15000 }).toBeGreaterThan(5);
  await expect(failed.last().locator("[data-l]:visible")).toHaveText(/needs you signed in to the app/);
  // … but only the first one cost a request.
  expect(calls).toHaveLength(1);
});

test("the note follows the language toggle instead of freezing where it was drawn", async ({ page }) => {
  layoutMock(page, 401, []);
  const note = await openRecipes(page, "en");
  await expect(note.locator("[data-l]:visible")).toHaveText(/The server draws this diagram/);
  await page.click("#lang-de");
  await expect(note.locator("[data-l]:visible")).toHaveText(/Das Diagramm rechnet der Server/);
});
