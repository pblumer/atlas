import { test, expect } from "@playwright/test";

const DECISION_SERVICE_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/"
             namespace="http://temis.example/decision-service"
             name="DecisionService"
             id="def_ds">
  <inputData id="id_age" name="Applicant Age">
    <variable name="Applicant Age" typeRef="number" />
  </inputData>
  <decision id="id_elig" name="Eligibility">
    <variable name="Eligibility" typeRef="string" />
    <informationRequirement>
      <requiredInput href="#id_age" />
    </informationRequirement>
    <literalExpression>
      <text>if Applicant Age &gt;= 18 then "ELIGIBLE" else "INELIGIBLE"</text>
    </literalExpression>
  </decision>
  <decision id="id_route" name="Routing">
    <variable name="Routing" typeRef="string" />
    <informationRequirement>
      <requiredDecision href="#id_elig" />
    </informationRequirement>
    <literalExpression>
      <text>if Eligibility = "ELIGIBLE" then "ACCEPT" else "DECLINE"</text>
    </literalExpression>
  </decision>
  <decisionService id="id_approval" name="Approval">
    <outputDecision href="#id_route" />
    <encapsulatedDecision href="#id_elig" />
    <inputData href="#id_age" />
  </decisionService>
  <decisionService id="id_routeonly" name="Routing Only">
    <outputDecision href="#id_route" />
    <inputDecision href="#id_elig" />
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_DecisionService">
      <dmndi:DMNShape id="DMNShape_Age" dmnElementRef="id_age">
        <dc:Bounds x="40" y="300" width="160" height="80" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Eligibility" dmnElementRef="id_elig">
        <dc:Bounds x="40" y="190" width="160" height="80" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Routing" dmnElementRef="id_route">
        <dc:Bounds x="40" y="80" width="160" height="80" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Approval" dmnElementRef="id_approval">
        <dc:Bounds x="300" y="60" width="320" height="320" />
        <dmndi:DMNDecisionServiceDividerLine>
          <di:waypoint x="300" y="210" />
          <di:waypoint x="620" y="210" />
        </dmndi:DMNDecisionServiceDividerLine>
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_RoutingOnly" dmnElementRef="id_routeonly">
        <dc:Bounds x="680" y="60" width="320" height="320" />
        <dmndi:DMNDecisionServiceDividerLine>
          <di:waypoint x="680" y="210" />
          <di:waypoint x="1000" y="210" />
        </dmndi:DMNDecisionServiceDividerLine>
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

function expectedServices(
  dividerY,
  approvalOutput = ["#id_route"],
  approvalEncapsulated = ["#id_elig"],
) {
  return {
    approval: {
      outputDecision: approvalOutput,
      encapsulatedDecision: approvalEncapsulated,
      inputDecision: [],
      inputData: ["#id_age"],
      dividerY,
    },
    routingOnly: {
      outputDecision: ["#id_route"],
      encapsulatedDecision: [],
      inputDecision: ["#id_elig"],
      inputData: [],
      dividerY: 210,
    },
  };
}

