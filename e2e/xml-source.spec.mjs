// The Modeler's source view (api/web/xml-source.js): the document behind a diagram,
// shown as text and, when it is corrected there, applied back onto the canvas.
//
// A diagram and a decision are XML underneath, and some corrections are easier to make
// there than on the canvas: an id that has to change everywhere at once, an attribute the
// properties panel does not offer, a reference a hand-written model got wrong. Export XML
// hands the document over as a file; this puts it in front of the author in place, and
// takes it back.
//
// What is worth pinning is what that costs if it goes wrong, because every way it can go
// wrong is quiet: a malformed edit must not reach the canvas, an edit the modeler cannot
// read must leave the diagram as it was rather than empty, and an applied edit must count
// as an edit — it replaces the canvas through an import, which the undo stack never sees,
// so without care the editor would believe it had nothing to lose, let the author leave
// without asking, and let a collaborator's change overwrite it.
//
// The vendored bpmn-js and dmn-js bundles are loaded for real (the static server serves
// ../api/web), so what is asserted here is the editor that ships.
import { test, expect } from "@playwright/test";

// ---------------------------------------------------------------------------------
// BPMN — the process editor (xml-source-harness.html mounts the real editor.js)
// ---------------------------------------------------------------------------------

async function mountBpmn(page, mode = "__mount") {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/xml-source-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((m) => window[m](), mode);
  await expect(page.locator("#bar-more")).toBeVisible();
  await expect(page.locator('[data-element-id="Task_1"]').first()).toBeVisible();
}

async function openBpmnSource(page) {
  await page.locator("#bar-more").click();
  await page.locator("#bar-menu #xmlsource").click();
  await expect(page.locator(".xml-modal")).toBeVisible();
}

const taskName = (page) => page.evaluate(() =>
  window.__atlasModeler.get("elementRegistry").get("Task_1").businessObject.name);

