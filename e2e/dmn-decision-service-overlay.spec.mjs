// A Decision Service is an overlay, through the bundle Atlas actually ships.
//
// The behaviour is held by specs inside the fork. What this holds is the other half:
// that the bundle committed under api/web/vendor/dmn carries it. A vendored bundle
// records its origin in a text file — a commit and a checksum — and nothing else
// checks that the bytes came from where the file says. Building against the wrong
// commit produces the same record and a bundle that quietly lacks the feature.
//
// All four cases below are things DMN 1.5 §6.2.5 says and diagram-js does not: "the
// border SHALL enclose all the encapsulated decisions" makes the box a container, so
// diagram-js carries its children and deletes them with it, and the paragraph under
// Figure 6-9 says the opposite — "decision services are defined as overlays and
// therefore do not encapsulate the decisions within them".
import { test, expect } from "@playwright/test";

// One service over two decisions, an input decision outside it, and a divider — the
// shape every assertion below needs.
const MODEL = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/"
             xmlns:dmndi="https://www.omg.org/spec/DMN/20230324/DMNDI/"
             xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/"
             xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/"
             id="Definitions_svc" name="Service" namespace="http://atlas/dmn/svc">
  <decision id="Decision_Output" name="Output Decision">
    <informationRequirement id="IR_Output">
      <requiredDecision href="#Decision_Encapsulated" />
    </informationRequirement>
  </decision>
  <decision id="Decision_Encapsulated" name="Encapsulated Decision" />
  <decision id="Decision_Outside" name="Outside Decision" />
  <decisionService id="Service_Approval" name="Approval Service">
    <outputDecision href="#Decision_Output" />
    <encapsulatedDecision href="#Decision_Encapsulated" />
  </decisionService>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_svc">
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
      <dmndi:DMNShape id="Shape_Outside" dmnElementRef="Decision_Outside">
        <dc:Bounds x="520" y="220" width="120" height="50" />
      </dmndi:DMNShape>
      <dmndi:DMNEdge id="Edge_IR_Output" dmnElementRef="IR_Output">
        <di:waypoint x="180" y="220" />
        <di:waypoint x="180" y="150" />
      </dmndi:DMNEdge>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;

// run opens the model in the shipped bundle and hands `body` the live viewer, so each
// test says what it does to the service rather than sharing one scripted sequence.
async function run(page, body, extra) {
  await page.goto("/harness.html");
  await page.addScriptTag({ url: "/vendor/dmn/dmn-modeler.js" });

  return page.evaluate(async ({ xml, source, extra }) => {
    document.body.innerHTML =
      '<div id="dmn" style="width:1200px;height:700px"></div>';

    const modeler = new window.AtlasDmn.DmnJS({
      container: "#dmn",
      dmnVersion: "1.5",
    });
    await modeler.importXML(xml);

    const viewer = modeler.getActiveViewer();

    // Read before doing, so a bundle built from a commit that predates any of this
    // fails saying so rather than with a bare TypeError about `undefined`.
    const shipped = {
      labelCommand: typeof viewer.get("modeling")
        .updateDecisionServiceLabelBounds === "function",
    };

    // eslint-disable-next-line no-new-func
    const result = await new Function(
      "viewer", "modeler", "extra",
      "return (" + source + ")(viewer, modeler, extra);")(viewer, modeler, extra);

    return { shipped, ...result };
  }, { xml: MODEL, source: body.toString(), extra: extra || null });
}

test("a service folded and unfolded is still a box that holds its decisions",
  async ({ page }) => {

    const state = await run(page, async (viewer) => {
      const registry = viewer.get("elementRegistry");
      const modeling = viewer.get("modeling");
      const service = registry.get("Service_Approval");

      modeling.collapseDecisionService(service, true);
      modeling.collapseDecisionService(service, false);

      const parent = registry.get("Decision_Output").parent;

      modeling.moveShape(service, { x: 200, y: 0 });

      return {
        parent: parent && parent.id,
        service: service.x,
        output: registry.get("Decision_Output").x,
        encapsulated: registry.get("Decision_Encapsulated").x,
        publishes: service.businessObject.get("outputDecision").map((r) => r.href),
        encapsulates: service.businessObject.get("encapsulatedDecision")
          .map((r) => r.href),
      };
    });

    // Unfolding used to hand the members to the root, which left a container with no
    // children: the next drag moved the box and the decisions stayed where they were.
    expect(state.parent).toBe("Service_Approval");
    expect(state.service).toBe(300);
    expect(state.output).toBe(320);
    expect(state.encapsulated).toBe(320);

    // and which compartment each one is in is what it was
    expect(state.publishes).toEqual([ "#Decision_Output" ]);
    expect(state.encapsulates).toEqual([ "#Decision_Encapsulated" ]);
  });