test("vendored dmn-js preserves DMN 1.5 Decision Services through modeling and roundtrip", async ({ page }) => {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  const result = await page.evaluate(async (xml) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:700px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({
      container: "#dmn",
      dmnVersion: "1.5",
    });

    const imported = await modeler.importXML(xml);
    let viewer = modeler.getActiveViewer();
    let elementRegistry = viewer.get("elementRegistry");
    const modeling = viewer.get("modeling");
    const commandStack = viewer.get("commandStack");

    // Decision Service references describe membership. Their serialized order is
    // not semantically meaningful, so snapshots compare a canonical ordering.
    const refs = (element, property) =>
      element.businessObject
        .get(property)
        .map((reference) => reference.href)
        .sort();

    const snapshot = () => {
      const approval = elementRegistry.get("id_approval");
      const routingOnly = elementRegistry.get("id_routeonly");
      const service = (shape) => {
        const divider = shape.businessObject.di.get("decisionServiceDividerLine");
        return {
          outputDecision: refs(shape, "outputDecision"),
          encapsulatedDecision: refs(shape, "encapsulatedDecision"),
          inputDecision: refs(shape, "inputDecision"),
          inputData: refs(shape, "inputData"),
          dividerY: divider && divider.waypoint && divider.waypoint[0].y,
        };
      };
      return {
        approval: service(approval),
        routingOnly: service(routingOnly),
      };
    };

    const initial = snapshot();
    const approval = elementRegistry.get("id_approval");
    const routing = elementRegistry.get("id_route");

    if (typeof modeling.updateDecisionServiceDivider !== "function") {
      throw new Error("vendored dmn-js has no Decision Service divider modeling API");
    }

    modeling.updateDecisionServiceDivider(approval, 250);
    const dividerMoved = snapshot();
    commandStack.undo();
    const dividerUndone = snapshot();
    commandStack.redo();
    const dividerRedone = snapshot();

    // Routing belongs to two Decision Services semantically. Moving it graphically
    // into Approval must not erase the independent Routing Only membership.
    modeling.moveShape(routing, { x: 320, y: 0 }, approval);
    const sharedMoved = snapshot();
    commandStack.undo();
    const sharedUndone = snapshot();
    commandStack.redo();
    const sharedRedone = snapshot();

    const saved = await modeler.saveXML({ format: true });
    const reimported = await modeler.importXML(saved.xml);
    viewer = modeler.getActiveViewer();
    elementRegistry = viewer.get("elementRegistry");
    const roundtrip = snapshot();

    modeler.destroy();

    return {
      importWarnings: imported.warnings.map((warning) => warning.message),
      reimportWarnings: reimported.warnings.map((warning) => warning.message),
      initial,
      dividerMoved,
      dividerUndone,
      dividerRedone,
      sharedMoved,
      sharedUndone,
      sharedRedone,
      roundtrip,
      savedXML: saved.xml,
    };
  }, DECISION_SERVICE_XML);

  const importedServices = expectedServices(210);
  const reclassifiedServices = expectedServices(
    250,
    ["#id_elig", "#id_route"],
    [],
  );

  expect(result.importWarnings).toEqual([]);
  expect(result.initial).toEqual(importedServices);

  // Eligibility is centred at y=230. Moving the divider to y=250 therefore
  // reclassifies it into the upper/output compartment. Undo must nevertheless
  // restore the exact imported semantic membership, not recompute it from geometry.
  expect(result.dividerMoved).toEqual(reclassifiedServices);
  expect(result.dividerUndone).toEqual(importedServices);
  expect(result.dividerRedone).toEqual(reclassifiedServices);

  expect(result.sharedMoved).toEqual(reclassifiedServices);
  expect(result.sharedUndone).toEqual(reclassifiedServices);
  expect(result.sharedRedone).toEqual(reclassifiedServices);

  expect(result.reimportWarnings).toEqual([]);
  expect(result.roundtrip).toEqual(reclassifiedServices);
  expect(result.savedXML).toContain("DMNDecisionServiceDividerLine");
});

// A minimal DRD to create into: one decision, nothing else, so what the test
// creates is unambiguous.
const EMPTY_DRD_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             namespace="http://temis.example/blank" name="Blank" id="def_blank">
  <decision id="id_only" name="Only">
    <variable name="Only" typeRef="string" />
    <literalExpression><text>"x"</text></literalExpression>
  </decision>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_Blank">
      <dmndi:DMNShape id="DMNShape_Only" dmnElementRef="id_only">
        <dc:Bounds x="40" y="80" width="160" height="80" />
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