test("the menu offers the source beside Export XML, and it is the diagram on screen", async ({ page }) => {
  await mountBpmn(page);
  // An edit made on the canvas and not saved anywhere is in the source: it is what the
  // author is looking at, not what the server last stored.
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    m.get("modeling").updateLabel(m.get("elementRegistry").get("End_1"), "Erledigt");
  });

  await page.locator("#bar-more").click();
  const entry = page.locator("#bar-menu #xmlsource");
  await expect(entry).toBeVisible();
  await expect(entry).toContainText("XML-Quelltext");
  // It sits with the other acts on the document, right after Export XML.
  await expect(page.locator("#bar-menu #export + #xmlsource")).toHaveCount(1);
  await entry.click();

  const modal = page.locator(".xml-modal");
  await expect(modal).toBeVisible();
  await expect(modal).toHaveAttribute("role", "dialog");
  const text = await page.locator(".xml-ta").inputValue();
  expect(text).toContain('<bpmn:userTask id="Task_1" name="Pruefen"');
  expect(text).toContain('name="Erledigt"');
  // Highlighted and numbered — the code-editor surface every other code field uses.
  await expect(modal.locator(".code-editor.has-gutter")).toBeVisible();
  await expect(modal.locator(".tok-html-tag").first()).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("a large document draws only the lines in view, each where the text area has it", async ({ page }) => {
  await mountBpmn(page);
  await openBpmnSource(page);

  // Three thousand numbered lines, one of them far wider than the dialog. A document
  // that size re-drawn whole on every keystroke took the better part of a second per
  // key, so the view draws the lines in view and keeps them on the text area's lines.
  await page.evaluate(() => {
    const ta = document.querySelector(".xml-ta");
    const rows = [];
    for (let i = 1; i <= 3000; i++) rows.push(`  <!-- row ${i} -->${i === 1500 ? " <x a=\"" + "w".repeat(400) + "\" tail=\"end\"/>" : ""}`);
    ta.value = `<?xml version="1.0"?>\n<doc>\n${rows.join("\n")}\n</doc>`;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const drawn = await page.locator(".xml-modal .ce-ln").count();
  expect(drawn, "only the lines in view (and a margin) are drawn").toBeLessThan(200);
  expect(drawn).toBeGreaterThan(5);

  // Scroll to the middle; line 1502 holds "row 1500".
  await page.evaluate(() => {
    const ta = document.querySelector(".xml-ta");
    ta.scrollTop = 1495 * parseFloat(getComputedStyle(ta).lineHeight);
    ta.dispatchEvent(new Event("scroll"));
  });
  // Compared by the middle of the line: an inline span's box is its font's height, set
  // inside the taller line, so its top is never the line's top, but its middle is.
  const pos = await page.evaluate(() => {
    const ta = document.querySelector(".xml-ta");
    const cs = getComputedStyle(ta);
    const lh = parseFloat(cs.lineHeight);
    const want = ta.getBoundingClientRect().top + parseFloat(cs.paddingTop) +
      (1502 - 1) * lh - ta.scrollTop + lh / 2;
    const mid = (el) => { const r = el.getBoundingClientRect(); return (r.top + r.bottom) / 2; };
    const span = [...document.querySelectorAll(".xml-modal .ce-highlight .tok-comment")]
      .find((s) => s.textContent.includes("row 1500 "));
    const ln = [...document.querySelectorAll(".xml-modal .ce-ln")].find((d) => d.textContent === "1502");
    return { want, text: span ? mid(span) : null, gutter: ln ? mid(ln) : null };
  });
  expect(pos.text, "the highlighted line is drawn").not.toBeNull();
  expect(Math.abs(pos.text - pos.want), "the text sits on the text area's line").toBeLessThan(1.5);
  expect(Math.abs(pos.gutter - pos.want), "the number sits on its line").toBeLessThan(1.5);

  // Sideways too: the wide line moves with the text area's horizontal scroll.
  const shift = await page.evaluate(() => {
    const ta = document.querySelector(".xml-ta");
    const tail = () => [...document.querySelectorAll(".xml-modal .ce-highlight .tok-html-attr")]
      .find((s) => s.textContent === "tail");
    const before = tail().getBoundingClientRect().left;
    ta.scrollLeft = 200;
    ta.dispatchEvent(new Event("scroll"));
    return before - tail().getBoundingClientRect().left;
  });
  expect(Math.abs(shift - 200)).toBeLessThan(1.5);
  expect(page.__errors).toEqual([]);
});

test("Apply puts a corrected source on the canvas", async ({ page }) => {
  await mountBpmn(page);
  await openBpmnSource(page);
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue()).replace('name="Pruefen"', 'name="Freigeben"'));
  await page.locator(".xml-modal [data-apply]").click();

  await expect(page.locator(".xml-modal")).toHaveCount(0);
  expect(await taskName(page)).toBe("Freigeben");
  await expect(page.locator('[data-element-id="Task_1"]').first()).toContainText("Freigeben");
  await expect.poll(() => page.evaluate(() => window.__toasts.map((t) => t.msg).join("\n"))).toContain("XML übernommen");
  expect(page.__errors).toEqual([]);
});

test("malformed XML is refused with the line it breaks on, and the canvas keeps what it had", async ({ page }) => {
  await mountBpmn(page);
  await openBpmnSource(page);
  const ta = page.locator(".xml-ta");
  const lines = (await ta.inputValue()).split("\n");
  const at = lines.findIndex((l) => l.includes('<bpmn:userTask id="Task_1"'));
  expect(at).toBeGreaterThan(0);
  // An unclosed tag: the document is no longer XML at all.
  lines[at] = lines[at].replace('name="Pruefen">', 'name="Pruefen"');
  await ta.fill(lines.join("\n"));

  // Said while typing, before Apply is ever pressed: the status names the line. Where a
  // parser reports a mismatch depends on the parser, so the assertion is that it is a
  // line at or after the break, not a particular one.
  const status = page.locator(".xml-modal .xml-status");
  await expect(status).toHaveClass(/\berr\b/);
  await expect(status).toContainText(/Zeile \d+/i);
  const named = Number(/Zeile (\d+)/i.exec(await status.textContent())[1]);
  expect(named).toBeGreaterThanOrEqual(at + 1);

  await page.locator(".xml-modal [data-apply]").click();
  // Refused: still open, still the author's text to fix, and the diagram untouched —
  // and the text is scrolled to the line, which the gutter marks.
  await expect(page.locator(".xml-modal")).toBeVisible();
  await expect(status).toContainText(/Zeile \d+/i);
  await expect(page.locator(".xml-modal .ce-ln.err")).toHaveText(String(named));
  expect(await ta.inputValue()).toBe(lines.join("\n"));
  expect(await taskName(page)).toBe("Pruefen");
  expect(page.__errors).toEqual([]);
});

