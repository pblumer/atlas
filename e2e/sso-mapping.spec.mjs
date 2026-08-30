// What a provider's claims decide here, from the browser
// (ADR-draft-federated-authentication, measure M12 step two; the Single sign-on
// card in app.js, viewConsoleOrg).
//
// The card is the whole editable surface of the mapping, and everything that makes
// it safe is a sentence somebody has to read before they switch it on. Only a real
// DOM proves the wiring: that the card is absent when no provider is configured,
// that it reads back what the server holds, and that Save sends exactly the rules
// on screen. Drives the REAL app shell against a STATEFUL mock of the two
// endpoints, like theme.spec.mjs.
import { test, expect } from "@playwright/test";

const GROUPS = [
  { id: "grp_model", name: "Modelling", members: [] },
  { id: "grp_ops", name: "Operations", members: [] },
];

// installMock answers as an admin-enforcing instance and models
// /api/v1/settings/oidc-mapping in memory. `providers` decides whether a provider
// is configured at all. The returned handle's .mapping is what the server holds,
// so a test can assert what Save actually persisted.
function installMock(page, { providers = [], putStatus = 0, putError = "" } = {}) {
  const state = { mapping: { enabled: false, claim: "", rules: [] } };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "u1", username: "root", roles: ["admin"] } } });
    }
    if (path.endsWith("/auth/providers")) return route.fulfill({ json: providers });
    if (path.endsWith("/settings/oidc-mapping")) {
      if (req.method() === "GET") return route.fulfill({ json: state.mapping });
      if (putStatus) return route.fulfill({ status: putStatus, json: { error: putError } });
      state.mapping = req.postDataJSON();
      return route.fulfill({ json: state.mapping });
    }
    if (path.endsWith("/groups")) return route.fulfill({ json: GROUPS });
    if (path.endsWith("/users")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
  return state;
}

async function gotoOrg(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#view")).toContainText("Single sign-on");
}

test("with no provider the card says so and offers nothing to edit", async ({ page }) => {
  installMock(page, { providers: [] });
  await gotoOrg(page);

  await expect(page.locator("#view")).toContainText("No identity provider is configured");
  await expect(page.locator("#sso-card")).toHaveCount(0);
});

test("the card reads back what the server holds and names the cost of switching it on", async ({ page }) => {
  const server = installMock(page, { providers: [{ id: "oidc", name: "Contoso ID", start: "/auth/oidc/start" }] });
  server.mapping = {
    enabled: true, claim: "realm_access.roles",
    rules: [{ value: "atlas-modeller", roles: ["modeler"], groups: ["grp_model"] }],
  };
  await gotoOrg(page);

  await expect(page.locator("#view")).toContainText("Contoso ID");
  await expect(page.locator("#view")).toContainText("whoever administers the");
  await expect(page.locator("#sso-enabled")).toBeChecked();
  await expect(page.locator("#sso-claim")).toHaveValue("realm_access.roles");

  const row = page.locator("tr.sso-rule");
  await expect(row).toHaveCount(1);
  await expect(row.locator("[data-sso='value']")).toHaveValue("atlas-modeller");
  await expect(row.locator("[data-sso='role'][value='modeler']")).toBeChecked();
  await expect(row.locator("[data-sso='role'][value='admin']")).not.toBeChecked();
  await expect(row.locator("[data-sso='group'][value='grp_model']")).toBeChecked();
  await expect(row.locator("[data-sso='group'][value='grp_ops']")).not.toBeChecked();
  // `user` is a floor, not a grant, so it is not offered as one.
  await expect(row.locator("[data-sso='role'][value='user']")).toHaveCount(0);
});

test("a rule added on screen is what Save sends", async ({ page }) => {
  const server = installMock(page, { providers: [{ id: "oidc", name: "Contoso ID", start: "/auth/oidc/start" }] });
  await gotoOrg(page);

  await expect(page.locator("tr.sso-rule")).toHaveCount(0);
  await page.locator("#sso-enabled").check();
  await page.locator("#sso-claim").fill("groups");
  await page.locator("#sso-add").click();

  const row = page.locator("tr.sso-rule");
  await row.locator("[data-sso='value']").fill("atlas-betrieb");
  await row.locator("[data-sso='role'][value='operator']").check();
  await row.locator("[data-sso='group'][value='grp_ops']").check();
  await page.locator("#sso-save").click();

  await expect(page.locator("#view")).toContainText("Single sign-on");
  await expect.poll(() => server.mapping).toEqual({
    enabled: true,
    claim: "groups",
    rules: [{ value: "atlas-betrieb", roles: ["operator"], groups: ["grp_ops"] }],
  });
});

test("a removed rule is gone from what Save sends", async ({ page }) => {
  const server = installMock(page, { providers: [{ id: "oidc", name: "Contoso ID", start: "/auth/oidc/start" }] });
  server.mapping = {
    enabled: true, claim: "groups",
    rules: [
      { value: "atlas-modeller", roles: ["modeler"], groups: [] },
      { value: "atlas-betrieb", roles: ["operator"], groups: [] },
    ],
  };
  await gotoOrg(page);

  await page.locator("tr.sso-rule").first().locator("button[data-sso-act='remove']").click();
  await expect(page.locator("tr.sso-rule")).toHaveCount(1);
  await page.locator("#sso-save").click();

  await expect.poll(() => server.mapping.rules).toEqual([
    { value: "atlas-betrieb", roles: ["operator"], groups: [] },
  ]);
});

test("a mapping the server refuses shows the server's reason", async ({ page }) => {
  installMock(page, {
    providers: [{ id: "oidc", name: "Contoso ID", start: "/auth/oidc/start" }],
    putStatus: 400,
    putError: `rule 1 ("x") names group "grp_gone", which does not exist`,
  });
  await gotoOrg(page);

  await page.locator("#sso-enabled").check();
  await page.locator("#sso-claim").fill("groups");
  await page.locator("#sso-add").click();
  await page.locator("tr.sso-rule [data-sso='value']").fill("x");
  await page.locator("#sso-save").click();

  await expect(page.locator("#toast")).toContainText("grp_gone");
});
