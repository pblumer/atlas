// End-to-end coverage for the application a Modeler session is filed under
// (api/web/app.js), which is what gives a data object's Type field its vocabulary.
//
// The case behind it: an information model belongs to an application (ADR-0230), so
// the panel can only offer classes, draw the class a Type points at, or say that one
// is unmodelled if it knows which application the diagram belongs to. A draft carries
// that. A *deployed* version carries it too — the deploy records it — but the route
// that opens one, #/modeler/d/{key}, does not name it, and it was never looked up. So
// opening a deployed process gave a Type field with no classes behind it: no picker,
// no class, and not even the warning, since "nothing is modelled" and "the vocabulary
// never loaded" are different answers and only the first is safe to state.
//
// These drive the REAL app (index.html → app.js) against a mocked /api/v1, because
// what is under test is the routing, not the panel.
import { test, expect } from "@playwright/test";

const XML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" id="D" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="kunde-erfassen" name="Kunde erfassen" isExecutable="true">
    <bpmn:dataObject id="DO_kunde" name="Kunde" itemSubjectRef="Customer"/>
    <bpmn:dataObjectReference id="Ref_kunde" name="Kunde" dataObjectRef="DO_kunde"/>
    <bpmn:startEvent id="Start_1" name="Start"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="Di"><bpmndi:BPMNPlane id="Pl" bpmnElement="kunde-erfassen">
    <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"><dc:Bounds x="160" y="120" width="36" height="36"/></bpmndi:BPMNShape>
    <bpmndi:BPMNShape id="Ref_kunde_di" bpmnElement="Ref_kunde"><dc:Bounds x="272" y="250" width="36" height="50"/></bpmndi:BPMNShape>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`;

// installMock answers the console's surface and records every path asked for, so a
// test can prove the vocabulary was fetched for the right application.
function installMock(page, { processes }) {
  const asked = [];
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname + url.search;
    asked.push(path);
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (/\/processes\/\d+\/xml$/.test(url.pathname)) {
      return route.fulfill({ body: XML, contentType: "application/xml" });
    }
    if (url.pathname.endsWith("/api/v1/processes")) return route.fulfill({ json: processes });
    if (url.pathname.endsWith("/api/v1/applications")) {
      return route.fulfill({ json: [{ id: "app-1", name: "Data Object Demo" }] });
    }
    if (url.pathname.endsWith("/api/v1/infomodel/models")) {
      return route.fulfill({ json: [{ id: "m1", name: "Data Object Demo" }] });
    }
    if (url.pathname.endsWith("/api/v1/infomodel/models/m1")) {
      return route.fulfill({ json: {
        id: "m1", name: "Data Object Demo", revision: 1, associations: [], stores: [],
        classes: [{ id: "c1", name: "Customer", stereotype: "businessObject", identity: ["kdnr"],
          attributes: [{ name: "kdnr", type: "string", multiplicity: "1" }] }],
      } });
    }
    return route.fulfill({ json: [] });
  });
  return asked;
}

async function openDeployment(page, asked) {
  await page.goto("/index.html#/modeler/d/7");
  await page.waitForFunction(
    () => document.querySelector(".crumbs") !== null, null, { timeout: 20000 });
  return asked;
}

test("a deployed process brings its application, so the Type field has a vocabulary", async ({ page }) => {
  const asked = installMock(page, {
    processes: [{ key: 7, processId: "kunde-erfassen", name: "Kunde erfassen", version: 7, projectId: "app-1" }],
  });
  await openDeployment(page, asked);

  // The vocabulary is fetched for the application the deploy recorded. Without this
  // the Type field degrades to a plain text box and says nothing about why.
  await expect.poll(() => asked.some((p) => p.includes("/infomodel/models?applicationId=app-1")),
    { timeout: 10000 }).toBe(true);
  // And the breadcrumb names it, which is how a reader can see which application a
  // diagram is filed under at all.
  await expect(page.locator(".crumbs")).toContainText("Data Object Demo");
});

test("a deployment filed under no application still opens, and asks for no vocabulary", async ({ page }) => {
  // Not every deployed process belongs to an application; that is not an error and
  // must not become a failed request or a broken editor.
  const asked = installMock(page, {
    processes: [{ key: 7, processId: "kunde-erfassen", name: "Kunde erfassen", version: 7 }],
  });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await openDeployment(page, asked);

  await expect(page.locator(".crumbs")).toContainText("Kunde erfassen");
  expect(asked.some((p) => p.includes("/infomodel/models?applicationId="))).toBe(false);
  expect(errors).toEqual([]);
});