// Two ways a well-formed document can fail to import, and bpmn-js treats them
// differently: one that is not BPMN at all is refused while it is parsed, with the
// canvas untouched; one that is BPMN but has nothing to draw — no diagram, no process —
// is refused only after bpmn-js has cleared the canvas. The second is the one the
// rollback exists for.
const EMPTY_BPMN = '<?xml version="1.0"?>\n<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="Empty" targetNamespace="http://atlas/bpmn"></bpmn:definitions>';
for (const [what, doc] of [
  ["not BPMN at all", '<?xml version="1.0"?>\n<notes><note>nothing to draw</note></notes>'],
  ["BPMN with nothing to draw", EMPTY_BPMN],
]) {
  test(`XML the modeler cannot read leaves the diagram as it was: ${what}`, async ({ page }) => {
    await mountBpmn(page);
    await openBpmnSource(page);
    // Well-formed, so it passes the first check.
    await page.locator(".xml-ta").fill(doc);
    await page.locator(".xml-modal [data-apply]").click();

    const status = page.locator(".xml-modal .xml-status");
    await expect(status).toHaveClass(/\berr\b/);
    await expect(status).toContainText("unverändert");
    await expect(page.locator(".xml-modal")).toBeVisible();
    // Not an empty canvas: whatever the import did to it was put back.
    expect(await taskName(page)).toBe("Pruefen");
    await expect(page.locator('[data-element-id="Call_1"]').first()).toBeVisible();
    expect(page.__errors).toEqual([]);
  });
}

test("closing with an unapplied edit asks first; closing an untouched source does not", async ({ page }) => {
  await mountBpmn(page);
  const asked = [];
  let answer = false;
  page.on("dialog", (d) => { asked.push(d.message()); answer ? d.accept() : d.dismiss(); });

  // Untouched: Escape just closes it.
  await openBpmnSource(page);
  await page.keyboard.press("Escape");
  await expect(page.locator(".xml-modal")).toHaveCount(0);
  expect(asked).toEqual([]);

  // Edited and not applied: the edit is the author's work, so it is not thrown away on
  // a stray Escape.
  await openBpmnSource(page);
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue()).replace('name="Pruefen"', 'name="Freigeben"'));
  await page.keyboard.press("Escape");
  await expect.poll(() => asked.length).toBe(1);
  expect(asked[0]).toContain("verwerfen");
  await expect(page.locator(".xml-modal")).toBeVisible();

  answer = true;
  await page.locator(".xml-modal [data-cancel]").click();
  await expect(page.locator(".xml-modal")).toHaveCount(0);
  expect(await taskName(page)).toBe("Pruefen");
  expect(page.__errors).toEqual([]);
});

