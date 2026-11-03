// End-to-end coverage for the height of a decision table's rules (api/web/app.css).
//
// Two of the console's own table rules reached into the vendored dmn-js table. dmn-js
// marks an editable whose value is unset with the class `empty` — its own state flag,
// the one form-js puts on an unset properties group (form-properties.spec.mjs) — and a
// rule's annotation is such an editable. app.css's bare `.empty` is our "nothing here
// yet" placeholder, 34px of padding all round, so every rule without an annotation —
// which is nearly every rule — grew a 89px annotation and the row with it: a rule of
// one line was 98px tall. Under it, the console's `tbody td` padding gave the rule
// number 11px above and below where dmn-js gives it none.
//
// Both rules are now held off the dmn-js tree rather than overridden inside it, so the
// vendor's own styling stands. These tests hold the outcome: a rule is as tall as what
// it says, annotated or not.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1500, height: 900 } });

// One-line entries throughout, so every rule should be one line tall. The second rule
// carries an annotation and the others do not — the difference that used to decide
// a row's height.
const XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="Definitions_acc" name="Equipment" namespace="http://atlas/dmn">
  <decision id="Decision_acc" name="accessories">
    <decisionTable id="DecisionTable_acc" hitPolicy="COLLECT">
      <input id="Input_model" label="Model">
        <inputExpression id="InputExpression_model" typeRef="string"><text>model</text></inputExpression>
      </input>
      <input id="Input_site" label="Site">
        <inputExpression id="InputExpression_site" typeRef="string"><text>site</text></inputExpression>
      </input>
      <output id="Output_value" name="value" typeRef="string" />
      <rule id="Rule_dock">
        <inputEntry id="IE_dock_model"><text>"x1","latitude"</text></inputEntry>
        <inputEntry id="IE_dock_site"><text>-</text></inputEntry>
        <outputEntry id="OE_dock"><text>"dock"</text></outputEntry>
      </rule>
      <rule id="Rule_bag">
        <description>every laptop</description>
        <inputEntry id="IE_bag_model"><text>"x1","mbp"</text></inputEntry>
        <inputEntry id="IE_bag_site"><text>-</text></inputEntry>
        <outputEntry id="OE_bag"><text>"bag"</text></outputEntry>
      </rule>
      <rule id="Rule_hub">
        <inputEntry id="IE_hub_model"><text>"mbp"</text></inputEntry>
        <inputEntry id="IE_hub_site"><text>-</text></inputEntry>
        <outputEntry id="OE_hub"><text>"hub"</text></outputEntry>
      </rule>
    </decisionTable>
  </decision>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_acc">
      <dmndi:DMNShape id="DMNShape_acc" dmnElementRef="Decision_acc">
        <dc:Bounds height="80" width="180" x="160" y="100" />
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  // The server, as far as opening one stored decision needs it.
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: "app-1", name: "Equipment", myRole: "owner" }] });
    if (path === "/api/v1/dmnrefs") {
      return route.fulfill({ json: [{ id: "ref-1", name: "Equipment", modelRef: "equipment", projectId: "app-1" }] });
    }
    if (path.startsWith("/api/v1/dmn-models/") && path.endsWith("/xml")) {
      return route.fulfill({ body: XML, contentType: "application/xml" });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await expect(page.locator(".dmn-editor .dmn-canvas .dmn-js-parent")).toBeVisible({ timeout: 20000 });
  await page.locator(".editor-bar .etabs#dmn-views button", { hasText: "accessories" }).click();
  await expect(page.locator(".tjs-table tbody tr")).toHaveCount(3);
});

// rows measures each rule: its height inside its borders, and the tallest thing
// written in it — the entries and the annotation, whichever runs to more lines. The
// borders are left out because dmn-js draws none above the first rule and one more
// under the last, so a table's rules differ by a pixel there by design.
const rows = (page) => page.locator(".tjs-table tbody tr").evaluateAll((trs) =>
  trs.map((tr) => ({
    height: tr.querySelector("td.input-cell").clientHeight,
    content: Math.max(...[...tr.querySelectorAll(".content-editable")].map((c) => c.getBoundingClientRect().height)),
    annotated: !tr.querySelector(".annotation-editor.empty"),
  })));

test("a rule is as tall as what it says, not padded out by an unset annotation", async ({ page }) => {
  const measured = await rows(page);

  // The table has both kinds — otherwise this test proves nothing.
  expect(measured.some((r) => r.annotated)).toBe(true);
  expect(measured.some((r) => !r.annotated)).toBe(true);

  // A cell's 4px above and below are all a row adds to its content; the
  // placeholder's padding added 68px more.
  for (const r of measured) expect(r.height - r.content).toBeLessThanOrEqual(10);
  // Annotated or not, three one-line rules are one height.
  expect(Math.max(...measured.map((r) => r.height)) - Math.min(...measured.map((r) => r.height))).toBeLessThan(1);
  expect(page.__errors).toEqual([]);
});

test("the table keeps dmn-js's own cell styling, not the console's", async ({ page }) => {
  const styles = await page.evaluate(() => {
    const cs = (sel) => getComputedStyle(document.querySelector(sel));
    const annotation = cs(".tjs-table tbody .annotation-editor.empty");
    const index = cs(".tjs-table tbody td.rule-index");
    return {
      annotationPadding: annotation.padding,
      annotationAlign: annotation.textAlign,
      indexPaddingTop: index.paddingTop,
      indexPaddingBottom: index.paddingBottom,
      indexBorderBottom: index.borderBottomWidth,
    };
  });
  expect(styles.annotationPadding).toBe("0px");
  expect(styles.annotationAlign).not.toBe("center"); // the placeholder centred it
  // dmn-js pads the rule number only on its right, leaving the browser's 1px above
  // and below; the console's tables pad 11px there and draw a rule under each cell.
  expect(styles.indexPaddingTop).toBe("1px");
  expect(styles.indexPaddingBottom).toBe("1px");
  expect(styles.indexBorderBottom).toBe("0px");
  expect(page.__errors).toEqual([]);
});

test("the console's own tables and placeholders keep their styling", async ({ page }) => {
  // The fix narrows two rules that are still doing their job everywhere else.
  const style = await page.evaluate(() => {
    const host = document.createElement("div");
    host.innerHTML = '<p class="empty">Nothing here yet.</p><table><tbody><tr><td>cell</td></tr></tbody></table>';
    document.body.appendChild(host);
    const p = getComputedStyle(host.querySelector(".empty"));
    const td = getComputedStyle(host.querySelector("td"));
    const out = { placeholder: p.padding, align: p.textAlign, cell: td.padding };
    host.remove();
    return out;
  });
  expect(style.placeholder).toBe("34px");
  expect(style.align).toBe("center");
  expect(style.cell).toBe("11px 12px");
  expect(page.__errors).toEqual([]);
});
