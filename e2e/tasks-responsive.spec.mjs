import { test, expect } from "@playwright/test";

// The Console's inbox, Start and Access review on a narrow screen
// (ADR-0417). Below 860px the inbox
// shows the list or the task, never both, and nothing sits past the screen's right
// edge except inside a box that scrolls on its own (the view names in the top bar,
// the folder chips). A wide screen keeps its three panes; the other tasks specs
// measure that at the default 900px, and the last test here at 1280px.

const PHONE = { width: 390, height: 844 };

const listing = (items, extra = {}) => ({
  items, total: items.length, totalExact: true, truncated: false, ...extra,
});

const TASKS = [
  {
    key: 101, processInstanceKey: 9001, processDefKey: 1, processId: "atlas-genehmigung-fix",
    elementId: "Genehmigen", name: "Genehmigen", formId: "genehmigung", priority: 50,
  },
  {
    key: 103, processInstanceKey: 9003, processDefKey: 2, processId: "freigabe",
    elementId: "sign", name: "Unterschreiben", priority: 50,
  },
];

const APPROVALS = [
  {
    task: TASKS[0], orderId: "ord_4711", itemId: "phone", positionId: "phone#black",
    variantId: "black", recipient: "usr_rosa", orderer: "usr_max",
    recipientName: "Rosa Meier", ordererName: "Max Muster",
    price: "CHF 1'200.–", texts: { de: "Apple iPhone 18 Pro", en: "Apple iPhone 18 Pro" },
  },
];

const PROCESSES = [
  { key: 7, processId: "onboarding", name: "Onboarding", version: 1, startFormId: "onb" },
];

const CAMPAIGN = { id: "rc_1", name: "Q3 review" };
const REPORT = {
  counts: { rows: 2, kept: 0, revoked: 0, undecided: 2 },
  rows: [
    { id: "r1", principal: "usr_rosa.meier@musterag.ch", itemId: "2027-benutzeraccount-intern",
      origin: "order", orderId: "ord_4711", since: 1.7e18 },
    { id: "r2", principal: "usr_max", itemId: "phone", variantId: "black",
      origin: "order", since: 1.7e18 },
  ],
};

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() !== "GET") return route.fulfill({ json: { skipped: [] } });
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: listing(TASKS) });
    if (path.endsWith("/api/v1/approvals")) return route.fulfill({ json: listing(APPROVALS) });
    if (path.endsWith("/api/v1/processes")) return route.fulfill({ json: PROCESSES });
    if (path.endsWith("/api/v1/recertification")) return route.fulfill({ json: [CAMPAIGN] });
    if (path.endsWith("/api/v1/recertification/rc_1")) return route.fulfill({ json: REPORT });
    return route.fulfill({ json: [] });
  });
}

// overflow names the elements that reach past the screen's right edge, leaving out
// those inside a box that scrolls sideways on its own — which is where they belong.
async function overflow(page) {
  return page.evaluate(() => {
    const vw = document.documentElement.clientWidth;
    const scrolls = (e) => {
      for (let p = e.parentElement; p; p = p.parentElement) {
        const ox = getComputedStyle(p).overflowX;
        if ((ox === "auto" || ox === "scroll" || ox === "hidden") && p.getBoundingClientRect().right <= vw + 1) return true;
      }
      return false;
    };
    const out = [];
    for (const e of document.querySelectorAll("body *")) {
      const b = e.getBoundingClientRect();
      if (b.width > 0 && b.height > 0 && b.right > vw + 1 && !scrolls(e)) {
        out.push(`${e.tagName.toLowerCase()}.${String(e.className || "").split(" ")[0]}`);
      }
    }
    return { page: document.documentElement.scrollWidth - vw, wide: [...new Set(out)] };
  });
}