test("the editor's shortcuts do not act behind the dialog", async ({ page }) => {
  await mountBpmn(page);
  await openBpmnSource(page);
  await page.evaluate(() => { window.__calls.length = 0; });

  // F4 toggles Variables and F8 runs Auto-layout from anywhere in the editor — both bound
  // on the document, ahead of anything a dialog binds there. Typed into the source, or
  // with a dialog button focused, neither may reach the canvas the dialog is covering.
  // Each F4 is checked on its own: two toggles would cancel out and prove nothing.
  await page.locator(".xml-ta").press("F4");
  await page.waitForTimeout(150);
  await expect(page.locator("#vars-panel")).toBeHidden();
  await page.locator(".xml-ta").press("F8");
  await page.locator(".xml-modal [data-copy]").focus();
  await page.keyboard.press("F4");
  await page.waitForTimeout(150);
  await expect(page.locator("#vars-panel")).toBeHidden();
  await page.keyboard.press("F8");
  await page.waitForTimeout(300);
  const layouts = await page.evaluate(() => window.__calls.filter((c) => /\/layout$/.test(c.url)).length);
  expect(layouts, "Auto-layout must not run under the dialog").toBe(0);
  await expect(page.locator(".xml-modal")).toBeVisible();

  // And once it is closed, they are the editor's again.
  await page.keyboard.press("Escape");
  await expect(page.locator(".xml-modal")).toHaveCount(0);
  await page.keyboard.press("F4");
  await expect(page.locator("#vars-panel")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("an applied source counts as an edit: leaving a deployment asks first", async ({ page }) => {
  await mountBpmn(page);
  const asked = [];
  page.on("dialog", (d) => { asked.push(d.message()); d.dismiss(); });

  await openBpmnSource(page);
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue()).replace('name="Pruefen"', 'name="Freigeben"'));
  await page.locator(".xml-modal [data-apply]").click();
  await expect(page.locator(".xml-modal")).toHaveCount(0);

  // The import cleared the undo stack, which is what "has this diagram been edited"
  // used to be read from. A deployment has no draft to put the edit in, so opening the
  // called process has to ask rather than discard it.
  const marker = await page.locator('[data-element-id="Call_1"] path[data-marker="sub-process"]').boundingBox();
  await page.mouse.dblclick(marker.x + marker.width / 2, marker.y + marker.height / 2);
  await expect.poll(() => asked.length).toBe(1);
  expect(asked[0]).toContain("ungespeicherte Änderungen");
  expect(await page.evaluate(() => location.hash)).toBe("");
  expect(page.__errors).toEqual([]);
});

test("Save writes the applied source as the draft", async ({ page }) => {
  await mountBpmn(page, "__mountDraft");
  await openBpmnSource(page);
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue()).replace('name="Pruefen"', 'name="Freigeben"'));
  await page.locator(".xml-modal [data-apply]").click();
  await expect(page.locator(".xml-modal")).toHaveCount(0);

  await page.evaluate(() => { window.__calls.length = 0; });
  await page.locator("#save").click();
  await expect.poll(() => page.evaluate(() =>
    window.__calls.filter((c) => c.method === "POST" && c.url.startsWith("/api/v1/drafts")).length)).toBe(1);
  const body = await page.evaluate(() =>
    window.__calls.find((c) => c.method === "POST" && c.url.startsWith("/api/v1/drafts")).body);
  expect(body).toContain('name="Freigeben"');
  expect(page.__errors).toEqual([]);
});

test("a collaborator's change does not overwrite an applied source", async ({ page }) => {
  await mountBpmn(page, "__mountDraft");
  // The live session is up (ADR-0140): this editor is a participant.
  await page.evaluate(() => window.__es.emit("sync", { self: "me", canEdit: true, participants: [], locks: [] }));

  await openBpmnSource(page);
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue()).replace('name="Pruefen"', 'name="Freigeben"'));
  await page.locator(".xml-modal [data-apply]").click();
  await expect(page.locator(".xml-modal")).toHaveCount(0);

  // A peer saves. The session would pull their draft into this canvas — which holds an
  // applied edit nobody has saved, so it must wait for this editor's Save instead.
  await page.evaluate(() => { window.__calls.length = 0; });
  await page.evaluate(() => window.__es.emit("change", { by: "peer", elementId: "End_1" }));
  await expect.poll(() => page.evaluate(() => window.__toasts.map((t) => t.msg).join("\n")))
    .toContain("Jemand hat das Diagramm geändert");
  await page.waitForTimeout(600); // past the session's 300 ms coalescing window
  const fetched = await page.evaluate(() =>
    window.__calls.filter((c) => c.method === "GET" && /\/drafts\/[^/]+\/xml$/.test(c.url)).length);
  expect(fetched, "the peer's draft must not be pulled over the applied edit").toBe(0);
  expect(await taskName(page)).toBe("Freigeben");
  expect(page.__errors).toEqual([]);
});

// Auto-layout replaces the canvas by the same kind of import — the server's re-flowed
// document, read back in — and so had the same blind spot: the undo stack it reads
// "anything to lose?" from came back empty, and the live session was never told.

async function autoLayout(page) {
  await page.locator("#bar-more").click();
  await page.locator("#bar-menu #autolayout").click();
  await expect.poll(() => page.evaluate(() => window.__toasts.map((t) => t.msg).join("\n"))).toContain("automatisch angeordnet");
}

