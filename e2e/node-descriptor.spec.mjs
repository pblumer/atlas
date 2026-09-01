import { test, expect } from "@playwright/test";

// The node descriptor in the Console (ADR-0189 §6). What matters on screen is that
// the id is readable in full — it is a value somebody copies into a model binding —
// and that naming the node is offered only to whoever the server would let do it.

const node = {
  id: "9f2c1e4a7b6d8f0e3a5c1b9d7e2f4a60",
  name: "Zurich primary",
  environment: "production",
  labels: { region: "ch-zh", tier: "1" },
  product: "Atlas",
  version: "0.1.0",
  partition: 0,
  partitions: 1,
  features: ["observations.stats", "panorama.mesh"],
};

function installMock(page, { admin = true, onPut } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({
        json: { authEnabled: true, user: { username: "root", roles: admin ? ["admin"] : ["operator"] } },
      });
    }
    if (path === "/api/v1/node" && request.method() === "PUT") {
      const body = JSON.parse(request.postData() || "{}");
      onPut?.(body);
      return route.fulfill({ json: { ...node, ...body } });
    }
    if (path === "/api/v1/node") return route.fulfill({ json: node });
    if (path === "/api/v1/info") return route.fulfill({ json: { product: "Atlas", version: "0.1.0" } });
    if (path === "/api/v1/stats") {
      return route.fulfill({ json: { activeProcessInstances: 0, activeElementInstances: 0 } });
    }
    return route.fulfill({ json: [] });
  });
}

test("shows the runtime identity in full, beside the build", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/console/engine");

  const card = page.locator("#node-card");
  await expect(card).toBeVisible();
  // The whole id, not a prefix: it is copied into a model binding, and a truncated
  // identifier is one that gets pasted wrong.
  await expect(card).toContainText(node.id);
  await expect(card).toContainText("Zurich primary");
  await expect(card).toContainText("production");
  await expect(card).toContainText("region=ch-zh");
  // Features are shown verbatim — an operator comparing two servers is looking for
  // the difference, which a summary would hide.
  await expect(card).toContainText("panorama.mesh");
});

test("an administrator names the node without changing its id", async ({ page }) => {
  const puts = [];
  installMock(page, { onPut: (body) => puts.push(body) });
  await page.goto("/index.html#/console/engine");

  await page.locator("#node-name").fill("Geneva standby");
  await page.locator("#node-env").fill("staging");
  await page.locator("#node-form button[type=submit]").click();

  await expect(page.locator("#node-card")).toContainText("Geneva standby");
  expect(puts).toEqual([{ name: "Geneva standby", environment: "staging" }]);
  // The id is not in the request at all: a stable id a form could change is not
  // stable, and every correlation made against the old one would point at nothing.
  expect(puts[0]).not.toHaveProperty("id");
  await expect(page.locator("#node-card")).toContainText(node.id);
});

// Naming the node is admin-only on the server. The Console does not offer a form
// whose every submission would come back 403.
test("a non-administrator sees the identity but is not offered the form", async ({ page }) => {
  installMock(page, { admin: false });
  await page.goto("/index.html#/console/engine");

  await expect(page.locator("#node-card")).toContainText(node.id);
  await expect(page.locator("#node-form")).toHaveCount(0);
});
