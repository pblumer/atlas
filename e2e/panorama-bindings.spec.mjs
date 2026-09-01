import { test, expect } from "@playwright/test";

// Atlas bindings in the Panorama model viewer (ADR-0189 §4). The document stores an
// opaque id; every name on screen comes from the server, so the panel can never show
// a stale copy of one.

const modelId = "22222222222222222222222222222222";

const xml = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="model-bound">
  <name xml:lang="en">Bound landscape</name>
  <elements>
    <element identifier="app-orders" xsi:type="ApplicationComponent"><name xml:lang="en">Order Service</name></element>
    <element identifier="bp-fulfil" xsi:type="BusinessProcess"><name xml:lang="en">Fulfil order</name></element>
  </elements>
  <views><diagrams>
    <view identifier="view-app" xsi:type="Diagram"><name xml:lang="en">Application cooperation</name>
      <node identifier="n-app" elementRef="app-orders" xsi:type="Element" x="80" y="100" w="190" h="80"/>
      <node identifier="n-bp" elementRef="bp-fulfil" xsi:type="Element" x="380" y="105" w="170" h="70"/>
    </view>
  </diagrams></views>
</model>`;

// resolution is what the server says the document's bindings mean. One resolved, one
// forbidden, one missing — the three answers the panel has to tell apart.
const resolution = {
  contractVersion: 1,
  unresolved: 2,
  problems: [],
  bindings: [
    {
      elementId: "app-orders", elementType: "ApplicationComponent", key: "atlas.applicationId",
      values: [
        { value: "proj-abc", status: "resolved", name: "Billing" },
        { value: "proj-hidden", status: "forbidden" },
        { value: "proj-gone", status: "missing" },
      ],
    },
  ],
};

// observations is what the server currently sees of what those bindings name —
// one healthy, one degraded, one nothing observes.
const observations = {
  contractVersion: 1,
  observedAt: 1_700_000_000,
  summary: { ok: 1, attention: 1, critical: 0, unknown: 1 },
  unavailable: [
    { state: "unreachable", reason: "This view contacts no source outside the engine." },
    { state: "stale", reason: "Every fact here is read when the request is served." },
  ],
  problems: [],
  observations: [
    {
      elementId: "app-orders", key: "atlas.applicationId", value: "proj-abc",
      source: "deployments", state: "healthy", severity: "ok",
      reason: "3 process(es) deployed, 1 live instance(s), nothing parked.",
      detail: { processes: "3", instances: "1" },
    },
    {
      elementId: "app-orders", key: "atlas.applicationId", value: "proj-gone",
      source: "none", state: "unbound", severity: "unknown",
      reason: "No resource with this id is present here, so there is nothing to observe.",
    },
    {
      elementId: "bp-fulfil", key: "atlas.processId", value: "fulfil",
      source: "instances", state: "degraded", severity: "attention",
      reason: "2 token(s) are parked behind an unresolved incident.",
      detail: { parkedTokens: "2" },
      // The journal has seen this one turn over, so the finding carries its age.
      changedAt: 1_699_999_000, previousState: "healthy",
    },
  ],
};

// drift is what previous reads established about this model (ADR-0189 P5): a list
// of transitions, newest first, with the three limits it publishes about itself.
const driftDocument = {
  contractVersion: 1,
  since: 1_699_990_000,
  truncated: false,
  entries: [
    {
      elementId: "bp-fulfil", key: "atlas.processId", value: "fulfil",
      from: "healthy", to: "degraded", at: 1_699_999_000,
      reason: "2 token(s) are parked behind an unresolved incident.",
    },
    {
      elementId: "app-orders", key: "atlas.applicationId", value: "proj-abc",
      from: "not-ready", to: "healthy", at: 1_699_995_000,
      reason: "3 process(es) deployed, 1 live instance(s), nothing parked.",
    },
  ],
  limits: [
    { limit: "only what was looked at", reason: "Observations are computed when somebody asks for them; nothing polls." },
    { limit: "not durable", reason: "This is runtime state and a restart empties it." },
    { limit: "bounded", reason: "The most recent changes are kept and older ones are dropped." },
  ],
};

function installMock(page, { role = "owner", onPut, observing = true, drift = driftDocument } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Enterprise Architecture", myRole: role }] });
    }
    if (path === `/api/v1/panorama/models/${modelId}`) {
      return route.fulfill({ json: { id: modelId, applicationId: "app-1", name: "Bound landscape", notation: "archimate-3.2", revision: 3 } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/xml`) {
      return route.fulfill({ contentType: "application/xml", body: xml });
    }
    if (path === `/api/v1/panorama/models/${modelId}/bindings/candidates`) {
      const key = new URL(request.url()).searchParams.get("key");
      if (key === "atlas.runtimeId") return route.fulfill({ json: { key, supported: false, candidates: [] } });
      return route.fulfill({ json: { key, supported: true, candidates: [
        { id: "proj-abc", name: "Billing" }, { id: "proj-new", name: "Collections" },
      ] } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/drift`) {
      if (!drift) return route.fulfill({ status: 404, json: { error: "no such model" } });
      return route.fulfill({ json: drift });
    }
    if (path === `/api/v1/panorama/models/${modelId}/observations`) {
      if (!observing) return route.fulfill({ status: 501, json: { error: "this server observes nothing" } });
      return route.fulfill({ json: observations });
    }
    if (path === `/api/v1/panorama/models/${modelId}/bindings`) {
      if (request.method() === "PUT") {
        onPut?.(JSON.parse(request.postData() || "{}"));
        return route.fulfill({ json: { id: modelId, revision: 4 } });
      }
      return route.fulfill({ json: resolution });
    }
    return route.fulfill({ json: [] });
  });
}

test("shows resolved names and keeps unresolved bindings visible", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Atlas bindings");
  // The resolved one shows the server's name beside the id the document stores.
  await expect(panel).toContainText("Billing");
  await expect(panel).toContainText("proj-abc");
  // A broken binding is shown, not hidden: hiding it would make the model look
  // correct. Each unresolved kind says which it is, because they are fixed in
  // different places.
  await expect(panel).toContainText("outside your access");
  await expect(panel).toContainText("no longer on this server");
  // A forbidden resource's name is exactly what the scope withholds.
  await expect(panel).not.toContainText("HR Confidential");

  expect(pageErrors).toEqual([]);
});

test("only keys valid for the element type are offered", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await page.locator('.djs-element[data-element-id="n-bp"]').click();
  const panel = page.locator(".panorama-properties");
  // A business process binds a BPMN process id and nothing else; offering an
  // application id here would invite an error the server would then refuse.
  await expect(panel).toContainText("BPMN process");
  await expect(panel).not.toContainText("Process application");
  await expect(panel).toContainText("Not bound");
});

test("binds a resource through the picker and sends the expected revision", async ({ page }) => {
  const puts = [];
  installMock(page, { onPut: (body) => puts.push(body) });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await page.locator('.djs-element[data-element-id="n-bp"]').click();
  await page.getByRole("button", { name: "Bind" }).click();

  // The picker lists only what the server offered — an opaque id is never typed.
  await expect(page.locator(".panorama-pick")).toContainText("Collections");
  await page.locator('.panorama-pick input[value="proj-new"]').check();
  await page.getByRole("button", { name: "Save binding" }).click();

  await expect.poll(() => puts.length).toBe(1);
  expect(puts[0]).toMatchObject({
    expectedRevision: 3, elementId: "bp-fulfil", key: "atlas.processId", values: ["proj-new"],
  });
});

test("a viewer is not offered the edit control", async ({ page }) => {
  installMock(page, { role: "viewer" });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await page.locator('.djs-element[data-element-id="n-app"]').click();
  await expect(page.locator(".panorama-properties")).toContainText("Billing");
  await expect(page.getByRole("button", { name: "Change" })).toHaveCount(0);
});

// The C4 projection (ADR-0211 §8). What makes a projection trustworthy is that it
// says what it could not express, so the loss report is asserted as hard as the
// structure.
const c4Projection = {
  notation: "c4-projection", sourceNotation: "archimate-3.2",
  sourceModelId: modelId, sourceRevision: 3, mappingVersion: 1, readOnly: true,
  elements: [
    { id: "app-orders", type: "SoftwareSystem", name: "Order Service", sourceType: "ApplicationComponent" },
    { id: "db-1", type: "Container", name: "Order database", parent: "app-orders", sourceType: "Node" },
  ],
  relationships: [
    { id: "r-1", source: "app-orders", target: "db-1", name: "reads", sourceType: "Access" },
  ],
  dropped: [
    { id: "bp-fulfil", sourceType: "BusinessProcess", name: "Fulfil order",
      reason: "C4 has no concept for an ArchiMate BusinessProcess" },
  ],
};

test("projects into C4 and says what it could not express", async ({ page }) => {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "EA", myRole: "owner" }] });
    }
    if (path === `/api/v1/panorama/models/${modelId}`) {
      return route.fulfill({ json: { id: modelId, applicationId: "app-1", name: "Bound landscape", notation: "archimate-3.2", revision: 3 } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/xml`) {
      return route.fulfill({ contentType: "application/xml", body: xml });
    }
    if (path === `/api/v1/panorama/models/${modelId}/c4`) return route.fulfill({ json: c4Projection });
    return route.fulfill({ json: [] });
  });

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.getByRole("button", { name: "C4 projection" }).click();

  const panel = page.locator(".c4-panel");
  // Structure, with C4's nesting rather than an arrow for the composition.
  await expect(panel).toContainText("SoftwareSystem");
  await expect(panel).toContainText("Order Service");
  await expect(panel.locator(".c4-tree .c4-tree")).toContainText("Order database");

  // The loss report is the contractual half and is never a footnote.
  await expect(panel).toContainText("Not projected (1)");
  await expect(panel).toContainText("Fulfil order");
  await expect(panel).toContainText("no concept for an ArchiMate BusinessProcess");

  // It says it is a projection of a named revision, so a picture cannot circulate
  // as though it were the authored artefact.
  await expect(panel).toContainText("revision 3");
  await expect(panel).toContainText("Nothing here is authored");
});