test("an auto-layout counts as an edit: leaving a deployment asks first", async ({ page }) => {
  await mountBpmn(page);
  const asked = [];
  page.on("dialog", (d) => { asked.push(d.message()); d.dismiss(); });
  await autoLayout(page);

  const marker = await page.locator('[data-element-id="Call_1"] path[data-marker="sub-process"]').boundingBox();
  await page.mouse.dblclick(marker.x + marker.width / 2, marker.y + marker.height / 2);
  await expect.poll(() => asked.length).toBe(1);
  expect(asked[0]).toContain("ungespeicherte Änderungen");
  expect(await page.evaluate(() => location.hash)).toBe("");
  expect(page.__errors).toEqual([]);
});

test("a collaborator's change does not overwrite an auto-layout", async ({ page }) => {
  await mountBpmn(page, "__mountDraft");
  await page.evaluate(() => window.__es.emit("sync", { self: "me", canEdit: true, participants: [], locks: [] }));
  await autoLayout(page);

  await page.evaluate(() => { window.__calls.length = 0; });
  await page.evaluate(() => window.__es.emit("change", { by: "peer", elementId: "End_1" }));
  await expect.poll(() => page.evaluate(() => window.__toasts.map((t) => t.msg).join("\n")))
    .toContain("Jemand hat das Diagramm geändert");
  await page.waitForTimeout(600);
  const fetched = await page.evaluate(() =>
    window.__calls.filter((c) => c.method === "GET" && /\/drafts\/[^/]+\/xml$/.test(c.url)).length);
  expect(fetched, "the peer's draft must not be pulled over the re-flowed layout").toBe(0);
  expect(page.__errors).toEqual([]);
});

test("an auto-layout the modeler cannot read leaves the diagram as it was", async ({ page }) => {
  await mountBpmn(page);
  // The server answers with BPMN that has nothing to draw. The import fails — and
  // bpmn-js has cleared the canvas by then, so without a rollback the author is left
  // looking at nothing.
  await page.evaluate((empty) => {
    const m = window.__atlasModeler;
    const real = m.importXML.bind(m);
    let first = true;
    m.importXML = (xml) => {
      if (first) { first = false; return real(empty); }
      return real(xml);
    };
  }, EMPTY_BPMN);
  await page.locator("#bar-more").click();
  await page.locator("#bar-menu #autolayout").click();
  await expect.poll(() => page.evaluate(() => window.__toasts.map((t) => t.msg).join("\n"))).toContain("Automatisches Anordnen fehlgeschlagen");
  expect(await taskName(page)).toBe("Pruefen");
  await expect(page.locator('[data-element-id="Call_1"]').first()).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("Copy puts the source on the clipboard", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await mountBpmn(page);
  await openBpmnSource(page);
  await page.locator(".xml-modal [data-copy]").click();
  await expect(page.locator(".xml-modal .xml-status")).toContainText("In die Zwischenablage kopiert");
  const copied = await page.evaluate(() => navigator.clipboard.readText());
  expect(copied).toBe(await page.locator(".xml-ta").inputValue());
  expect(page.__errors).toEqual([]);
});

// ---------------------------------------------------------------------------------
// DMN — the decision editor (the Console, with the server stubbed)
// ---------------------------------------------------------------------------------

const DMN_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="Definitions_src" name="Eligibility" namespace="http://atlas/dmn">
  <decision id="Decision_src" name="eligibility">
    <decisionTable id="DecisionTable_src" hitPolicy="UNIQUE">
      <input id="Input_src" label="amount">
        <inputExpression id="InputExpression_src" typeRef="number"><text>amount</text></inputExpression>
      </input>
      <output id="Output_src" name="result" typeRef="string" />
      <rule id="Rule_src">
        <inputEntry id="InputEntry_src"><text>&gt;= 100</text></inputEntry>
        <outputEntry id="OutputEntry_src"><text>"approve"</text></outputEntry>
      </rule>
    </decisionTable>
  </decision>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_src">
      <dmndi:DMNShape id="DMNShape_src" dmnElementRef="Decision_src">
        <dc:Bounds height="80" width="180" x="160" y="100" />
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

// stubDecisionServer answers what opening one stored decision asks for, and records
// the draft saves so a test can see what Save sent. With `draft`, the decision has a
// draft — which is what a live session is keyed on (ADR-0323) — and every read of the
// draft's XML is counted, since that read is how a peer's change arrives.
function stubDecisionServer(page, { draft = false } = {}) {
  const draftSaves = [];
  const draftReads = [];
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (/\/session\/(presence|lock|change|leave)$/.test(path)) return route.fulfill({ status: 204, body: "" });
    if (path === "/api/v1/dmn-drafts" && request.method() === "GET") {
      return route.fulfill({ json: draft ? [{ id: "ref-1", refId: "ref-1", modelRef: "eligibility", projectId: "app-1" }] : [] });
    }
    if (path === "/api/v1/dmn-drafts/ref-1/xml") {
      draftReads.push(path);
      return route.fulfill({ body: DMN_XML, contentType: "application/xml" });
    }
    if (path === "/api/v1/dmn-layout" && request.method() === "POST") {
      return route.fulfill({ body: DMN_XML.replace('x="160" y="100"', 'x="60" y="60"'), contentType: "application/xml" });
    }
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: "app-1", name: "Order Management", myRole: "owner" }] });
    if (path === "/api/v1/dmnrefs" && request.method() === "GET") {
      return route.fulfill({ json: [{ id: "ref-1", name: "Eligibility", modelRef: "eligibility", projectId: "app-1" }] });
    }
    if (path === "/api/v1/dmn-drafts" && request.method() === "POST") {
      const payload = request.postDataJSON();
      draftSaves.push(payload);
      return route.fulfill({ json: { ...payload, id: payload.id || "dd-new", name: "Decision", savedAt: 1 } });
    }
    if (path.startsWith("/api/v1/dmn-models/") && path.endsWith("/xml")) {
      return route.fulfill({ body: DMN_XML, contentType: "application/xml" });
    }
    return route.fulfill({ json: [] });
  });
  return { draftSaves, draftReads };
}