// #998's second half: the palette offered a Decision Service and the canvas
// refused the drop, because the DRD create rule did not list the type — so the
// tool was a button that never worked. The fork fixed the rule and Atlas rebuilt
// the bundle; this holds the result where Atlas actually ships it.
//
// It asks the *rule*, not the modeling API. `modeling.createShape` executes the
// command directly and consults no rules, which is why the fork's own authoring
// test passed throughout the defect: the interactive path the palette uses asks
// `rules.allowed('shape.create', …)`, and that is the thing that was false.
test("vendored dmn-js lets an author create a DMN 1.5 Decision Service from the palette", async ({ page }) => {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  const result = await page.evaluate(async (xml) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:700px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({ container: "#dmn", dmnVersion: "1.5" });
    const imported = await modeler.importXML(xml);
    const viewer = modeler.getActiveViewer();
    const canvas = viewer.get("canvas");
    const rules = viewer.get("rules");
    const elementFactory = viewer.get("elementFactory");
    const modeling = viewer.get("modeling");
    const palette = viewer.get("palette");

    const entries = palette.getEntries();
    const entry = entries["create.decision-service"];

    const root = canvas.getRootElement();
    const shape = elementFactory.createShape({ type: "dmn:DecisionService" });
    // The question the palette asks before it lets go of the shape.
    const allowed = rules.allowed("shape.create", {
      position: { x: 500, y: 240 },
      shape,
      target: root,
    });

    modeling.createShape(shape, { x: 500, y: 240 }, root);
    const divider = shape.businessObject.di.get("decisionServiceDividerLine");
    const waypoints = (divider && divider.get("waypoint")) || [];

    const saved = await modeler.saveXML({ format: true });
    const reimported = await modeler.importXML(saved.xml);

    modeler.destroy();
    return {
      importWarnings: imported.warnings.map((w) => w.message),
      offered: !!entry,
      iconClass: entry && entry.className,
      allowed,
      type: shape.businessObject.$type,
      parented: shape.businessObject.$parent === root.businessObject,
      dividerWaypoints: waypoints.length,
      savedXML: saved.xml,
      reimportWarnings: reimported.warnings.map((w) => w.message),
    };
  }, EMPTY_DRD_XML);

  expect(result.importWarnings).toEqual([]);
  // The rule that made the button dead: the palette offered the tool and this
  // answered false, in every configuration, so the drop was refused.
  expect(result.offered).toBe(true);
  expect(result.allowed).toBe(true);
  // And its own icon rather than the decision's — the two shapes mean different
  // things, and a palette that draws them alike says they do not.
  expect(result.iconClass).toContain("dmn-icon-decision-service");

  expect(result.type).toBe("dmn:DecisionService");
  expect(result.parented).toBe(true);
  // A service is drawn with a divider line, and it is created with the shape
  // rather than left for the author to repair.
  expect(result.dividerWaypoints).toBe(2);

  // And it survives to the document, which is what a deploy would read.
  expect(result.savedXML).toContain("<decisionService");
  expect(result.savedXML).toContain("DMNDecisionServiceDividerLine");
  expect(result.reimportWarnings).toEqual([]);
});

// A Decision Service drawn AROUND its members — the shape Atlas's own layout
// generator emits (dmn/layout.go) and the one that used to render wrong: the box
// was painted over its contents and dragging it left them behind.
const CONTAINED_DECISION_SERVICE_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/"
             namespace="http://temis.example/contained"
             name="Contained" id="def_contained">
  <decision id="id_out" name="Out" />
  <decision id="id_enc" name="Enc">
    <informationRequirement id="ir_enc">
      <requiredDecision href="#id_boundary" />
    </informationRequirement>
  </decision>
  <decision id="id_boundary" name="Boundary" />
  <decisionService id="id_svc" name="Contained Service">
    <outputDecision href="#id_out" />
    <encapsulatedDecision href="#id_enc" />
    <inputDecision href="#id_boundary" />
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_Contained">
      <dmndi:DMNShape id="DMNShape_Svc" dmnElementRef="id_svc">
        <dc:Bounds x="100" y="80" width="300" height="240" />
        <dmndi:DMNDecisionServiceDividerLine>
          <di:waypoint x="100" y="200" />
          <di:waypoint x="400" y="200" />
        </dmndi:DMNDecisionServiceDividerLine>
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Out" dmnElementRef="id_out">
        <dc:Bounds x="130" y="100" width="180" height="80" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Enc" dmnElementRef="id_enc">
        <dc:Bounds x="130" y="220" width="180" height="80" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Boundary" dmnElementRef="id_boundary">
        <dc:Bounds x="500" y="220" width="180" height="80" />
      </dmndi:DMNShape>
      <dmndi:DMNEdge id="DMNEdge_ir_enc" dmnElementRef="ir_enc">
        <di:waypoint x="500" y="260" />
        <di:waypoint x="310" y="260" />
      </dmndi:DMNEdge>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