// The observation projection in the viewer (ADR-0189 §6): what an element *is*,
// and beside it what it is currently *doing*.
test("shows what the bound resources are doing, without recolouring the diagram", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Live");
  await expect(panel).toContainText("3 process(es) deployed");
  // The class and the state both travel: the class makes a panel legible at a
  // glance, the state is what somebody acts on.
  await expect(panel.locator(".panorama-obs").first()).toContainText("OK");
  await expect(panel.locator(".panorama-obs").first()).toContainText("healthy");
  await expect(panel.locator(".panorama-obs").first()).toContainText("deployments");
  // The numbers behind the sentence, for a reader who wants them.
  await expect(panel.locator(".panorama-obs-detail").first()).toContainText("processes");

  // A bound id nothing here holds is reported, not dropped: an element that
  // vanished from the live view would look like an element with nothing wrong.
  await expect(panel.locator(".panorama-sev-unknown")).toContainText("nothing here observes it");

  // What the view cannot see is stated beside what it can.
  await expect(panel).toContainText("Not watched here");
  await expect(panel).toContainText("unreachable");

  // ArchiMate layer colours are untouched — runtime state is text and a bar on the
  // panel, never a recoloured element (ADR-0189 §6).
  const fill = await page.locator('.djs-element[data-element-id="n-app"] rect').first()
    .evaluate((el) => el.getAttribute("fill") || getComputedStyle(el).fill);
  expect(fill).not.toMatch(/rgb\(19[0-9], 5[0-9], 4[0-9]\)|#c0392b/i);
});