// fakeSessionStream stands in for the live session's stream before the Console loads,
// so a test can play a collaborator's frames into the decision editor.
async function fakeSessionStream(page) {
  await page.addInitScript(() => {
    window.__streams = [];
    window.EventSource = class {
      constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); }
      addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
      close() {}
      emit(type, data) { for (const fn of this.listeners[type] || []) fn({ data: JSON.stringify(data) }); }
    };
  });
}

// peerChangesTheDraft joins the session and plays a collaborator's change into it, and
// answers whether the editor then fetched the draft to pull it over its canvas.
async function peerChangesTheDraft(page, server) {
  const before = server.draftReads.length;
  await page.evaluate(() => {
    const es = window.__streams.find((s) => s.url.includes("/dmn-drafts/"));
    es.emit("change", { by: "peer", elementId: "Decision_src" });
  });
  await expect(page.locator("#toast")).toContainText("Jemand hat das Diagramm geändert");
  await page.waitForTimeout(600); // past the session's 300 ms coalescing window
  return server.draftReads.length - before;
}

async function openDecision(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await expect(page.locator(".dmn-editor .dmn-canvas .dmn-js-parent")).toBeVisible({ timeout: 20000 });
}

async function openDmnSource(page) {
  await page.locator("#dmn-more").click();
  await page.locator("#dmn-menu #dmn-xmlsource").click();
  await expect(page.locator(".xml-modal")).toBeVisible();
}

test("the decision editor offers the same source view, and Save keeps what was applied", async ({ page }) => {
  const server = stubDecisionServer(page);
  await openDecision(page);

  await page.locator("#dmn-more").click();
  await expect(page.locator("#dmn-menu #dmn-export + #dmn-xmlsource")).toHaveCount(1);
  await page.locator("#dmn-menu #dmn-xmlsource").click();
  const ta = page.locator(".xml-ta");
  expect(await ta.inputValue()).toContain('<decision id="Decision_src" name="eligibility">');

  await ta.fill((await ta.inputValue()).replace('name="eligibility"', 'name="eligibility-fixed"'));
  await page.locator(".xml-modal [data-apply]").click();
  await expect(page.locator(".xml-modal")).toHaveCount(0);
  // The views are rebuilt from the applied document, so the tab names the decision by
  // its corrected name.
  await expect(page.locator("#dmn-views button", { hasText: "eligibility-fixed" })).toBeVisible();

  await page.locator("#dmn-save").click();
  await expect.poll(() => server.draftSaves.length).toBe(1);
  expect(server.draftSaves[0].xml).toContain('name="eligibility-fixed"');
  expect(page.__errors).toEqual([]);
});

