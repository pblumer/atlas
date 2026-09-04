// e2e for the documentation Markdown renderer (api/web/markdown.js,
// ADR-0250).
//
// Two things are being established here, and they are not the same kind of claim.
//
// The first is that the prose people already wrote still reads the way they wrote it.
// Every documentation text in every existing model was authored as plain text and shown
// under `white-space: pre-wrap`; if this renderer reflows it, joins its lines, or turns
// a variable name into italics, the change is a silent regression in text nobody will
// re-read until it is wrong in front of an auditor.
//
// The second is that the output is safe to hand to innerHTML. That is a claim about a
// browser — whether a payload became an element, whether a handler survived, where an
// href actually points — so it is tested by inserting the output into a live DOM and
// asking the parser, not by matching strings. It matters because the author of a
// documentation text and its reader are different people: a modeller writes it, a
// caseworker reads it in the Tasks app, and an auditor may read it through a public
// link with no account at all (ADR-0143).
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/markdown-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const render = (page, src) => page.evaluate((s) => window.__render(s), src);
const plain = (page, src) => page.evaluate((s) => window.__md.markdownToPlain(s), src);

// ---------- the prose that already exists ----------

test("plain prose keeps its paragraphs and its line breaks", async ({ page }) => {
  const html = await render(page,
    "Vergleiche die Angaben mit den Anlagen.\nPrüfe besonders das Datum.\n\nFehlt etwas, lehne ab.");

  // Two paragraphs, and inside the first the author's own line break — not a reflow.
  expect(html).toBe("<p>Vergleiche die Angaben mit den Anlagen.<br>Prüfe besonders das Datum.</p>"
    + "\n<p>Fehlt etwas, lehne ab.</p>");
});

test("a variable name is a name, not emphasis", async ({ page }) => {
  // The one thing documentation prose is full of that Markdown would otherwise eat.
  const html = await render(page, "Die Variablen order_id und MAX_RETRIES bleiben stehen.");
  expect(html).toBe("<p>Die Variablen order_id und MAX_RETRIES bleiben stehen.</p>");

  // Arithmetic with asterisks is not emphasis either: an opening delimiter needs a
  // non-space right after it.
  expect(await render(page, "Aufschlag: 5 * 3 * 2 Prozent")).toBe("<p>Aufschlag: 5 * 3 * 2 Prozent</p>");
});

// ---------- what authors write ----------

test("a backslash keeps a marker as a character", async ({ page }) => {
  // The escape hatch for prose that is *about* markup — and for the angle brackets an
  // author writes around a placeholder.
  expect(await render(page, "Ein \\*Stern\\* und \\<Platzhalter\\>"))
    .toBe("<p>Ein *Stern* und &lt;Platzhalter&gt;</p>");
});

test("headings, lists and inline marks render as structure", async ({ page }) => {
  await render(page, [
    "## Prüfschritte",
    "",
    "- Ausweis **vollständig**",
    "- Vertrag *unterschrieben*",
    "",
    "1. Erst prüfen",
    "2. Dann freigeben",
  ].join("\n"));

  expect(await page.evaluate(() => window.__tags()))
    .toEqual(["h2", "ul", "li", "strong", "li", "em", "ol", "li", "li"]);
  await expect(page.locator("#out h2")).toHaveText("Prüfschritte");
  await expect(page.locator("#out ul li").first()).toHaveText("Ausweis vollständig");
});

test("a numbered list starts where the author started it", async ({ page }) => {
  const html = await render(page, "3. dritter Schritt\n4. vierter Schritt");
  expect(html).toContain('<ol start="3">');
});

test("an item carries its nested list, and a blank line between items spaces them out", async ({ page }) => {
  const nested = await render(page, "1. Dann das\n   - Unterpunkt\n   - noch einer");
  expect(nested).toBe("<ol><li>Dann das\n<ul><li>Unterpunkt</li><li>noch einer</li></ul></li></ol>");

  // A loose list keeps the <p> per item — the distinction that decides whether the
  // items read as a tight enumeration or as spaced-out prose.
  const loose = await render(page, "- Erst dies\n\n- Dann das");
  expect(loose).toBe("<ul><li><p>Erst dies</p></li><li><p>Dann das</p></li></ul>");
});