test("the shipped modeler draws a Decision Service as a container", async ({ page }) => {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  const result = await page.evaluate(async (xml) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:700px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({
      container: "#dmn",
      dmnVersion: "1.5",
    });

    const imported = await modeler.importXML(xml);
    const viewer = modeler.getActiveViewer();
    const elementRegistry = viewer.get("elementRegistry");
    const modeling = viewer.get("modeling");
    const canvas = viewer.get("canvas");

    const service = elementRegistry.get("id_svc");
    const out = elementRegistry.get("id_out");
    const boundary = elementRegistry.get("id_boundary");

    const parents = {
      out: out.parent && out.parent.id,
      enc: elementRegistry.get("id_enc").parent.id,
      boundary: boundary.parent && boundary.parent.id,
      root: canvas.getRootElement().id,
    };
    const children = service.children.map((child) => child.id).sort();

    // SVG paints in document order: the box has to come before what it holds.
    const painted = Array.prototype.map.call(
      canvas.getContainer().querySelectorAll(".djs-element"),
      (gfx) => gfx.getAttribute("data-element-id"),
    );

    modeling.moveElements([ service ], { x: 60, y: 40 });

    const moved = {
      out: { x: out.x, y: out.y },
      boundary: { x: boundary.x, y: boundary.y },
    };

    const refs = (element, property) =>
      element.businessObject
        .get(property)
        .map((reference) => reference.href)
        .sort();

    const membership = {
      outputDecision: refs(service, "outputDecision"),
      encapsulatedDecision: refs(service, "encapsulatedDecision"),
      inputDecision: refs(service, "inputDecision"),
    };

    const saved = await modeler.saveXML({ format: true });

    modeler.destroy();

    return {
      importWarnings: imported.warnings.map((warning) => warning.message),
      parents,
      children,
      paintedService: painted.indexOf("id_svc"),
      paintedOut: painted.indexOf("id_out"),
      moved,
      membership,
      savedXML: saved.xml,
    };
  }, CONTAINED_DECISION_SERVICE_XML);

  expect(result.importWarnings).toEqual([]);

  // The Decisions the service declares AND draws around are its children.
  expect(result.parents.out).toBe("id_svc");
  expect(result.parents.enc).toBe("id_svc");
  expect(result.children).toEqual([ "id_enc", "id_out" ]);

  // The input decision is the caller-supplied boundary, so it stays outside even
  // though the service names it.
  expect(result.parents.boundary).toBe(result.parents.root);

  // The box is painted beneath what it holds, not over it.
  expect(result.paintedService).toBeGreaterThanOrEqual(0);
  expect(result.paintedOut).toBeGreaterThan(result.paintedService);

  // Moving the service carries its members and leaves the boundary put.
  expect(result.moved.out).toEqual({ x: 190, y: 140 });
  expect(result.moved.boundary).toEqual({ x: 500, y: 220 });

  // And the move does not rewrite what the document declared. inputDecision is
  // derived from what the members require from outside, so the requirement in the
  // fixture is what keeps the boundary named.
  expect(result.membership).toEqual({
    outputDecision: [ "#id_out" ],
    encapsulatedDecision: [ "#id_enc" ],
    inputDecision: [ "#id_boundary" ],
  });
  expect(result.savedXML).toContain("DMNDecisionServiceDividerLine");
});

