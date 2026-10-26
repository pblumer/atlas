// End-to-end coverage for what a Worker Type's extension carries in the Modeler
// Implement panel (api/web/editor.js). A field the chosen operation hides is one the
// panel neither shows nor saves again, so a value left on the element there could only
// be removed in the XML source — and Teams Phone, Mail, Jira and Discord refuse to
// compile a task carrying one ("does not use accountType; remove it"). Driven through
// the real vendored bpmn-js, asserting against the exported XML the deploy sends.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-fields-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// select shows one task in the panel with every group expanded, so each field the
// operation offers is on screen.
async function select(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-all[data-all='expand']").click();
}

// taskXML returns the exported XML of one service task.
async function taskXML(page, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:serviceTask id="${id}"[\\s\\S]*?</bpmn:serviceTask>`).exec(xml)[0];
}

test("choosing Teams Phone writes no account type for the Resolve users it starts on", async ({ page }) => {
  await select(page, "Activity_plain");
  await page.locator(".stkind-row[data-kind='teamsphone']").click();

  await expect(page.locator("#f-st-operation")).toHaveValue("resolve-users");
  await expect(page.locator("#f-st-accountType")).toHaveCount(0);
  const task = await taskXML(page, "Activity_plain");
  expect(task).toContain('operation="resolve-users"');
  expect(task).not.toContain("accountType");
  expect(page.__errors).toEqual([]);
});

test("switching the operation drops the fields the new one does not use", async ({ page }) => {
  await select(page, "Activity_account");
  await page.locator("#f-st-operation").selectOption("resolve-users");

  await expect(page.locator("#f-st-users")).toBeVisible();
  const task = await taskXML(page, "Activity_account");
  expect(task).toContain('operation="resolve-users"');
  expect(task).not.toContain("accountType");
  expect(task).not.toContain("userPrincipalName");
  expect(task).not.toContain("displayName");
  // What every operation uses stays.
  expect(task).toContain('connector="ucc_teams"');
  expect(task).toContain('resultVariable="konto"');
  expect(page.__errors).toEqual([]);
});

test("an operation's select is written as the panel shows it", async ({ page }) => {
  await select(page, "Activity_resolve");
  await page.locator("#f-st-operation").selectOption("ensure-resource-account");

  // The select has no empty choice, so the panel shows the first one; the model has
  // to say the same, or the deploy refuses a value the author sees as chosen.
  await expect(page.locator("#f-st-accountType")).toHaveValue("call-queue");
  const task = await taskXML(page, "Activity_resolve");
  expect(task).toContain('accountType="call-queue"');
  expect(task).not.toContain("users=");
  expect(page.__errors).toEqual([]);
});

test("the next edit of a task clears a value an earlier Modeler left hidden", async ({ page }) => {
  await select(page, "Activity_stale");
  await page.locator("#f-st-resultVariable").fill("agentIds");
  await page.locator("#f-st-resultVariable").blur();

  const task = await taskXML(page, "Activity_stale");
  expect(task).toContain('resultVariable="agentIds"');
  expect(task).toContain('users="=agents"');
  expect(task).not.toContain("accountType");
  expect(page.__errors).toEqual([]);
});

test("fields hidden by a filled-in alternative are kept", async ({ page }) => {
  // Entra's inline attributes and the attributes variable are two ways to give one body.
  // Filling one in hides the other, but nothing about the operation changed: with both
  // filled in, the compiler names the conflict, and the panel does not settle it by
  // deleting one of them on an unrelated edit.
  await select(page, "Activity_licence");
  await page.locator("#f-st-retries").fill("4");
  await page.locator("#f-st-retries").blur();

  const task = await taskXML(page, "Activity_licence");
  expect(task).toContain('retries="4"');
  expect(task).toContain('attributesVariable="lizenzen"');
  expect(task).toContain("addLicenses");
  expect(page.__errors).toEqual([]);
});

test("a field the catalog describes twice is kept while one description shows it", async ({ page }) => {
  // CSV's columns are names for a delimited file and name:width for a fixed-width one:
  // two descriptions of one attribute, one of them always hidden. The hidden one does
  // not make the attribute unused.
  await select(page, "Activity_csv");
  await page.locator("#f-st-source").fill("datei");
  await page.locator("#f-st-source").blur();

  const task = await taskXML(page, "Activity_csv");
  expect(task).toContain('source="datei"');
  expect(task).toContain('columns="email,group,license"');
  expect(task).toContain('delimiter=";"');
  expect(page.__errors).toEqual([]);
});

test("switching a CSV task to fixed width keeps its columns and drops its delimiter", async ({ page }) => {
  await select(page, "Activity_csv");
  await page.locator("#f-st-format").selectOption("fixed-width");

  // The columns stay on screen, as names:widths now, for the author to correct.
  await expect(page.locator("#f-st-columns")).toHaveValue("email,group,license");
  const task = await taskXML(page, "Activity_csv");
  expect(task).toContain('format="fixed-width"');
  expect(task).toContain('columns="email,group,license"');
  expect(task).not.toContain("delimiter");
  expect(page.__errors).toEqual([]);
});

test("a SharePoint task authored before operations existed is saved as the create-item it was", async ({ page }) => {
  // The operation select shows Create list item for a task with no operation, and
  // what the panel writes for it names no other operation: the compiler reads an empty
  // operation as the create-item such a model always was.
  await select(page, "Activity_sharepoint");
  await expect(page.locator("#f-st-operation")).toHaveValue("");
  await page.locator("#f-st-resultVariable").fill("eintrag");
  await page.locator("#f-st-resultVariable").blur();

  const task = await taskXML(page, "Activity_sharepoint");
  expect(task).not.toMatch(/operation="[^"]/);
  expect(task).toContain('list="Auftraege"');
  expect(task).toContain('name="Title"');
  expect(task).toContain('resultVariable="eintrag"');
  expect(page.__errors).toEqual([]);
});

test("switching a SharePoint task to a file operation drops what an item operation carried", async ({ page }) => {
  await select(page, "Activity_sharepoint");
  await page.locator("#f-st-operation").selectOption("get-file");

  // The list becomes the library, which a file operation may leave empty; the columns
  // a create carried are nothing a check of a file reads, and the compiler refuses them.
  await expect(page.locator("#f-st-path")).toBeVisible();
  const task = await taskXML(page, "Activity_sharepoint");
  expect(task).toContain('operation="get-file"');
  expect(task).not.toContain("itemField");
  expect(task).toContain('site="https://contoso.sharepoint.com/sites/hr"');
  expect(page.__errors).toEqual([]);
});

test("a SharePoint task that names create-item outright keeps its list and columns", async ({ page }) => {
  // operation="create-item" is the same task as no operation at all. Were the panel to
  // show the list and the columns only for the empty value, the next save would drop
  // them as fields the operation does not use, and the compiler would refuse the task.
  await select(page, "Activity_sp_explicit");
  await expect(page.locator("#f-st-list")).toHaveValue("Protokoll");
  await page.locator("#f-st-resultVariable").fill("eintrag");
  await page.locator("#f-st-resultVariable").blur();

  const task = await taskXML(page, "Activity_sp_explicit");
  expect(task).toContain('list="Protokoll"');
  expect(task).toContain('name="Title"');
  expect(task).not.toMatch(/operation="(?!create-item"|")/);
  expect(page.__errors).toEqual([]);
});