test("an element that binds nothing says so rather than looking healthy", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-bp"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("2 token(s) are parked");
  await expect(panel.locator(".panorama-sev-attention")).toBeVisible();
});

// A server that observes nothing refuses the route. The viewer loses its Live
// section and keeps the diagram — the model is worth opening either way.
test("a server that observes nothing still opens the model", async ({ page }) => {
  installMock(page, { observing: false });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Atlas bindings");
  await expect(panel.locator(".panorama-obs")).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});

// Runtime marks on the diagram (ADR-0189 §6, P4d). The record asks for borders,
// badges and a text legend — and, in the same breath, for ArchiMate layer colours
// to remain intact. A layer colour says whether an element is business,
// application or technology; painting health over it would destroy one meaning to
// show another.
test("marks the diagram with what each element is doing, and never repaints it", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await expect(page.locator(".panorama-canvas .djs-element").first()).toBeVisible();

  // The application is healthy on one binding and unobserved on another, so it
  // shows the worst *finding* — ok — rather than whichever answer came first.
  const application = page.locator('.djs-element[data-element-id="n-app"]');
  await expect(application).toHaveClass(/panorama-marked-ok/);
  // The business process has a parked token: attention, with a glyph, because that
  // is what somebody is scanning the diagram for.
  const process = page.locator('.djs-element[data-element-id="n-bp"]');
  await expect(process).toHaveClass(/panorama-marked-attention/);
  await expect(process.locator(".panorama-mark-glyph")).toHaveText("•");

  // The mark is a border drawn around the element and a badge beside it. Nothing
  // touched the element's own fill — this is the assertion the whole design is
  // arranged around.
  await expect(process.locator(".panorama-mark-border")).toHaveCSS("fill", "none");
  const fills = await page.evaluate(() => [...document.querySelectorAll(
    '.djs-element[data-element-id="n-bp"] rect:not(.panorama-mark-border)')]
    .map((r) => r.getAttribute("fill")).filter(Boolean));
  for (const fill of fills) {
    expect(fill).not.toMatch(/#c0392b|#b7791f|var\(--danger\)|var\(--warn\)/i);
  }

  // The finding joins the accessible name rather than replacing it: a screen
  // reader has to hear what the element is before what it is doing.
  const spoken = await process.getAttribute("aria-label");
  expect(spoken).toContain("Attention");
  expect(spoken).toContain("2 token(s) are parked");
});

