// An approval is decided in the inbox (api/web/app.js, #1029).
//
// An approval is an ordinary user task, so it was already a row in Tasks — rendered
// like every other row, saying nothing about the product, the price or the person
// waiting, and decided by opening a second surface in another tab. The inbox now
// says what the approval decides and takes the decision itself.
//
// The API halves are covered by the Go suite. This drives the REAL app shell against
// a mocked /api/v1 to check the wiring: what the block shows, what each button
// posts, and what a rejection with no reason does *not* post.
import { test, expect } from "@playwright/test";

const listing = (items, extra = {}) => ({
  items, total: items.length, totalExact: true, truncated: false, ...extra,
});

// Two approvals of one order and one ordinary task beside them. The two approvals
// are what makes "decide the whole order together" expressible; the third row is
// what proves the block is not left standing on a task that is not an approval.
const TASKS = [
  {
    key: 101, processInstanceKey: 9001, processDefKey: 1, processId: "atlas-genehmigung-fix",
    elementId: "Genehmigen", name: "Genehmigen", formId: "genehmigung", priority: 50,
  },
  {
    key: 102, processInstanceKey: 9002, processDefKey: 1, processId: "atlas-genehmigung-fix",
    elementId: "Genehmigen", name: "Genehmigen", formId: "genehmigung", priority: 50,
  },
  {
    key: 103, processInstanceKey: 9003, processDefKey: 2, processId: "freigabe",
    elementId: "sign", name: "Unterschreiben", priority: 50,
  },
  {
    key: 104, processInstanceKey: 9004, processDefKey: 3, processId: "kunden-genehmigung",
    elementId: "Pruefen", name: "Freigabe prüfen", formId: "eigene-freigabe", priority: 50,
  },
];

const APPROVALS = [
  {
    task: TASKS[0], orderId: "ord_4711", itemId: "phone", positionId: "phone#black",
    variantId: "black", recipient: "usr_rosa", orderer: "usr_max",
    price: "CHF 1'200.–", texts: { de: "Apple iPhone 18 Pro", en: "Apple iPhone 18 Pro" },
    catalogId: "cat_mobil", catalogTexts: { de: "Mobile Geräte", en: "Mobile devices" },
  },
  {
    task: TASKS[1], orderId: "ord_4711", itemId: "huelle", positionId: "huelle",
    recipient: "usr_rosa", orderer: "usr_max",
    price: "CHF 49.–", texts: { de: "Schutzhülle", en: "Case" },
  },
  {
    // An installation's own approval model: the order names it, so Atlas recognises
    // it as the process that decides that line — and knows nothing about what its
    // form asks or what completing it means.
    task: TASKS[3], orderId: "ord_0815", itemId: "laptop", positionId: "laptop",
    recipient: "usr_rosa", orderer: "usr_max", texts: { en: "Laptop" },
  },
];

// installMock answers what the shell asks on boot, and records every write so a test
// can assert what the page sent rather than what it rendered about it.
function installMock(page, sent) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() === "POST") {
      let body = null;
      try { body = JSON.parse(req.postData() || "null"); } catch { /* not JSON */ }
      sent.push({ path, body });
      return route.fulfill({ json: { skipped: [] } });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: listing(TASKS) });
    if (path.endsWith("/api/v1/approvals")) return route.fulfill({ json: listing(APPROVALS) });
    return route.fulfill({ json: [] });
  });
}

async function bootTasks(page) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
}

const select = async (page, key) => page.locator(`.tasks-item[data-key="${key}"]`).click();

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__sent = [];
  installMock(page, page.__sent);
});

test("an approval says what it decides, in names rather than ids", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  const block = page.locator(".tasks-approval");
  await expect(block).toBeVisible();
  await expect(block).toContainText("Apple iPhone 18 Pro");
  await expect(block).toContainText("CHF 1'200.–");
  await expect(block).toContainText("ord_4711");
  await expect(block).toContainText("usr_rosa");
  // Which customer's catalogue this is. The page this replaced said it in that
  // catalogue's colours; the Console wears nobody's brand, so it says it in words —
  // an approver deciding for two customers needs to know which one they are in.
  await expect(block).toContainText("Mobile devices");
  expect(page.__errors).toEqual([]);
});

test("the list says which approval is which", async ({ page }) => {
  await bootTasks(page);

  // Every approval task in an inbox is called "Genehmigen". Without the product on
  // the row, a queue of them is a column of identical lines and each one has to be
  // opened to learn what it is.
  const first = page.locator('.tasks-item[data-key="101"]');
  await expect(first.locator(".tasks-item-appr")).toContainText("Apple iPhone 18 Pro");
  await expect(first.locator(".tasks-item-appr")).toContainText("CHF 1'200.–");
  await expect(page.locator('.tasks-item[data-key="103"] .tasks-item-appr')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("approving posts the decision the shipped model reads", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);
  await page.locator("#appr-approve").click();

  await expect.poll(() => page.__sent.length).toBeGreaterThan(0);
  const post = page.__sent[0];
  expect(post.path).toBe("/api/v1/tasks/101/complete");
  expect(post.body).toEqual({ variables: { genehmigt: true, begruendung: "" } });
  expect(page.__errors).toEqual([]);
});

test("a rejection with no reason is refused before anything is sent", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);
  await page.locator("#appr-reject").click();

  // Nothing left the page: a rejection the orderer cannot read is the one outcome
  // this screen must not produce quietly.
  await expect(page.locator("#toast")).toBeVisible();
  expect(page.__sent).toEqual([]);

  await page.locator("#appr-reason").fill("Budget für dieses Quartal ausgeschöpft");
  await page.locator("#appr-reject").click();
  await expect.poll(() => page.__sent.length).toBeGreaterThan(0);
  expect(page.__sent[0].body).toEqual({
    variables: { genehmigt: false, begruendung: "Budget für dieses Quartal ausgeschöpft" },
  });
  expect(page.__errors).toEqual([]);
});

