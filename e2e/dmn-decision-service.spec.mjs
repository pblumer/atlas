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