test("a phone shows the inbox's list, then the task, then the list again", async ({ page }) => {
  installMock(page);
  await page.setViewportSize(PHONE);
  await page.goto("/index.html#/tasks");
  const item = page.locator('.tasks-item[data-key="101"]');
  await expect(item).toBeVisible({ timeout: 15000 });

  // The list: the folders above it, the task pane out of the way.
  await expect(page.locator(".tasks-folders")).toBeVisible();
  await expect(page.locator("#task-detail")).toBeHidden();
  expect(await overflow(page)).toEqual({ page: 0, wide: [] });

  // The task: the list gives its place up, and the way back is the first thing on it.
  await item.click();
  await expect(page.locator(".tasks")).toHaveClass(/has-selection/);
  await expect(page.locator(".tasks-list-pane")).toBeHidden();
  await expect(page.locator("#task-back")).toBeVisible();
  await expect(page.locator(".tasks-approval")).toBeVisible();
  expect(await overflow(page)).toEqual({ page: 0, wide: [] });

  // A field's label stands above its value, not in a column beside it.
  const field = page.locator(".tasks-approval .tasks-field").first();
  const [label, value] = await Promise.all([
    field.locator(":scope > *").nth(0).boundingBox(),
    field.locator(":scope > *").nth(1).boundingBox(),
  ]);
  expect(value.y).toBeGreaterThanOrEqual(label.y + label.height - 1);

  await page.locator("#task-back").click();
  await expect(page.locator(".tasks")).not.toHaveClass(/has-selection/);
  await expect(item).toBeVisible();
  await expect(page.locator("#task-detail")).toBeHidden();
});

test("a phone shows Start's list, then the process, then the list again", async ({ page }) => {
  installMock(page);
  await page.setViewportSize(PHONE);
  await page.goto("/index.html#/tasks/start");
  const item = page.locator("#start-list .tasks-item").first();
  await expect(item).toBeVisible({ timeout: 15000 });
  await expect(page.locator("#start-detail")).toBeHidden();

  await item.click();
  await expect(page.locator("#start-grid")).toHaveClass(/has-selection/);
  await expect(page.locator("#start-list")).toBeHidden();
  await expect(page.locator("#start-go")).toBeVisible();
  expect(await overflow(page)).toEqual({ page: 0, wide: [] });

  await page.locator("#start-back").click();
  await expect(item).toBeVisible();
  await expect(page.locator("#start-detail")).toBeHidden();
});

test("a phone shows an access review row as a card with its answers under it", async ({ page }) => {
  installMock(page);
  await page.setViewportSize(PHONE);
  await page.goto("/index.html#/tasks/recertification");
  const row = page.locator("#rct-rows tr").first();
  await expect(row.locator('button[data-act="keep"]')).toBeVisible({ timeout: 15000 });

  await expect(page.locator(".rct-table thead")).toBeHidden();
  await expect(row.locator('td[data-label="Person"]')).toContainText("usr_rosa.meier@musterag.ch");
  const keep = await row.locator('button[data-act="keep"]').boundingBox();
  expect(keep.height).toBeGreaterThanOrEqual(40);
  expect(await overflow(page)).toEqual({ page: 0, wide: [] });
});

test("a wide screen keeps the inbox's three panes and no back button", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/index.html#/tasks");
  const item = page.locator('.tasks-item[data-key="101"]');
  await expect(item).toBeVisible({ timeout: 15000 });
  await item.click();

  await expect(page.locator(".tasks-folders")).toBeVisible();
  await expect(page.locator(".tasks-list-pane")).toBeVisible();
  await expect(page.locator(".tasks-approval")).toBeVisible();
  await expect(page.locator("#task-back")).toBeHidden();
  const [list, detail] = await Promise.all([
    page.locator(".tasks-list-pane").boundingBox(),
    page.locator("#task-detail").boundingBox(),
  ]);
  expect(detail.x).toBeGreaterThanOrEqual(list.x + list.width - 1);
  await expect(page.locator(".rct-table")).toHaveCount(0);
});