test("the other positions of one order can be decided with it", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  // The offer exists only because a second approval of the same order is in this
  // inbox; it names how many, so nobody decides four things meaning to decide one.
  const together = page.locator("#appr-together");
  await expect(together).toBeVisible();
  await expect(page.locator("label[for=appr-together]")).toContainText("1");

  await together.check();
  await page.locator("#appr-approve").click();
  await expect.poll(() => page.__sent.length).toBeGreaterThan(0);
  const post = page.__sent[0];
  expect(post.path).toBe("/api/v1/approvals/decide");
  expect(post.body.approved).toBe(true);
  expect(post.body.taskKeys.sort()).toEqual([101, 102]);
  expect(page.__errors).toEqual([]);
});

test("an ordinary task carries no decision block", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);
  await expect(page.locator(".tasks-approval")).toBeVisible();

  await select(page, 103);
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Unterschreiben");
  await expect(page.locator(".tasks-approval")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("an installation's own approval model keeps its own form", async ({ page }) => {
  await bootTasks(page);
  await select(page, 104);

  // The block still says what is being decided — that half is true of any approval.
  const block = page.locator(".tasks-approval");
  await expect(block).toBeVisible();
  await expect(block).toContainText("Laptop");
  await expect(block).toContainText("ord_0815");

  // But not the two buttons. Approve and Reject send `genehmigt` and `begruendung`,
  // which is the shipped model's contract and not a general one: a model with its own
  // form may be asking for something else entirely, and two buttons that answered for
  // it would complete somebody's task with variables their process never reads.
  await expect(page.locator("#appr-approve")).toHaveCount(0);
  await expect(page.locator("#appr-reject")).toHaveCount(0);
  await expect(block).toContainText("form");
  expect(page.__errors).toEqual([]);
});

test("the shipped approval has exactly one way to answer", async ({ page }) => {
  await bootTasks(page);
  await select(page, 101);

  // The generic Complete button and the form's own "Genehmigen" checkbox answer the
  // same question as the two buttons above them — and answer it by accident: a task
  // completed with no variables reads as `genehmigt = null`, which is not true, which
  // is a rejection with no reason. So for the approval Atlas ships, the block is the
  // only way to answer in this screen.
  await expect(page.locator("#task-complete")).toHaveCount(0);
  await expect(page.locator("#task-form")).toHaveCount(0);

  // Ctrl+Enter is the same act by keyboard, and it says so rather than doing it.
  await page.keyboard.press("Control+Enter");
  await expect(page.locator("#toast")).toBeVisible();
  expect(page.__sent).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("an approval on another model keeps the ordinary way to complete it", async ({ page }) => {
  await bootTasks(page);
  await select(page, 104);

  // Nothing was taken away here: Atlas does not know what that model's form asks, so
  // the form and its Complete button are still how the task is answered.
  await expect(page.locator("#task-complete")).toBeVisible();
  await expect(page.locator("#task-form")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

// The link in an approval notification lands on that approval.
//
// The mail names the **order line** and not the task: a task key does not exist
// until the task activates, while the order and the product do, and they survive a
// reassignment that changes the key. So the inbox resolves the line to the row it
// holds for it — and says so when it holds none, because an approval somebody else
// decided in the meantime is the ordinary reason for that, not a broken link.
test("a notification's link opens the approval it names", async ({ page }) => {
  await page.goto("/index.html#/tasks?order=ord_4711&item=phone%23black");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });

  await expect(page.locator(".tasks-detail-head h1")).toBeVisible();
  const block = page.locator(".tasks-approval");
  await expect(block).toContainText("Apple iPhone 18 Pro");
  await expect(block).toContainText("ord_4711");
  expect(page.__errors).toEqual([]);
});

test("a link to an approval that is no longer open says so", async ({ page }) => {
  await page.goto("/index.html#/tasks?order=ord_4711&item=gone");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });

  // The inbox still opens — it is the right place to be — and the page says why the
  // approval that was linked is not in front of them.
  await expect(page.locator("#toast")).toBeVisible();
  await expect(page.locator(".tasks-approval")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// The product named alone still resolves, which is what every link sent before a
// product could be ordered twice carries.
test("a link naming the product resolves where the order carries one of it", async ({ page }) => {
  await page.goto("/index.html#/tasks?order=ord_4711&item=huelle");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".tasks-approval")).toContainText("ord_4711");
  await expect(page.locator(".tasks-detail-head h1")).toBeVisible();
  await expect(page.locator(".tasks-item.selected")).toHaveCount(1);
  await expect(page.locator(".tasks-item.selected")).toHaveAttribute("data-key", "102");
  expect(page.__errors).toEqual([]);
});