test("a folded service takes its decisions with it when it is dragged",
  async ({ page }) => {

    const state = await run(page, async (viewer) => {
      const registry = viewer.get("elementRegistry");
      const modeling = viewer.get("modeling");
      const service = registry.get("Service_Approval");

      modeling.collapseDecisionService(service, true);
      modeling.moveShape(service, { x: 300, y: 200 });
      modeling.collapseDecisionService(service, false);

      const divider = service.businessObject.di
        .get("decisionServiceDividerLine").get("waypoint")[0];

      return {
        service: { x: service.x, y: service.y, w: service.width, h: service.height },
        dividerY: divider.y,
        output: { x: registry.get("Decision_Output").x,
          y: registry.get("Decision_Output").y },
      };
    });

    // A folded service holds nothing on the canvas, so nothing travelled with it and
    // unfolding put the box straight back where it was folded — the drag was undone.
    expect(state.service).toEqual({ x: 400, y: 280, w: 300, h: 240 });
    expect(state.dividerY).toBe(400);
    expect(state.output).toEqual({ x: 420, y: 300 });
  });


// Every way of moving a shape, not just the drag. A decision service is a container
// in diagram-js terms, and the three defects above are what happens when something
// reads that containment differently from the rest. The keyboard move and the align
// and distribute actions all go through modeling.moveElements — the same path the
// drag takes — which is why they work; this is here so that stays true, and so that
// a future change to any of those paths is caught here rather than by a reader whose
// box arrived empty.
//
// Each case carries its own negative control: the decision drawn *outside* the box
// must not move. Without it "the members travelled" would also pass if everything on
// the canvas had travelled, or if the numbers were simply what they always were.
[
  {
    what: "moved with the keyboard",
    // eight presses, so the distance is unmistakable against a one-pixel step
    act: (viewer) => {
      viewer.get("selection").select(
        viewer.get("elementRegistry").get("Service_Approval"));

      for (let i = 0; i < 8; i++) {
        viewer.get("editorActions").trigger("moveSelection", {
          direction: "right", accelerated: false,
        });
      }
    },
    delta: 8,
  },
  {
    what: "aligned with another element",
    // right, so it is the service that moves: the box ends at 400 and
    // Decision_Outside at 640, so aligning their right edges carries it 240 across
    act: (viewer) => {
      const registry = viewer.get("elementRegistry");

      viewer.get("selection").select([
        registry.get("Service_Approval"), registry.get("Decision_Outside"),
      ]);
      viewer.get("editorActions").trigger("alignElements", { type: "right" });
    },
    delta: 240,
  },
].forEach(({ what, act, delta }) => {

  test("a service carries its decisions when it is " + what, async ({ page }) => {

    const state = await run(page, async (viewer, modeler, { act }) => {
      const registry = viewer.get("elementRegistry");
      const at = () => ({
        service: registry.get("Service_Approval").x,
        output: registry.get("Decision_Output").x,
        encapsulated: registry.get("Decision_Encapsulated").x,
        outside: registry.get("Decision_Outside").x,
      });

      const before = at();

      // eslint-disable-next-line no-new-func
      new Function("viewer", "return (" + act + ")(viewer);")(viewer);

      return { before, after: at() };
    }, { act: act.toString() });

    // What the action did, so a case that silently stopped moving the box is not
    // read as a case that carried its decisions perfectly.
    expect(state.after.service).toBe(state.before.service + delta);

    // and what this file is here for: whatever the box travelled, its decisions
    // travelled with it — stated as the same distance rather than as a coordinate,
    // because the distance is the property and the coordinate is arithmetic.
    const travelled = state.after.service - state.before.service;

    expect(state.after.output - state.before.output).toBe(travelled);
    expect(state.after.encapsulated - state.before.encapsulated).toBe(travelled);

    // the negative control: what the box was not drawn around stays where it is
    expect(state.after.outside).toBe(state.before.outside);
  });

});


