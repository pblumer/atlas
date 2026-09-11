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

function expectedServices(dividerY) {
  return {
    approval: {
      outputDecision: ["#id_route"],
      encapsulatedDecision: ["#id_elig"],
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

    const refs = (element, property) =>
      element.businessObject.get(property).map((reference) => reference.href);

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

  expect(result.importWarnings).toEqual([]);
  expect(result.initial).toEqual(expectedServices(210));
  expect(result.dividerMoved).toEqual(expectedServices(250));
  expect(result.dividerUndone).toEqual(expectedServices(210));
  expect(result.dividerRedone).toEqual(expectedServices(250));

  expect(result.sharedMoved.routingOnly.outputDecision).toEqual(["#id_route"]);
  expect(result.sharedUndone).toEqual(expectedServices(250));
  expect(result.sharedRedone.routingOnly.outputDecision).toEqual(["#id_route"]);

  expect(result.reimportWarnings).toEqual([]);
  expect(result.roundtrip).toEqual(expectedServices(250));
  expect(result.savedXML).toContain("DMNDecisionServiceDividerLine");
});