// A Decision Service that declares itself collapsed. DMN draws one as the same
// rounded rectangle with its name over a plus marker and no divider, because its
// decisions are folded away (DMN 1.5 Table 5-2).
const COLLAPSED_DECISION_SERVICE_XML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             namespace="http://temis.example/collapsed"
             name="Collapsed" id="def_collapsed">
  <decision id="id_out" name="Out" />
  <decisionService id="id_svc" name="Collapsed Service">
    <outputDecision href="#id_out" />
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_Collapsed">
      <dmndi:DMNShape id="DMNShape_Svc" dmnElementRef="id_svc" isCollapsed="true">
        <dc:Bounds x="100" y="80" width="300" height="240" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_Out" dmnElementRef="id_out">
        <dc:Bounds x="130" y="100" width="180" height="80" />
      </dmndi:DMNShape>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

test("the shipped modeler draws a Decision Service the way DMN draws one", async ({ page }) => {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  const result = await page.evaluate(async ([expanded, collapsed]) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:700px"></div>';

    // The drawn shape, not the invisible hit area diagram-js puts beside it.
    const read = async (xml) => {
      const modeler = new window.AtlasDmn.DmnJS({
        container: "#dmn",
        dmnVersion: "1.5",
      });
      const imported = await modeler.importXML(xml);
      const viewer = modeler.getActiveViewer();
      const elementRegistry = viewer.get("elementRegistry");
      const visual = (id) =>
        elementRegistry.getGraphics(id).querySelector(".djs-visual");

      const service = elementRegistry.get("id_svc");
      const serviceVisual = visual("id_svc");
      const rect = serviceVisual.querySelector("rect");
      const label = serviceVisual.querySelector("text");
      const box = label.getBBox();

      const out = {
        warnings: imported.warnings.map((w) => w.message),
        serviceRadius: Number(rect.getAttribute("rx")),
        decisionRadius: Number(
          visual("id_out").querySelector("rect").getAttribute("rx") || 0,
        ),
        labelTop: box.y / service.height,
        labelCentre: (box.x + box.width / 2) / service.width,
        rects: serviceVisual.querySelectorAll("rect").length,
        dividers: serviceVisual.querySelectorAll("polyline").length,
      };

      modeler.destroy();

      return out;
    };

    return { expanded: await read(expanded), collapsed: await read(collapsed) };
  }, [CONTAINED_DECISION_SERVICE_XML, COLLAPSED_DECISION_SERVICE_XML]);

  expect(result.expanded.warnings).toEqual([]);
  expect(result.collapsed.warnings).toEqual([]);

  // A decision service is a ROUNDED rectangle and a decision a plain one; the
  // corner is what tells them apart at a glance (DMN 1.5 Figure 5-10).
  expect(result.expanded.serviceRadius).toBeGreaterThan(0);
  expect(result.expanded.decisionRadius).toBe(0);
  expect(result.collapsed.serviceRadius).toBeGreaterThan(0);

  // The name starts in the top left, where DMN 1.5 Figures 6-7, 6-8 and 6-9 draw it.
  // A default rather than the notation: §6.2.5 requires the name inside the shape
  // and nothing more, the figures do not agree with each other (6-6 centres it), and
  // a DMNLabel with bounds moves it — which dmn-decision-service-overlay.spec.mjs
  // holds.
  expect(result.expanded.labelTop).toBeLessThan(0.25);
  expect(result.expanded.labelCentre).toBeLessThan(0.35);

  // Collapsed: the name is centred over a plus marker, and there is no
  // compartment to divide.
  expect(result.collapsed.labelCentre).toBeCloseTo(0.5, 1);
  expect(result.collapsed.rects).toBe(2);
  expect(result.collapsed.dividers).toBe(0);
});
