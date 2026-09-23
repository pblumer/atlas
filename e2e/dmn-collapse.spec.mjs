// Folding a Decision Service away, through the bundle Atlas actually ships.
//
// The behaviour itself is held by specs inside the fork. What this holds is the other
// half: that the bundle committed under api/web/vendor/dmn carries it. A vendored
// bundle records its origin in a text file — a commit and a checksum — and nothing
// else checks that the bytes came from where the file says. Building against the
// wrong commit produces the same record and a bundle that quietly lacks the feature,
// and the first sign of it is somebody in the Modeler finding no button.
//
// So this exercises the fold end to end and, in the same breath, reads the provenance:
// a bundle built from anything before the fold went in fails at the first assertion.
import { test, expect } from "@playwright/test";

// One service over two decisions, with an input decision outside it — the shape that
// tells apart "the members go" from "everything named goes".
const MODEL = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/"
             id="Definitions_fold" name="Fold" namespace="http://atlas/dmn/fold">
  <decision id="Decision_Output" name="Output Decision">
    <informationRequirement id="IR_Output">
      <requiredDecision href="#Decision_Encapsulated" />
    </informationRequirement>
  </decision>
  <decision id="Decision_Encapsulated" name="Encapsulated Decision" />
  <decision id="Decision_Boundary" name="Boundary Decision" />
  <decisionService id="Service_Approval" name="Approval Service">
    <outputDecision href="#Decision_Output" />
    <encapsulatedDecision href="#Decision_Encapsulated" />
    <inputDecision href="#Decision_Boundary" />
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_fold">
      <dmndi:DMNShape id="Shape_Service" dmnElementRef="Service_Approval">
        <dc:Bounds x="100" y="80" width="300" height="240" />
        <dmndi:DMNDecisionServiceDividerLine>
          <di:waypoint x="100" y="200" />
          <di:waypoint x="400" y="200" />
        </dmndi:DMNDecisionServiceDividerLine>
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Output" dmnElementRef="Decision_Output">
        <dc:Bounds x="120" y="100" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Encapsulated" dmnElementRef="Decision_Encapsulated">
        <dc:Bounds x="120" y="220" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Boundary" dmnElementRef="Decision_Boundary">
        <dc:Bounds x="500" y="220" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNEdge id="Edge_IR_Output" dmnElementRef="IR_Output">
        <di:waypoint x="180" y="220" />
        <di:waypoint x="180" y="150" />
      </dmndi:DMNEdge>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

// fold opens the model in the shipped bundle, folds the service, and reports what the
// canvas, the DRG and the saved document say afterwards.
async function fold(page, { andUnfold = false } = {}) {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  return page.evaluate(async ({ xml, andUnfold }) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:700px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({ container: "#dmn", dmnVersion: "1.5" });
    await modeler.importXML(xml);

    const viewer = modeler.getActiveViewer();
    const registry = viewer.get("elementRegistry");
    const service = registry.get("Service_Approval");

    const pad = () => Object.keys(viewer.get("contextPad").getEntries(service));
    const modeling = viewer.get("modeling");

    // Read before doing: a bundle built from a commit without the fold has neither
    // the command nor the entry, and the tests below would otherwise fail with a bare
    // TypeError that says nothing about where the bytes came from.
    const shipped = {
      command: typeof modeling.collapseDecisionService === "function",
      padBefore: pad(),
    };
    if (!shipped.command) {
      return { shipped, missing: true };
    }

    const padBefore = shipped.padBefore;

    modeling.collapseDecisionService(service, true);

    const onCanvas = (id) => !!registry.get(id);
    const state = {
      shipped,
      padBefore,
      padAfter: pad(),
      onCanvas: {
        output: onCanvas("Decision_Output"),
        encapsulated: onCanvas("Decision_Encapsulated"),
        boundary: onCanvas("Decision_Boundary"),
        edge: onCanvas("Edge_IR_Output"),
      },
      drg: viewer.get("canvas").getRootElement().businessObject
        .get("drgElement").map((e) => e.id).sort(),
      publishes: service.businessObject.get("outputDecision")
        .map((r) => r.href),
    };

    if (andUnfold) {
      modeling.collapseDecisionService(service, false);
      state.afterUnfold = {
        output: onCanvas("Decision_Output"),
        encapsulated: onCanvas("Decision_Encapsulated"),
        edge: onCanvas("Decision_Output") ? !!registry.get("IR_Output") : false,
      };
    }

    const saved = await modeler.saveXML({ format: true });
    state.saved = saved.xml;

    return state;
  }, { xml: MODEL, andUnfold });
}

