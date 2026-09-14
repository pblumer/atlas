// e2e for co-editing a decision (ADR-draft-co-editing-a-decision).
//
// The claim is that ADR-0140's session runs over a decision draft with the same
// semantics, in an editor whose canvas is reached differently — and that the lock
// is the decision: opening a decision's table claims that decision, because a
// table row has no stable identity to lock.
//
// The real editor is mounted against a mock server, and the session's traffic is
// recorded, so what is asserted is what the editor actually sends.
import { test, expect } from "@playwright/test";

const TWO_DECISIONS = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" id="Definitions_two" name="Antrag" namespace="http://atlas/dmn">
  <decision id="eligibility" name="Eligibility">
    <decisionTable id="dt1" hitPolicy="UNIQUE">
      <input id="in1" label="amount"><inputExpression id="ie1" typeRef="number"><text>amount</text></inputExpression></input>
      <output id="out1" name="verdict" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>&gt;= 100</text></inputEntry><outputEntry id="o1"><text>"approve"</text></outputEntry></rule>
    </decisionTable>
  </decision>
  <decision id="fee" name="Fee">
    <literalExpression id="le1"><text>0.1</text></literalExpression>
  </decision>
  <dmndi:DMNDI><dmndi:DMNDiagram id="D1">
    <dmndi:DMNShape id="s1" dmnElementRef="eligibility"><dc:Bounds x="60" y="60" width="180" height="80"/></dmndi:DMNShape>
    <dmndi:DMNShape id="s2" dmnElementRef="fee"><dc:Bounds x="300" y="60" width="180" height="80"/></dmndi:DMNShape>
  </dmndi:DMNDiagram></dmndi:DMNDI>
</definitions>`;

// installSessionMock answers the editor's reads and records every session call,
// including the SSE stream the browser opens to join.
function installSessionMock(page, { draftId = "ref-1" } = {}) {
  const session = { locks: [], presence: [], joined: 0, streamPath: "" };
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

    if (path.endsWith("/session")) {
      session.joined++;
      session.streamPath = path;
      // One sync frame, then nothing: the editor needs only the self id to start
      // acting, and holding the stream open is what a real session does.
      return route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: `id: 0\nevent: sync\ndata: ${JSON.stringify({ self: "p1", canEdit: true, participants: [{ id: "p1", name: "Alice" }], locks: [] })}\n\n`,
      });
    }
    if (path.endsWith("/session/lock")) {
      session.locks.push(request.postDataJSON());
      return route.fulfill({ status: 204, body: "" });
    }
    if (path.endsWith("/session/presence")) {
      session.presence.push(request.postDataJSON());
      return route.fulfill({ status: 204, body: "" });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [] });
    if (path === "/api/v1/dmnrefs" && request.method() === "GET") {
      return route.fulfill({ json: [{ id: "ref-1", name: "Antrag", modelRef: "antrag", projectId: "" }] });
    }
    if (path === "/api/v1/dmn-drafts" && request.method() === "GET") {
      return route.fulfill({ json: [{ id: draftId, refId: "ref-1", modelRef: "antrag", name: "Antrag", savedAt: 1 }] });
    }
    if (path.startsWith("/api/v1/dmn-drafts/") && path.endsWith("/xml")) {
      return route.fulfill({ body: TWO_DECISIONS, contentType: "application/xml" });
    }
    if (path.startsWith("/api/v1/dmn-models/") && path.endsWith("/xml")) {
      return route.fulfill({ body: TWO_DECISIONS, contentType: "application/xml" });
    }
    return route.fulfill({ json: [] });
  });
  return session;
}

const editorReady = (page) =>
  expect(page.locator(".dmn-editor .dmn-canvas .dmn-js-parent")).toBeVisible({ timeout: 20000 });

test("a decision with a draft joins its own session, not a diagram's", async ({ page }) => {
  const session = installSessionMock(page);
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  await expect.poll(() => session.joined).toBeGreaterThan(0);
  // The decision's own namespace: a diagram draft with the same id is a different
  // session, which is what the dmn- prefix on the path buys.
  expect(session.streamPath).toBe("/api/v1/dmn-drafts/ref-1/session");
});

test("opening a decision's table claims that decision, and leaving it releases the claim", async ({ page }) => {
  const session = installSessionMock(page);
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);
  await expect.poll(() => session.joined).toBeGreaterThan(0);

  // Open the first decision's table. A table row has no id the session could
  // lock, so what is claimed is the decision itself.
  await page.locator("#dmn-views button", { hasText: "Eligibility" }).click();
  await expect.poll(() => session.locks.filter((l) => l.action === "acquire").map((l) => l.elementId))
    .toContain("eligibility");

  // Moving to the other decision releases the first and claims the second, so two
  // people can genuinely work on two decisions of one model at once.
  await page.locator("#dmn-views button", { hasText: "Fee" }).click();
  await expect.poll(() => session.locks.filter((l) => l.action === "release").map((l) => l.elementId))
    .toContain("eligibility");
  await expect.poll(() => session.locks.filter((l) => l.action === "acquire").map((l) => l.elementId))
    .toContain("fee");

  // Back to the graph: the table's claim is given up, because the graph claims
  // nothing until something in it is selected.
  await page.locator("#dmn-views button", { hasText: "Overview (DRG)" }).click();
  await expect.poll(() => session.locks.filter((l) => l.action === "release").map((l) => l.elementId))
    .toContain("fee");
});

test("the roster is drawn on the editor's canvas", async ({ page }) => {
  installSessionMock(page);
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);

  // Who else is here is shown where the work is, the way it is for a diagram.
  await expect(page.locator(".dmn-canvas .collab-presence .collab-avatar")).toHaveCount(1);
  await expect(page.locator(".dmn-canvas .collab-presence .collab-avatar")).toHaveAttribute("title", /Alice/);
});

test("a decision with no draft yet holds no session", async ({ page }) => {
  const session = installSessionMock(page);
  // A decision opened from its reference with no draft has nothing to key a
  // session on until the first Save.
  await page.route("**/api/v1/dmn-drafts", (route) =>
    route.request().method() === "GET" ? route.fulfill({ json: [] }) : route.continue());
  await page.goto("/index.html#/modeler/dmn/e/ref-1");
  await editorReady(page);
  await page.waitForTimeout(500);
  expect(session.joined).toBe(0);
});
