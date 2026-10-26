// End-to-end coverage for the constants this installation holds (api/web/app.js,
// ADR-0453, ADR-0456): an application's Constants tab and the
// Server constants section of Admin → Engine list what the deployed processes read —
// application.NAME, server.NAME — with what this installation answers for each, and edit
// the constants held here. A constant's name is written in capitals, applied while it is
// typed.
//
// DEV, INT and PROD each hold their own; the pages are where an operator sees that a
// constant a process needs is missing here before a start is refused for it.
//
// Drives the REAL app (index.html → app.js) against a mocked /api/v1.
import { test, expect } from "@playwright/test";

const APP = { id: "app-1", name: "UCC-Sammelanschlüsse" };

const VIEW = (constants) => ({
  applicationId: APP.id,
  constants,
  required: [
    { name: "LANGUAGE", type: "string", processes: ["ucc-create"], fallback: true,
      state: constants.some((v) => v.name === "LANGUAGE") ? "set" : "fallback" },
    { name: "TENANT_ID", type: "string", processes: ["ucc-create", "ucc-delete"], fallback: false,
      state: constants.some((v) => v.name === "TENANT_ID") ? "set" : "missing" },
  ],
  updatedAt: 1760000000,
  updatedBy: "usr_1",
  updatedByName: "Anja Admin",
});

function installMock(page) {
  const puts = { app: [], server: [] };
  let held = [{ name: "DOMAIN_SUFFIX", type: "string", value: "contoso.ch" }];
  let server = [];
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications" || path === "/api/v1/projects") return route.fulfill({ json: [APP] });
    if (path === `/api/v1/applications/${APP.id}/constants`) {
      if (req.method() === "PUT") {
        const body = JSON.parse(req.postData() || "{}");
        puts.app.push(body);
        held = body.constants;
      }
      return route.fulfill({ json: VIEW(held) });
    }
    if (path === "/api/v1/server/constants") {
      if (req.method() === "PUT") {
        const body = JSON.parse(req.postData() || "{}");
        puts.server.push(body);
        server = body.constants;
      }
      return route.fulfill({ json: { constants: server, required: [
        { name: "ENTRA_TENANT_ID", type: "string", processes: ["entra-create-account"], fallback: false,
          state: server.some((v) => v.name === "ENTRA_TENANT_ID") ? "set" : "missing" },
      ] } });
    }
    if (path === `/api/v1/applications/${APP.id}/deployments`) {
      return route.fulfill({ json: { id: APP.id, name: APP.name, processes: 0, definitions: [] } });
    }
    if (path === "/api/v1/node") return route.fulfill({ json: { id: "n1", partition: 0, partitions: 1, features: [] } });
    if (path === "/api/v1/info") return route.fulfill({ json: { product: "Atlas", version: "0.1.0" } });
    if (path === "/api/v1/stats") return route.fulfill({ json: { activeProcessInstances: 0, activeElementInstances: 0 } });
    return route.fulfill({ json: [] });
  });
  return puts;
}

async function openAppConstants(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto(`/index.html#/modeler/p/${APP.id}`);
  await page.locator('#pd-tabs button[data-pane="constants"]').click({ timeout: 15000 });
  await expect(page.locator("#ac-table")).toBeVisible();
}

test("the Constants tab says what the processes read and what this installation holds", async ({ page }) => {
  installMock(page);
  await openAppConstants(page);

  // The page reads its words from the catalogue (ADR-0267): German by default.
  await expect(page.locator('#pd-tabs button[data-pane="constants"]')).toHaveText("Konstanten");
  await expect(page.locator("#ac-required tbody tr")).toHaveCount(2);
  await expect(page.locator('#ac-required tr[data-req="TENANT_ID"]')).toContainText("fehlt");
  await expect(page.locator('#ac-required tr[data-req="TENANT_ID"]')).toContainText("application.TENANT_ID");
  await expect(page.locator('#ac-required tr[data-req="TENANT_ID"]')).toContainText("ucc-create, ucc-delete");
  await expect(page.locator('#ac-required tr[data-req="LANGUAGE"]')).toContainText("Fallback");

  // The constant held here, then a row for each required name it does not hold yet — so
  // setting one is typing a value, not retyping its name.
  const names = await page.locator("#ac-rows .hc-name").evaluateAll((els) => els.map((e) => e.value));
  expect(names).toEqual(["DOMAIN_SUFFIX", "LANGUAGE", "TENANT_ID"]);
  // Who changed them is named, not identified.
  await expect(page.locator("#pd-constants")).toContainText("von Anja Admin");
  expect(page.__errors).toEqual([]);
});

test("Save sends the constants that have a value, named in capitals", async ({ page }) => {
  const puts = installMock(page);
  await openAppConstants(page);

  await page.locator("#ac-rows tr").nth(2).locator(".hc-value").fill("tenant-int");
  await page.locator("#ac-add").click();
  const added = page.locator("#ac-rows tr").last();
  await added.locator(".hc-name").pressSequentially("max agents");
  await expect(added.locator(".hc-name")).toHaveValue("MAX_AGENTS");
  await added.locator(".hc-type").selectOption("number");
  await added.locator(".hc-value").fill("20");
  await page.locator("#ac-save").click();

  await expect.poll(() => puts.app.length).toBe(1);
  // LANGUAGE has no value typed, so it is left out rather than saved as an empty string.
  expect(puts.app[0].constants).toEqual([
    { name: "DOMAIN_SUFFIX", type: "string", value: "contoso.ch" },
    { name: "TENANT_ID", type: "string", value: "tenant-int" },
    { name: "MAX_AGENTS", type: "number", value: "20" },
  ]);
  await expect(page.locator('#ac-required tr[data-req="TENANT_ID"]')).toContainText("gesetzt");
  expect(page.__errors).toEqual([]);
});

test("Admin → Engine holds the server constants every application reads", async ({ page }) => {
  const puts = installMock(page);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/index.html#/console/engine");

  const card = page.locator("#server-constants-card");
  await expect(card.locator("h2")).toHaveText("Server-Konstanten");
  await expect(card.locator('#sc-required tr[data-req="ENTRA_TENANT_ID"]')).toContainText("fehlt");
  await expect(card.locator('#sc-required tr[data-req="ENTRA_TENANT_ID"]')).toContainText("server.ENTRA_TENANT_ID");

  await card.locator("#sc-rows tr").first().locator(".hc-value").fill("contoso-prod");
  await card.locator("#sc-save").click();
  await expect.poll(() => puts.server.length).toBe(1);
  expect(puts.server[0].constants).toEqual([{ name: "ENTRA_TENANT_ID", type: "string", value: "contoso-prod" }]);
  await expect(card.locator('#sc-required tr[data-req="ENTRA_TENANT_ID"]')).toContainText("gesetzt");
  expect(errors).toEqual([]);
});