test("a changed DMN version is refused by name, and the decision stays as it was", async ({ page }) => {
  stubDecisionServer(page);
  await openDecision(page);
  await openDmnSource(page);

  // The editor's moddle is chosen for one DMN version when the page opens and cannot
  // change under it; importing a 1.5 document into it fails with a message about
  // parsing that says nothing of versions. So it is refused before that, in words.
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue())
    .replace("https://www.omg.org/spec/DMN/20191111/MODEL/", "https://www.omg.org/spec/DMN/20230324/MODEL/")
    .replace('name="eligibility"', 'name="eligibility-15"'));
  await page.locator(".xml-modal [data-apply]").click();

  const status = page.locator(".xml-modal .xml-status");
  await expect(status).toHaveClass(/\berr\b/);
  await expect(status).toContainText("DMN 1.5");
  await expect(status).toContainText("DMN 1.3");
  await expect(page.locator(".xml-modal")).toBeVisible();
  page.once("dialog", (d) => d.accept()); // discarding the refused edit
  await page.keyboard.press("Escape");
  await expect(page.locator(".xml-modal")).toHaveCount(0);
  await expect(page.locator("#dmn-views button", { hasText: "eligibility-15" })).toHaveCount(0);
  await expect(page.locator("#dmn-views button", { hasText: "eligibility" }).first()).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("in the decision editor, a collaborator's change waits for an applied source", async ({ page }) => {
  await fakeSessionStream(page);
  const server = stubDecisionServer(page, { draft: true });
  await openDecision(page);
  await page.evaluate(() => window.__streams.find((s) => s.url.includes("/dmn-drafts/"))
    .emit("sync", { self: "me", canEdit: true, participants: [], locks: [] }));

  await openDmnSource(page);
  const ta = page.locator(".xml-ta");
  await ta.fill((await ta.inputValue()).replace('name="eligibility"', 'name="eligibility-fixed"'));
  await page.locator(".xml-modal [data-apply]").click();
  await expect(page.locator(".xml-modal")).toHaveCount(0);

  expect(await peerChangesTheDraft(page, server), "the peer's draft must wait for this one's Save").toBe(0);
  await expect(page.locator("#dmn-views button", { hasText: "eligibility-fixed" })).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("in the decision editor, a collaborator's change waits for an auto-layout", async ({ page }) => {
  await fakeSessionStream(page);
  const server = stubDecisionServer(page, { draft: true });
  await openDecision(page);
  await page.evaluate(() => window.__streams.find((s) => s.url.includes("/dmn-drafts/"))
    .emit("sync", { self: "me", canEdit: true, participants: [], locks: [] }));

  await page.locator("#dmn-more").click();
  await page.locator("#dmn-menu #dmn-autolayout").click();
  await expect(page.locator("#toast")).toContainText("angeordnet");

  expect(await peerChangesTheDraft(page, server), "the peer's draft must wait for this one's Save").toBe(0);
  expect(page.__errors).toEqual([]);
});

// ---------------------------------------------------------------------------------
// Forms — a form is a form-js schema, which is JSON, and the form editor has carried
// it as a tab of its own since ADR-0028. The tab says what it is.
// ---------------------------------------------------------------------------------

test("the form editor's source is a tab that says so, and an edit there is what Save writes", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountDivergent());

  const tab = page.locator('#form-tabs [data-ftab="editor"]');
  await expect(tab).toHaveText("Quelltext");
  await tab.click();
  const ta = page.locator("#fv-schema");
  await expect(ta).toBeVisible();
  const schema = JSON.parse(await ta.inputValue());
  schema.components = [{ type: "textfield", key: "kunde", label: "Kunde" }];
  await ta.fill(JSON.stringify(schema, null, 2));

  await page.evaluate(() => window.__save());
  await expect.poll(() => page.evaluate(() => window.__saved)).not.toBeNull();
  const saved = await page.evaluate(() => window.__saved);
  expect(saved.schema.components[0].key).toBe("kunde");
  expect(errors).toEqual([]);
});