test("code keeps every character the author put in it", async ({ page }) => {
  await render(page, "Der Ausdruck:\n\n```feel\nbetrag > 1000 and status = \"offen\"\n```");

  await expect(page.locator("#out pre code")).toHaveText('betrag > 1000 and status = "offen"');
  await expect(page.locator("#out pre code")).toHaveClass("language-feel");

  // Inline code is literal too: an underscore inside it is part of the name.
  const html = await render(page, "Setze `order_id` auf den *Wert*.");
  expect(html).toBe("<p>Setze <code>order_id</code> auf den <em>Wert</em>.</p>");
});

test("a block quote and a thematic break survive", async ({ page }) => {
  await render(page, "> Frist beachten.\n> Sonst verfällt der Antrag.\n\n---\n\nDanach.");
  // The <br> inside the quote is the author's own line break, kept like everywhere else.
  expect(await page.evaluate(() => window.__tags())).toEqual(["blockquote", "p", "br", "hr", "p"]);
});

// ---------- safety ----------

test("HTML in a documentation text is text, and nothing executes", async ({ page }) => {
  const html = await render(page, [
    "<script>window.__pwned('script')</script>",
    "",
    "<img src=x onerror=\"window.__pwned('img')\">",
    "",
    "<div onmouseover=\"window.__pwned('div')\">hover</div>",
  ].join("\n"));

  // The parser built paragraphs and nothing else: no script, no img, no div.
  expect(await page.evaluate(() => window.__tags())).toEqual(["p", "p", "p"]);
  expect(html).toContain("&lt;script&gt;");
  expect(await page.evaluate(() => window.__executed), "nothing ran").toEqual([]);

  // And the reader sees what the author typed, rather than a blank where markup was.
  await expect(page.locator("#out")).toContainText("<script>window.__pwned('script')</script>");
});

test("a dangerous link loses its href and keeps its words", async ({ page }) => {
  for (const url of ["javascript:window.__pwned('href')", "JaVaScRiPt:alert", "vbscript:x",
    "data:text/html;base64,PHNjcmlwdD4=", "//evil.example/phish"]) {
    const html = await render(page, `Siehe [den Antrag](${url}) im Archiv.`);
    expect(await page.evaluate(() => window.__links()), `no anchor for ${url}`).toEqual([]);
    // The label is still readable — a refused link must not silently delete the sentence.
    expect(html, `label kept for ${url}`).toContain("den Antrag");
  }
  expect(await page.evaluate(() => window.__executed)).toEqual([]);
});

test("a quote in a destination stays inside the href instead of opening an attribute", async ({ page }) => {
  await render(page, '[x](https://ok.example/p"onmouseover="window.__pwned(1))');

  // Either it is not a link at all or it is one whose href holds the quote as a
  // character. What it must never be is an element with an extra attribute.
  const attrs = await page.evaluate(() =>
    [...document.querySelectorAll("#out *")].flatMap((el) => [...el.attributes].map((a) => a.name)));
  expect(attrs.filter((a) => a.startsWith("on"))).toEqual([]);
  expect(await page.evaluate(() => window.__executed)).toEqual([]);
});

test("a safe link resolves where it says, and an external one opens away from the app", async ({ page }) => {
  await render(page, "Siehe [das Handbuch](/handbuch.html), [die Aufgaben](#/tasks) "
    + "und [die Norm](https://example.org/iso_9001).");

  const links = await page.evaluate(() => window.__links());
  expect(links.map((l) => l.text)).toEqual(["das Handbuch", "die Aufgaben", "die Norm"]);

  // In-app destinations stay in the app: same origin, no new tab.
  expect(links[0].protocol).toBe("http:");
  expect(links[0].target).toBe("");
  expect(links[1].attr).toBe("#/tasks");
  expect(links[1].target).toBe("");

  // An external one leaves the console, so it opens in its own tab and cannot reach
  // back through window.opener.
  expect(links[2].href).toBe("https://example.org/iso_9001");
  expect(links[2].target).toBe("_blank");
  expect(links[2].rel).toBe("noopener noreferrer");
});

// ---------- the one-line form ----------

test("markdownToPlain flattens prose for a place with no room for markup", async ({ page }) => {
  const src = "## Prüfschritte\n\n- Ausweis **vollständig**\n- `order_id` gesetzt\n\nSiehe [Handbuch](/h.html).";
  expect(await plain(page, src))
    .toBe("Prüfschritte Ausweis vollständig order_id gesetzt Siehe Handbuch.");

  // It is plain text, not HTML: markup in the source stays visible characters, so a
  // caller that escapes it (they all do) shows the author's angle brackets.
  expect(await plain(page, "<b>fett</b>")).toBe("<b>fett</b>");
  expect(await plain(page, "  ")).toBe("");
});