test("the shipped bundle folds a decision service, and the model survives it", async ({ page }) => {
  const state = await fold(page);

  // Provenance first: a bundle built before the fold went in carries neither, and
  // every assertion below would be explained away as a test problem.
  expect(state.shipped.command,
    "the shipped bundle has collapseDecisionService — if not, it was built from a "
    + "commit before the fold, whatever ATLAS-VENDORED.txt says").toBe(true);
  expect(state.shipped.padBefore, "the shipped bundle offers the fold on the context pad")
    .toContain("decision-service.collapse");

  // The members leave the picture; the input decision does not — it is the boundary
  // the caller supplies, which DMN draws outside the service.
  expect(state.onCanvas.output).toBe(false);
  expect(state.onCanvas.encapsulated).toBe(false);
  expect(state.onCanvas.edge).toBe(false);
  expect(state.onCanvas.boundary).toBe(true);

  // Nothing left the model. This is the assertion that a fold built out of
  // removeElements fails: that takes the decisions out of drgElement as well.
  expect(state.drg).toEqual([
    "Decision_Boundary", "Decision_Encapsulated", "Decision_Output", "Service_Approval",
  ]);

  // And the service still publishes what it published.
  expect(state.publishes).toEqual([ "#Decision_Output" ]);
});

test("what it saves is a folded diagram, not a smaller model", async ({ page }) => {
  const { saved } = await fold(page);

  // The fold is stated where DMN states it.
  expect(saved).toMatch(/dmnElementRef="Service_Approval"[^>]*isCollapsed="true"/);

  // The members' shapes are gone from the diagram — that is what folded means — while
  // their decisions and the requirement between them stay in the model.
  expect(saved).not.toContain('dmnElementRef="Decision_Output"');
  expect(saved).not.toContain('dmnElementRef="Decision_Encapsulated"');
  expect(saved).toContain('dmnElementRef="Decision_Boundary"');
  expect(saved).toContain('id="Decision_Output"');
  expect(saved).toContain('id="Decision_Encapsulated"');
  expect(saved).toContain('id="IR_Output"');
});

test("unfolding in the same session puts the decisions back", async ({ page }) => {
  const state = await fold(page, { andUnfold: true });

  expect(state.afterUnfold.output).toBe(true);
  expect(state.afterUnfold.encapsulated).toBe(true);
  expect(state.afterUnfold.edge).toBe(true);
  expect(state.saved).not.toContain('isCollapsed="true"');
  expect(state.saved).toContain('dmnElementRef="Decision_Output"');
});


// The shape the first real model had, and the one the model above does not: a
// requirement that crosses the service boundary. Input data feeding a decision
// inside the service, and a decision outside feeding one inside it. Both are
// requirements of the service — DMN derives inputData and inputDecision from
// exactly these crossings (§10.4) — so a fold has to keep them.
const CROSSING_MODEL = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/"
             id="Definitions_cross" name="Cross" namespace="http://atlas/dmn/cross">
  <inputData id="InputData_Amount" name="amount" />
  <decision id="Decision_Afford" name="Affordability">
    <informationRequirement id="IR_Amount">
      <requiredInput href="#InputData_Amount" />
    </informationRequirement>
  </decision>
  <decision id="Decision_Verdict" name="Verdict">
    <informationRequirement id="IR_Afford">
      <requiredDecision href="#Decision_Afford" />
    </informationRequirement>
    <informationRequirement id="IR_Score">
      <requiredDecision href="#Decision_Score" />
    </informationRequirement>
  </decision>
  <decision id="Decision_Score" name="Score" />
  <decisionService id="Service_Credit" name="Credit Service">
    <outputDecision href="#Decision_Verdict" />
    <encapsulatedDecision href="#Decision_Afford" />
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_cross">
      <dmndi:DMNShape id="Shape_Service_Credit" dmnElementRef="Service_Credit">
        <dc:Bounds x="100" y="80" width="360" height="300" />
        <dmndi:DMNDecisionServiceDividerLine>
          <di:waypoint x="100" y="240" />
          <di:waypoint x="460" y="240" />
        </dmndi:DMNDecisionServiceDividerLine>
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Verdict" dmnElementRef="Decision_Verdict">
        <dc:Bounds x="140" y="110" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Afford" dmnElementRef="Decision_Afford">
        <dc:Bounds x="140" y="270" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Score" dmnElementRef="Decision_Score">
        <dc:Bounds x="560" y="110" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="Shape_Amount" dmnElementRef="InputData_Amount">
        <dc:Bounds x="140" y="440" width="125" height="45" />
      </dmndi:DMNShape>
      <dmndi:DMNEdge id="Edge_IR_Amount" dmnElementRef="IR_Amount">
        <di:waypoint x="202" y="440" />
        <di:waypoint x="202" y="320" />
      </dmndi:DMNEdge>
      <dmndi:DMNEdge id="Edge_IR_Afford" dmnElementRef="IR_Afford">
        <di:waypoint x="200" y="270" />
        <di:waypoint x="200" y="160" />
      </dmndi:DMNEdge>
      <dmndi:DMNEdge id="Edge_IR_Score" dmnElementRef="IR_Score">
        <di:waypoint x="560" y="135" />
        <di:waypoint x="260" y="135" />
      </dmndi:DMNEdge>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