test("a service drawn after a requirement goes behind it, not over it",
  async ({ page }) => {

    const state = await run(page, async (viewer) => {
      const registry = viewer.get("elementRegistry");

      // querySelectorAll answers in document order, which is paint order: later is
      // on top. A shape drawn inside another's group counts as over it, which is
      // what a member of a decision service is.
      const paintsOver = (a, b) => {
        const drawn = Array.from(
          viewer.get("canvas")._svg.querySelectorAll(".djs-element"));

        return drawn.indexOf(registry.getGraphics(a)) >
          drawn.indexOf(registry.getGraphics(b));
      };

      // the author draws the box afterwards, which is the order a diagram is
      // usually built in
      const service = viewer.get("elementFactory").createShape({
        type: "dmn:DecisionService" });

      viewer.get("modeling").createShape(
        service, { x: 700, y: 400, width: 300, height: 240 },
        viewer.get("canvas").getRootElement());

      viewer.get("modeling").moveShape(
        registry.get("Decision_Outside"), { x: 0, y: 0 }, service);

      return {
        overRequirement: paintsOver(service.id, "IR_Output"),
        overDecision: paintsOver(service.id, "Decision_Output"),
        underItsMember: paintsOver("Decision_Outside", service.id),
      };
    });

    // The box is a background: §6.2.5 encloses the decisions it names and Figure 6-9
    // has requirements crossing its border, which only reads as a diagram if the
    // border is behind them. Drawn last, it used to be painted last, and the arrow
    // simply disappeared inside it.
    expect(state.overRequirement).toBe(false);
    expect(state.overDecision).toBe(false);

    // and what it does hold is still drawn in it
    expect(state.underItsMember).toBe(true);
  });


test("deleting a service leaves the decisions it was drawn around", async ({ page }) => {

  const state = await run(page, async (viewer, modeler) => {
    const registry = viewer.get("elementRegistry");

    viewer.get("modeling").removeShape(registry.get("Service_Approval"));

    const saved = await modeler.saveXML({ format: true });

    return {
      drg: viewer.get("canvas").getRootElement().businessObject
        .get("drgElement").map((e) => e.id),
      onCanvas: {
        output: !!registry.get("Decision_Output"),
        encapsulated: !!registry.get("Decision_Encapsulated"),
        requirement: !!registry.get("IR_Output"),
      },
      saved: saved.xml,
    };
  });

  // diagram-js deletes a shape's children with it. A decision service does not own
  // its decisions — it is drawn around them — so this took two decisions and the
  // requirement between them out of a four-decision model.
  expect(state.drg).toEqual([
    "Decision_Output", "Decision_Encapsulated", "Decision_Outside",
  ]);
  expect(state.onCanvas).toEqual({
    output: true, encapsulated: true, requirement: true,
  });
  expect(state.saved).toContain('id="IR_Output"');
  expect(state.saved).not.toContain('id="Service_Approval"');
});


test("a requirement drawn from a service says what it requires", async ({ page }) => {

  const state = await run(page, async (viewer, modeler) => {
    const registry = viewer.get("elementRegistry");

    viewer.get("modeling").connect(
      registry.get("Service_Approval"),
      registry.get("Decision_Outside"),
      { type: "dmn:KnowledgeRequirement" },
    );

    const saved = await modeler.saveXML({ format: true });

    return {
      requires: registry.get("Decision_Outside").businessObject
        .get("knowledgeRequirement")
        .map((r) => r.get("requiredKnowledge") && r.get("requiredKnowledge").href),
      saved: saved.xml,
    };
  });

  // The arrow drew and the document said nothing: the reference was written under a
  // property nobody declared, so the requirement required nothing.
  expect(state.requires).toEqual([ "#Service_Approval" ]);
  expect(state.saved).toContain('<requiredKnowledge href="#Service_Approval" />');
});


test("the name can be moved, and comes back as a DMNLabel", async ({ page }) => {

  const state = await run(page, async (viewer, modeler) => {
    const registry = viewer.get("elementRegistry");
    const service = registry.get("Service_Approval");

    // Past the divider and past the right edge on purpose: §6.2.5 puts the Name
    // inside the shape, in the part that holds the output decisions.
    viewer.get("modeling").updateDecisionServiceLabelBounds(service, {
      x: 5000, y: 5000, width: 90, height: 18,
    });

    const saved = await modeler.saveXML({ format: true });

    const bounds = service.businessObject.di.get("label").get("bounds");

    return {
      bounds: { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height },
      saved: saved.xml,
    };
  });

  expect(state.shipped.labelCommand,
    "the shipped bundle can move a decision service's name — if not, it was built "
    + "from a commit before it, whatever ATLAS-VENDORED.txt says").toBe(true);

  // clamped into the box, and above the divider at y=200
  expect(state.bounds).toEqual({ x: 300, y: 182, width: 90, height: 18 });

  // and written where DMN keeps it, so it survives the save
  expect(state.saved).toContain("<dmndi:DMNLabel>");
  expect(state.saved).toMatch(
    /<dc:Bounds height="18" width="90" x="300" y="182" \/>/);
});