// A mark nobody can decode is decoration, so the legend ships with the badges. It
// lists only the classes on this diagram, and states what an *unmarked* element
// means — the half a legend usually leaves out.
test("the diagram's marks come with a legend that says what unmarked means", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  const legend = page.locator(".panorama-live-legend");
  await expect(legend).toContainText("Attention");
  await expect(legend).toContainText("OK");
  await expect(legend).toContainText("binds nothing this server observes");
  // Critical is not on this diagram, so it is not in the legend.
  await expect(legend).not.toContainText("cannot do work");
});

// A server that observes nothing leaves the diagram unmarked and says so, rather
// than showing a model where everything looks fine.
test("a diagram with nothing observed is unmarked and says why", async ({ page }) => {
  installMock(page, { observing: false });
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await expect(page.locator(".panorama-canvas .djs-element").first()).toBeVisible();

  await expect(page.locator(".panorama-canvas-mark")).toHaveCount(0);
  await expect(page.locator(".panorama-live-legend")).toHaveCount(0);
});

// The drift journal in the panel (ADR-0189 P5). The record allows a correlation
// surface and forbids a time-series database, so what is shown is transitions —
// and, beside them, the three things the journal cannot see. Those limits are the
// contractual half: without them "nothing changed" and "nobody looked" read alike.
test("shows what changed about the selected element, with what it cannot see", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-bp"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("What changed");
  // The direction is the finding: healthy → degraded is somebody's incident.
  const entry = panel.locator(".panorama-drift-entry").first();
  await expect(entry).toContainText("healthy");
  await expect(entry).toContainText("degraded");
  await expect(entry).toContainText("2 token(s) are parked");

  // Scoped to the selection, like the panel it sits in — the model's other change
  // is counted rather than listed, so the narrowing is visible rather than silent.
  await expect(panel.locator(".panorama-drift-entry")).toHaveCount(1);
  await expect(panel).toContainText("1 other recorded change(s) in this model");
  await expect(panel).toContainText("Recorded since");

  // A history that hides what it cannot see is worse than no history.
  const limits = panel.locator(".panorama-drift-limits");
  await expect(limits).toContainText("only what was looked at");
  await expect(limits).toContainText("not durable");
  await expect(limits).toContainText("bounded");
});

// "Degraded" and "degraded since nine this morning" are different findings, and
// the second is the one somebody acts on. The age rides on the observation, so no
// second request is needed to tell a standing condition from an incident.
test("a finding says when it last changed and what it was before", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-bp"]').click();

  const since = page.locator(".panorama-properties .panorama-obs-since");
  await expect(since).toContainText("Changed");
  await expect(since).toContainText("was healthy");
});

// Nothing has been seen to change about this element. That is said outright rather
// than by an empty section, because an absent history and an uneventful one are
// different answers.
test("an element with no recorded change says so rather than showing an empty list", async ({ page }) => {
  installMock(page, { drift: { contractVersion: 1, since: 0, truncated: false, entries: [], limits: [] } });
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Nothing has been seen to change about this element");
  await expect(panel.locator(".panorama-drift-entry")).toHaveCount(0);
  // An empty journal has no moment to speak from, so it claims none.
  await expect(panel).not.toContainText("Recorded since");
});

// The journal is additive: a model whose history cannot be read is still a model
// worth opening, and the viewer must not lose the diagram over it.
test("a model whose history cannot be read still opens", async ({ page }) => {
  installMock(page, { drift: null });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Atlas bindings");
  await expect(panel).toContainText("Live");
  await expect(panel).not.toContainText("What changed");
  expect(pageErrors).toEqual([]);
});

// A body this code cannot read is treated as a history that never arrived. Showing
// "nothing has been seen to change" from it would turn a contract fault into a
// finding about the architecture — the one mistake this section exists to avoid.
test("a history in a shape this code cannot read is treated as absent", async ({ page }) => {
  installMock(page, { drift: { contractVersion: 99 } });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-bp"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Live");
  await expect(panel).not.toContainText("What changed");
  expect(pageErrors).toEqual([]);
});