async function foldCrossing(page) {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  return page.evaluate(async (xml) => {
    document.body.innerHTML = '<div id="dmn" style="width:1200px;height:900px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({ container: "#dmn", dmnVersion: "1.5" });
    await modeler.importXML(xml);

    const viewer = modeler.getActiveViewer();
    const registry = viewer.get("elementRegistry");
    const modeling = viewer.get("modeling");
    const service = registry.get("Service_Credit");

    const endOf = (id, end) => {
      const connection = registry.get(id);

      return connection && connection[end] ? connection[end].id : null;
    };

    const geometry = () => {
      const { x, y, width, height } = service;
      const divider = service.businessObject.di
        .get("decisionServiceDividerLine");

      return {
        x, y, width, height,
        dividerY: divider && divider.waypoint && divider.waypoint.length
          ? divider.waypoint[0].y
          : null,
      };
    };

    const before = geometry();

    modeling.collapseDecisionService(service, true);

    const folded = {
      amountTarget: endOf("IR_Amount", "target"),
      scoreTarget: endOf("IR_Score", "target"),
      amountSource: endOf("IR_Amount", "source"),
      scoreSource: endOf("IR_Score", "source"),
      internal: !!registry.get("IR_Afford"),
      geometry: geometry(),
    };

    const saved = (await modeler.saveXML({ format: true })).xml;

    modeling.collapseDecisionService(service, false);

    const unfolded = {
      amountTarget: endOf("IR_Amount", "target"),
      scoreTarget: endOf("IR_Score", "target"),
      internal: !!registry.get("IR_Afford"),
      geometry: geometry(),
    };

    return { before, folded, saved, unfolded };
  }, CROSSING_MODEL);
}

test("a folded service keeps what it is given, drawn against the box", async ({ page }) => {
  const { folded, saved } = await foldCrossing(page);

  // The ends inside the service move to the box: it is the only thing left to draw
  // them against, and it is what the service is — dropping them left the input data
  // floating unattached and the box looking like it took nothing and gave nothing.
  expect(folded.amountTarget).toBe("Service_Credit");
  expect(folded.scoreTarget).toBe("Service_Credit");

  // The ends that were always outside do not move.
  expect(folded.amountSource).toBe("InputData_Amount");
  expect(folded.scoreSource).toBe("Decision_Score");

  // A requirement drawn wholly inside has nothing left to draw between, so it goes.
  expect(folded.internal).toBe(false);

  // The saved diagram says the same: the crossing edges are still drawn, the
  // internal one is not, and the requirement itself is untouched either way.
  expect(saved).toContain('dmnElementRef="IR_Amount"');
  expect(saved).toContain('dmnElementRef="IR_Score"');
  expect(saved).not.toContain('dmnElementRef="IR_Afford"');
  expect(saved).toContain('id="IR_Afford"');
});

test("unfolding puts the box back, divider and all", async ({ page }) => {
  const { before, folded, unfolded } = await foldCrossing(page);

  // Folded, the box is the collapsed size.
  expect(folded.geometry.width).toBe(180);
  expect(folded.geometry.height).toBe(100);

  // Unfolded, it is the box the author drew — neither its bounds nor its divider can
  // be recomputed on the way back, because the fold clamped the divider into 180x100.
  expect(unfolded.geometry).toEqual(before);

  // And the edges are docked back to the decisions that own their requirements.
  expect(unfolded.amountTarget).toBe("Decision_Afford");
  expect(unfolded.scoreTarget).toBe("Decision_Verdict");
  expect(unfolded.internal).toBe(true);
});