test("a service dragged over a requirement still lets it through",
  async ({ page }) => {

    const state = await run(page, async (viewer) => {
      const registry = viewer.get("elementRegistry");
      const modeling = viewer.get("modeling");

      const paintsOver = (a, b) => {
        const drawn = Array.from(
          viewer.get("canvas")._svg.querySelectorAll(".djs-element"));

        return drawn.indexOf(registry.getGraphics(a)) >
          drawn.indexOf(registry.getGraphics(b));
      };

      // A requirement that crosses the border: its source is outside the box and
      // its target is a member, so it belongs to the root and the box can cover it.
      // The requirement wholly inside the service cannot be covered — it is drawn
      // in the box's own group — which is why this one has to be drawn first.
      modeling.connect(
        registry.get("Decision_Outside"),
        registry.get("Decision_Encapsulated"),
        { type: "dmn:InformationRequirement" },
      );

      const service = registry.get("Service_Approval");

      const before = paintsOver(service.id, "Decision_Outside");

      modeling.moveShape(service, { x: 20, y: 0 });

      return {
        before,
        after: paintsOver(service.id, "Decision_Outside"),
        crossing: service.parent.children
          .filter((child) => child.waypoints).map((child) => child.id),
      };
    });

    // diagram-js moves a shape by taking it out of its parent's children and putting
    // it back, and putting it back with no index asked for means at the end. A
    // stored diagram therefore drew correctly right up to the moment the author
    // nudged the box, at which point the arrow crossing its border disappeared
    // underneath it.
    expect(state.before).toBe(false);
    expect(state.after).toBe(false);

    // the negative control: there is a crossing connection at root level for the box
    // to have covered
    expect(state.crossing.length).toBeGreaterThan(0);
  });


test("the name stays in the box when the box is moved and resized",
  async ({ page }) => {

    const state = await run(page, async (viewer) => {
      const registry = viewer.get("elementRegistry");
      const modeling = viewer.get("modeling");
      const service = registry.get("Service_Approval");

      const label = () => {
        const bounds = service.businessObject.di.get("label").get("bounds");

        return {
          x: bounds.x, y: bounds.y,
          width: bounds.width, height: bounds.height,
        };
      };

      // where the renderer draws the name: the group is already at the shape's
      // origin, so this translation is what puts the text outside the box
      const drawnAt = () => {
        const drawn = registry.getGraphics(service)
          .querySelector(".djs-label");

        return drawn && drawn.getAttribute("transform");
      };

      // the box is at 100,80 and 300x240, so 60 across and 15 down from its corner
      modeling.updateDecisionServiceLabelBounds(service, {
        x: 160, y: 95, width: 80, height: 20,
      });

      modeling.moveShape(service, { x: 150, y: 60 });

      const moved = { label: label(), drawnAt: drawnAt() };

      modeling.resizeShape(service, {
        x: 250, y: 140, width: 120, height: 100,
      });

      const box = {
        x: service.x, y: service.y,
        width: service.width, height: service.height,
      };

      // and what diagram-js places the context pad from: the element's rendered
      // bounding box, which a name drawn outside the box swells to cover both
      const rendered = registry.getGraphics(service).getBoundingClientRect();

      return {
        moved,
        resized: { label: label(), box },
        rendered: {
          width: Math.round(rendered.width),
          height: Math.round(rendered.height),
        },
      };
    });

    expect(state.shipped.labelCommand,
      "the shipped bundle can move a decision service's name — if not, it was built "
      + "from a commit before it, whatever ATLAS-VENDORED.txt says").toBe(true);

    // DMNDI records a DMNLabel's bounds in diagram coordinates, and nothing kept
    // them in step with the shape: the name stayed put while the box travelled, and
    // was drawn further outside it with every drag.
    expect(state.moved.label).toEqual({ x: 310, y: 155, width: 80, height: 20 });
    expect(state.moved.drawnAt).toBe("translate(60,15)");

    // and a box that shrinks past the name pulls it back in rather than leaving it
    // outside
    const { label, box } = state.resized;

    expect(label.x).toBeGreaterThanOrEqual(box.x);
    expect(label.x + label.width).toBeLessThanOrEqual(box.x + box.width);
    expect(label.y).toBeGreaterThanOrEqual(box.y);
    expect(label.y + label.height).toBeLessThanOrEqual(box.y + box.height);

    // which is also what puts the context pad back beside the service: diagram-js
    // reads the element's drawn box, not its bounds, so a stray name moved the pad
    // with it
    expect(state.rendered).toEqual({ width: box.width, height: box.height });
  });
