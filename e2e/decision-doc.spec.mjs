// e2e for the decision documentation collector and layout
// (api/web/decision-doc.js, ADR-draft-decision-documentation).
//
// The point of the feature is that the business rule itself — the table, its hit
// policy, what each rule matches and returns, and the prose written about it —
// reaches a reader who will never open Atlas. These tests drive the real module
// over a real DMN model, because a stub would only prove the stub.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/decision-doc-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("the collector reads the model identity and every decision it declares", async ({ page }) => {
  const c = await page.evaluate(() => window.__dmndoc.collectDecisionDocumentation(window.__xml));
  expect(c.modelName).toBe("Antragsprüfung");
  // The document is filed under the first decision the model declares, which is
  // the one the editor is about.
  expect(c.decisionId).toBe("eignung");
  expect(c.decisions.map((d) => d.id)).toEqual(["eignung", "gebuehr"]);
  expect(c.decisions[0].description).toBe("Ob der Antrag ohne weitere Prüfung angenommen wird.");
});

test("a decision carries the input data it reads, with declared types", async ({ page }) => {
  const c = await page.evaluate(() => window.__dmndoc.collectDecisionDocumentation(window.__xml));
  expect(c.decisions[0].inputs).toEqual([
    { label: "", expression: "betrag", type: "number" },
    { label: "", expression: "land", type: "string" },
  ]);
  // A decision reading nothing says so by carrying nothing, rather than by
  // inventing a field.
  expect(c.decisions[1].inputs).toEqual([]);
});

test("the rule table is captured as a table: policy, columns, and every rule", async ({ page }) => {
  const c = await page.evaluate(() => window.__dmndoc.collectDecisionDocumentation(window.__xml));
  const dec = c.decisions[0];

  // The hit policy carries its aggregation, because COLLECT SUM and COLLECT are
  // different rules.
  expect(dec.hitPolicy).toBe("COLLECT SUM");

  // An unlabelled column is described by what it evaluates, which is what it means.
  expect(dec.inputColumns).toEqual([
    { label: "Betrag", expression: "betrag", type: "number" },
    { label: "", expression: "land", type: "string" },
  ]);
  expect(dec.outputColumns).toEqual([{ label: "Urteil", expression: "urteil", type: "string" }]);

  expect(dec.rules).toHaveLength(2);
  expect(dec.rules[0].inputs).toEqual([">= 1000", '"CH"']);
  expect(dec.rules[0].outputs).toEqual(['"annehmen"']);
  // A rule's annotation says why it exists, which the grid has no room for.
  expect(dec.rules[0].description).toBe("Grosse Beträge im Inland");
  expect(dec.rules[1].inputs).toEqual(["< 1000", "-"]);
});

test("a decision whose logic is a literal expression carries the expression", async ({ page }) => {
  const c = await page.evaluate(() => window.__dmndoc.collectDecisionDocumentation(window.__xml));
  const fee = c.decisions[1];
  expect(fee.literal).toBe("0.02 * betrag");
  expect(fee.rules).toEqual([]);
  expect(fee.hitPolicy).toBe("");
});

test("a model with nothing to document collects nothing rather than throwing", async ({ page }) => {
  const empty = await page.evaluate(() => window.__dmndoc.collectDecisionDocumentation(window.__emptyXml));
  expect(empty.decisionId).toBe("");
  expect(empty.decisions).toEqual([]);
  const junk = await page.evaluate(() => window.__dmndoc.collectDecisionDocumentation("not xml at all"));
  expect(junk.decisions).toEqual([]);
});

test("the document is a PDF that says what the decision does", async ({ page }) => {
  const text = await page.evaluate(() => {
    const c = window.__dmndoc.collectDecisionDocumentation(window.__xml);
    const bytes = window.__dmndoc.buildDecisionDocumentationPdf({
      collection: c, diagram: null, title: "Antragsprüfung", note: "Freigabe März",
      version: 3, createdAt: "2026-03-01", createdBy: "pat", modelRef: "antrag",
    });
    return window.__asLatin1(bytes);
  });

  expect(text.startsWith("%PDF-")).toBe(true);
  // The title block: what this is, which version, and who signed it off.
  expect(text).toContain("Antragspr");        // the title, WinAnsi-encoded
  expect(text).toContain("Decision ID");
  expect(text).toContain("eignung");
  expect(text).toContain("v3");
  expect(text).toContain("Freigabe M");
  expect(text).toContain("antrag.dmn");

  // The rules themselves, which is the whole point.
  expect(text).toContain("hit policy COLLECT SUM");
  expect(text).toContain(">= 1000");
  expect(text).toContain("annehmen");
  expect(text).toContain("Rule 1: Grosse Betr");
  // Parentheses are escaped inside a PDF string, so the type reads as \(string\)
  // in the file itself — assert the part that is not escaped.
  expect(text).toContain("Writes: urteil");

  // And the second decision's logic, set as what it is.
  expect(text).toContain("0.02 * betrag");
});

test("publishing sends the decision, its rules and the document to its own route", async ({ page }) => {
  const calls = await page.evaluate(async () => {
    await window.__dmndoc.exportDecisionDocumentation({
      modeler: window.__modeler, api: window.__api, xml: window.__xml,
      title: "Antragsprüfung", note: "Freigabe März", modelRef: "antrag",
    });
    return window.__apiCalls;
  });

  expect(calls).toHaveLength(1);
  expect(calls[0].method).toBe("POST");
  // Filed under the decision, not the model: the version line is per decision id.
  expect(calls[0].path).toBe("/api/v1/decisions/eignung/documentation");
  expect(calls[0].body.modelName).toBe("Antragsprüfung");
  expect(calls[0].body.modelRef).toBe("antrag");
  expect(calls[0].body.xml).toContain("<decisionTable");
  expect(calls[0].body.decisions[0].rules).toHaveLength(2);
  expect(calls[0].body.pdfBase64.length).toBeGreaterThan(100);
});

test("a picture that cannot be drawn does not fail the publish", async ({ page }) => {
  // The harness's modeler always refuses to save an SVG, which is the case of a
  // model open on a decision table rather than the requirements graph. The rules
  // are the part that matters, so the document is published without the picture.
  const ok = await page.evaluate(async () => {
    const res = await window.__dmndoc.exportDecisionDocumentation({
      modeler: window.__modeler, api: window.__api, xml: window.__xml,
    });
    return !!res.id;
  });
  expect(ok).toBe(true);
});

test("a model with no decision refuses to publish, and says why", async ({ page }) => {
  const message = await page.evaluate(async () => {
    try {
      await window.__dmndoc.exportDecisionDocumentation({
        modeler: window.__modeler, api: window.__api, xml: window.__emptyXml,
      });
      return "";
    } catch (e) {
      return e.message;
    }
  });
  expect(message).toContain("no decision to document");
});
