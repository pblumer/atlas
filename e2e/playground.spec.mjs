// End-to-end coverage for the Playground tab's browser half (api/web/playground.js,
// ADR-0215).
//
// The tab is a mode, not a level of detail: it takes over the control strip and a side
// panel, and it drives a server-side sandbox over a dozen endpoints. The Go tests cover
// the sandbox and the API; only a real DOM can show that the tab wires the two together
// — that a waiting task becomes a button, that completing it repaints the diagram, and
// that leaving the editor releases the sandbox instead of leaving it to its TTL.
import { test, expect } from "@playwright/test";

// The editor's toolbar runs the width of the window; a narrow viewport puts the tabs and
// the Playground bar off screen.
test.use({ viewport: { width: 1600, height: 900 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/playground-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator('.etabs button[data-tab="playground"]')).toBeVisible();
});

const calls = (page) => page.evaluate(() => window.__calls);

// openTab switches to the Playground and starts its sandbox.
async function startSandbox(page) {
  await page.locator('.etabs button[data-tab="playground"]').click();
  await expect(page.locator("#pg-bar")).toBeVisible();
  await page.locator("#pg-start").click();
  await expect(page.locator("#pg-case")).toBeVisible();
}

test("the tab is a mode: it takes the bar and the panel, and gives back the canvas", async ({ page }) => {
  // Off the tab, nothing of the Playground is on screen.
  await expect(page.locator("#pg-bar")).toBeHidden();
  await expect(page.locator("#pg-panel")).toBeHidden();
  await expect(page.locator("#pg-setup")).toBeHidden();
  await expect(page.locator("#props")).toBeVisible();

  await page.locator('.etabs button[data-tab="playground"]').click();
  await expect(page.locator("#pg-bar")).toBeVisible();
  await expect(page.locator("#pg-panel")).toBeVisible();
  await expect(page.locator("#pg-setup")).toBeVisible();
  // Two side panels would leave the diagram — the thing being watched — a sliver.
  await expect(page.locator("#props")).toBeHidden();
  // And nothing that offers to change the diagram: the Playground runs the model as
  // it stood when the sandbox started, so a palette here promises an edit the run
  // would not carry.
  await expect(page.locator(".djs-palette")).toBeHidden();

  // The panel's ✕ leaves the mode through the tab that owns it, so the tab bar and the
  // panel cannot disagree about which mode is on.
  await page.locator("#pg-panel-close").click();
  await expect(page.locator("#pg-panel")).toBeHidden();
  await expect(page.locator('.etabs button[data-tab="design"]')).toHaveClass(/active/);
  await expect(page.locator("#props")).toBeVisible();
  await expect(page.locator(".djs-palette")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("starting a sandbox sends the diagram on screen, not a stored copy", async ({ page }) => {
  await startSandbox(page);
  const open = (await calls(page)).find((c) => /\/playground\/sessions$/.test(c.url));
  expect(open.body.source).toBe("xml");
  expect(open.body.xml).toContain('id="credit"');
  // The whole policy is one stub duration, and it is fixed for the sandbox's life.
  expect(open.body.stubs.default.minMillis).toBe(60000);
  await expect(page.locator("#pg-stats")).toContainText("seed 4711");
  await expect(page.locator("#pg-dur-wrap")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a waiting task becomes a button, and completing it repaints the diagram", async ({ page }) => {
  await startSandbox(page);
  await page.locator("#pg-startvars").fill('{"amount": 12400}');
  await page.locator("#pg-case").click();

  // The person at the keyboard is the worker for a user task.
  const task = page.locator(".pg-task").filter({ hasText: "review" });
  await expect(task).toBeVisible();
  await expect(task).toContainText("user task");
  await expect(page.locator("#pg-hint")).toContainText("waiting for you");

  // The case's variables reached the server as JSON, not as text.
  const started = (await calls(page)).find((c) => /\/cases$/.test(c.url) && c.method === "POST");
  expect(started.body.variables).toEqual({ amount: 12400 });

  // Two elements have been reached, so two carry a count; the waiting one is live.
  await expect(page.locator(".token-badge")).toHaveCount(2);
  await expect(page.locator('.djs-element[data-element-id="review"].atlas-active')).toHaveCount(1);

  await page.locator("#pg-outputs").fill('{"decision":"approved"}');
  await task.locator("button").click();

  await expect(page.locator(".pg-result")).toContainText("completed");
  await expect(page.locator(".pg-result")).toContainText("start → review → score → done");
  await expect(page.locator(".pg-vars")).toContainText("approved");
  // The whole path is drawn now, and nothing is live any more.
  await expect(page.locator(".token-badge")).toHaveCount(4);
  await expect(page.locator(".atlas-active")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("bad JSON is reported rather than sent", async ({ page }) => {
  await startSandbox(page);
  await page.locator("#pg-startvars").fill("{not json");
  await page.locator("#pg-case").click();
  // Nothing was posted, and the panel is still usable.
  const posted = (await calls(page)).filter((c) => /\/cases$/.test(c.url) && c.method === "POST");
  expect(posted).toHaveLength(0);
  await expect(page.locator("#pg-case")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("editing the diagram says the run no longer matches it", async ({ page }) => {
  await startSandbox(page);
  await page.evaluate(() => {
    const modeler = window.__atlasModeler;
    const el = modeler.get("elementRegistry").get("review");
    modeler.get("modeling").updateProperties(el, { name: "Antrag doppelt prüfen" });
  });
  await expect(page.locator("#pg-hint")).toContainText("diagram changed");
  expect(page.__errors).toEqual([]);
});

test("leaving the editor releases the sandbox", async ({ page }) => {
  await startSandbox(page);
  await page.evaluate(() => window.__leave());
  await expect
    .poll(async () => (await calls(page)).some((c) => c.method === "DELETE" && /\/playground\/sessions\//.test(c.url)))
    .toBe(true);
  expect(page.__errors).toEqual([]);
});

// switchToBatch opens the sandbox and moves to the batch half of the panel.
async function switchToBatch(page) {
  await startSandbox(page);
  await page.locator('#pg-setup button[data-mode="batch"]').click();
  await expect(page.locator("#pg-batch")).toBeVisible();
}

test("the pool setup is read off the diagram, and travels with the sandbox", async ({ page }) => {
  await page.locator('.etabs button[data-tab="playground"]').click();
  // A row per task the author drew, named as they named it — not a list to retype.
  await expect(page.locator(".pg-pool")).toHaveCount(2);
  await expect(page.locator(".pg-pool").first()).toContainText("Antrag prüfen");

  await page.locator('.pg-pool input[data-pool="review"]').fill("clerks");
  await page.locator('.pg-pool input[data-seats="review"]').fill("3");
  await page.locator("#pg-hours").check();
  // A batch needs its user tasks answered by something; "leave for me" is Step's.
  await page.locator("#pg-human").selectOption("3600000");
  await page.locator("#pg-start").click();
  await expect(page.locator("#pg-case")).toBeVisible();

  const open = (await calls(page)).find((c) => /\/playground\/sessions$/.test(c.url));
  expect(open.body.stubs.pools).toEqual({
    clerks: { capacity: 3, calendar: { open: [{ fromMinutes: 480, toMinutes: 1020 }], days: [1, 2, 3, 4, 5] } },
  });
  expect(open.body.stubs.poolOf).toEqual({ review: "clerks" });
  expect(open.body.stubs.human).toEqual({ minMillis: 3600000, maxMillis: 3600000 });
  expect(page.__errors).toEqual([]);
});

test("a batch runs, is polled to a stop, and reports what it did", async ({ page }) => {
  await switchToBatch(page);
  await page.locator("#pg-cases").fill('[{"amount":10},{"amount":20},{"amount":5000}]');
  await page.locator("#pg-arrival").selectOption("every");
  await page.locator("#pg-arrival-n").fill("15");
  await page.locator("#pg-batch").click();

  // The dataset and the timing reached the server as data, not as text.
  const started = (await calls(page)).find((c) => c.method === "POST" && /\/runs$/.test(c.url));
  expect(started.body.cases).toHaveLength(3);
  expect(started.body.arrival).toEqual({ mode: "every", intervalMillis: 900000 });

  // The report arrives on its own, because the panel polled until the run stopped.
  await expect(page.locator(".pg-facts").first()).toContainText("of 3 finished");
  await expect(page.locator("#pg-hint")).toContainText("report is in the panel");

  // The bottleneck ranking puts the queue first: review waited, score did not.
  const rows = page.locator(".pg-bottlenecks tbody tr");
  await expect(rows.first()).toContainText("review");
  await expect(rows.first()).toContainText("3h");
  await expect(page.locator(".pg-pools")).toContainText("clerks");

  // And the run over time is drawn rather than described.
  await expect(page.locator(".pg-chart")).toBeVisible();
  await expect(page.locator(".pg-chart .pg-line")).toHaveCount(1);
  await expect(page.locator(".pg-legend")).toContainText("peak 3");

  // Polling stops when the run does: no further status calls after the report.
  const before = (await calls(page)).filter((c) => c.method === "GET" && /\/runs$/.test(c.url)).length;
  await page.waitForTimeout(1600);
  const after = (await calls(page)).filter((c) => c.method === "GET" && /\/runs$/.test(c.url)).length;
  expect(after).toBe(before);
  expect(page.__errors).toEqual([]);
});

test("the heat map shades elements and flows, and names what was never reached", async ({ page }) => {
  await switchToBatch(page);
  await page.locator("#pg-batch").click();
  await expect(page.locator(".pg-facts").first()).toBeVisible();

  // The busiest parts take the top of the scale, and the flow nobody took is
  // marked as never reached rather than merely left plain.
  await expect(page.locator('.djs-element[data-element-id="review"].pg-heat-5')).toHaveCount(1);
  await expect(page.locator('.djs-element[data-element-id="f1"].pg-heat-5')).toHaveCount(1);
  await expect(page.locator('.djs-element[data-element-id="f3"].pg-heat-0')).toHaveCount(1);
  // A sequence flow is named by its ends on the wire; the client resolved f3 from
  // score → done against its own diagram.
  await expect(page.locator(".pg-cold")).toContainText("score → done");

  // "Off" takes the shading away again, leaving the diagram as it was.
  await page.locator('#pg-overlay button[data-overlay="off"]').click();
  await expect(page.locator(".pg-heat-5")).toHaveCount(0);
  await page.locator('#pg-overlay button[data-overlay="runs"]').click();
  await expect(page.locator('.djs-element[data-element-id="review"].pg-heat-5')).toHaveCount(1);

  // Stepping is a different question, so it is not answered on a shaded diagram.
  await page.locator('#pg-setup button[data-mode="step"]').click();
  await expect(page.locator(".pg-heat-5")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a CSV dataset is uploaded as a file, not parsed in the browser", async ({ page }) => {
  await switchToBatch(page);
  await page.locator('#pg-setup button[data-source="csv"]').click();
  await page.locator("#pg-csv").setInputFiles({
    name: "antraege.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("kunde,amount\nA,10\nB,5000\n"),
  });
  await expect(page.locator(".pg-file")).toContainText("antraege.csv");
  await page.locator("#pg-batch").click();

  const up = (await calls(page)).find((c) => /\/runs\/csv$/.test(c.url));
  expect(up.body.file).toBe("antraege.csv");
  expect(up.body.arrival.mode).toBe("allAtOnce");
  // The rows were never parsed here: the server reads them with the same code a
  // real CSV import uses, so the Playground and production read a file alike.
  const inline = (await calls(page)).filter((c) => c.method === "POST" && /\/runs$/.test(c.url));
  expect(inline).toHaveLength(0);
  await expect(page.locator(".pg-facts").first()).toContainText("of 3 finished");
  expect(page.__errors).toEqual([]);
});

test("stopping a batch leaves what it did readable", async ({ page }) => {
  await switchToBatch(page);
  await page.locator("#pg-batch").click();
  await expect(page.locator("#pg-cancel")).toBeVisible();
  await page.locator("#pg-cancel").click();

  await expect(page.locator(".pg-run-line")).toContainText("of 3 finished");
  await expect(page.locator(".pg-facts").first()).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("a run is judged, and the verdict names what it missed", async ({ page }) => {
  await switchToBatch(page);
  // The two boxes an author ticks are the two a build exits on.
  await expect(page.locator("#pg-x-finish")).toBeChecked();
  await expect(page.locator("#pg-x-inc")).toBeChecked();
  await page.locator("#pg-x-p90").fill("2");
  await page.locator("#pg-x-reach").fill("review, score");
  await page.locator("#pg-batch").click();
  await expect(page.locator(".pg-verdict")).toBeVisible();

  // What the panel asked for is what the checkboxes said, resolved against the
  // run that happened: "every case finishes" is the cases this run had.
  const judged = (await calls(page)).find((c) => /\/verdict$/.test(c.url));
  expect(judged.body).toEqual({
    minCompleted: 3,
    maxIncidents: 0,
    maxP90Millis: 7200000,
    minVisits: { review: 1, score: 1 },
  });

  // The verdict is a badge first and a table second, and a failed check is marked.
  await expect(page.locator(".pg-verdict")).toHaveText("failed");
  await expect(page.locator(".pg-verdict")).toHaveClass(/bad/);
  // Scoped to the verdict's own table: a case parked behind an incident is marked
  // the same way down in the results strip.
  await expect(page.locator(".pg-checks tr.pg-bad")).toContainText("queue at clerks");
  await expect(page.locator(".pg-checks tr.pg-bad")).toContainText("at most 5");
  expect(page.__errors).toEqual([]);
});

test("a saved scenario is opened, and it replaces the sandbox", async ({ page }) => {
  await page.locator('.etabs button[data-tab="playground"]').click();
  // The diagram's saved runs are offered before a sandbox exists, because a
  // scenario carries the policy a sandbox is opened with.
  await expect(page.locator("#pg-scenario-pick")).toBeVisible();
  await page.locator("#pg-scenario-pick").selectOption("nightly");
  await page.locator("#pg-scenario-open").click();

  // It opened a session with the scenario's own request, not with the panel's.
  await expect(page.locator("#pg-batch")).toBeVisible();
  const opened = (await calls(page)).filter((c) => /\/playground\/sessions$/.test(c.url) && c.method === "POST");
  expect(opened).toHaveLength(1);
  expect(opened[0].body.seed).toBe(4711);
  expect(opened[0].body.stubs.poolOf).toEqual({ review: "clerks" });

  // And the panel is filled in from it: the dataset, the timing, the targets.
  await expect(page.locator("#pg-cases")).toContainText("5000");
  await expect(page.locator("#pg-arrival")).toHaveValue("every");
  await expect(page.locator("#pg-arrival-n")).toHaveValue("15");
  await expect(page.locator("#pg-x-p90")).toHaveValue("2");
  await expect(page.locator("#pg-x-reach")).toHaveValue("review");
  expect(page.__errors).toEqual([]);
});

test("a run is saved as the scenario it was, requests and all", async ({ page }) => {
  await switchToBatch(page);
  await page.locator("#pg-cases").fill('[{"amount":10}]');
  await page.locator("#pg-x-p90").fill("4");
  await page.locator("#pg-scenario-name").fill("Smoke test");
  await page.locator("#pg-scenario-save").click();

  const saved = (await calls(page)).find((c) => c.method === "POST" && /\/playground\/scenarios$/.test(c.url));
  expect(saved.body.name).toBe("Smoke test");
  expect(saved.body.processId).toBe("credit");
  // What is stored is the three requests that make the run — the same bodies the
  // endpoints take, which is why nothing here can drift from them.
  expect(saved.body.spec.open.source).toBe("xml");
  // The seed the sandbox actually used is written down. Without it the scenario
  // would be re-seeded from the clock on every open, and a run saved as
  // reproducible would come back with different numbers.
  expect(saved.body.spec.open.seed).toBe(4711);
  expect(saved.body.spec.run.cases).toEqual([{ amount: 10 }]);
  expect(saved.body.spec.expect.maxP90Millis).toBe(14400000);
  expect(page.__errors).toEqual([]);
});

test("a run is set beside the baseline, and only what moved is shown", async ({ page }) => {
  await page.locator('.etabs button[data-tab="playground"]').click();
  await page.locator("#pg-scenario-pick").selectOption("nightly");
  await page.locator("#pg-scenario-open").click();
  await expect(page.locator("#pg-batch")).toBeVisible();
  await page.locator("#pg-batch").click();
  await expect(page.locator(".pg-verdict")).toBeVisible();

  // The comparison used the baseline the scenario carries, not one the panel made up.
  const compared = (await calls(page)).find((c) => /\/compare$/.test(c.url));
  expect(compared.body.baseline.pools.clerks.maxQueue).toBe(9);

  // Three of the four measures moved; the one that did not is left out, because a
  // table of unchanged numbers is where the ones that did move go to hide.
  await expect(page.locator(".pg-deltas tbody tr")).toHaveCount(3);
  await expect(page.locator(".pg-worse")).toHaveText("12");
  await expect(page.locator(".pg-better")).toHaveText("2h");
  // Utilisation moved but has no good direction, so it carries neither colour.
  const util = page.locator(".pg-deltas tr").filter({ hasText: "utilisation at clerks" });
  await expect(util).toBeVisible();
  await expect(util.locator(".pg-better, .pg-worse")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a failing run cannot be kept as the baseline", async ({ page }) => {
  await page.locator('.etabs button[data-tab="playground"]').click();
  await page.locator("#pg-scenario-pick").selectOption("nightly");
  await page.locator("#pg-scenario-open").click();
  await page.locator("#pg-batch").click();
  await expect(page.locator(".pg-verdict")).toHaveText("failed");

  // The control is there — this run belongs to a scenario — and refuses, because a
  // failing baseline would hide the failure from every run after it.
  const keep = page.locator("#pg-keep-baseline");
  await expect(keep).toBeVisible();
  await expect(keep).toBeDisabled();
  expect((await calls(page)).filter((c) => /\/baseline$/.test(c.url))).toHaveLength(0);
  expect(page.__errors).toEqual([]);
});

test("a CSV run says why it cannot be saved as a scenario", async ({ page }) => {
  await switchToBatch(page);
  await page.locator('#pg-setup button[data-source="csv"]').click();
  await page.locator("#pg-csv").setInputFiles({
    name: "rows.csv", mimeType: "text/csv", buffer: Buffer.from("kunde\nA\n"),
  });
  // No name box and no Save: the rows are on the server, parsed by the same code a
  // real import uses, and are not in the browser to store.
  await expect(page.locator("#pg-scenario-save")).toHaveCount(0);
  await expect(page.locator("#pg-setup")).toContainText("cannot be saved as a scenario");
  expect(page.__errors).toEqual([]);
});

test("a dataset is described rather than listed, and the description is what runs", async ({ page }) => {
  await switchToBatch(page);
  await page.locator('#pg-setup button[data-source="generated"]').click();

  // It opens on the dataset everybody wants first: a few hundred cases with a
  // random amount. Nobody types three hundred of those, which is the whole point.
  await expect(page.locator("#pg-gen-count")).toHaveValue("300");
  await expect(page.locator('.pg-field [data-gen="name"]').first()).toHaveValue("amount");

  await page.locator("#pg-gen-count").fill("500");
  // A second field: a weighted choice, which is how the rare branch stays rare.
  await page.locator("#pg-gen-add").click();
  const tier = page.locator(".pg-field").nth(1);
  await tier.locator('[data-gen="name"]').fill("tier");
  await tier.locator('[data-gen="kind"]').selectOption("choice");
  await tier.locator('[data-gen="choices"]').fill("gold:1, standard:9");

  // The preview is read before the run, and says it is the run rather than a
  // sample of what one might look like.
  await page.locator("#pg-gen-preview").click();
  await expect(page.locator(".pg-preview th").first()).toHaveText("amount");
  await expect(page.locator(".pg-preview th").nth(1)).toHaveText("tier");
  await expect(page.locator("#pg-setup")).toContainText("of 500");

  await page.locator("#pg-batch").click();
  const started = (await calls(page)).find((c) => c.method === "POST" && /\/runs$/.test(c.url));
  // The description travels, not five hundred rows built here: that is what keeps
  // the request small and the run repeatable.
  expect(started.body.cases).toBeUndefined();
  expect(started.body.generate.count).toBe(500);
  expect(started.body.generate.fields).toEqual([
    { name: "amount", kind: "int", min: 100, max: 5000 },
    { name: "tier", kind: "choice", choices: [{ value: "gold", weight: 1 }, { value: "standard", weight: 9 }] },
  ]);
  expect(page.__errors).toEqual([]);
});

test("a described dataset is saved as a scenario, which an uploaded one cannot be", async ({ page }) => {
  await switchToBatch(page);
  await page.locator('#pg-setup button[data-source="generated"]').click();
  await page.locator("#pg-scenario-name").fill("Three hundred applications");
  await page.locator("#pg-scenario-save").click();

  const saved = (await calls(page)).find((c) => c.method === "POST" && /\/playground\/scenarios$/.test(c.url));
  // The stored run is the description, so re-running it next month produces the
  // same five hundred amounts — which is what a CSV upload can never offer.
  expect(saved.body.spec.run.cases).toBeUndefined();
  expect(saved.body.spec.run.generate.count).toBe(300);
  expect(saved.body.spec.run.generate.fields[0]).toEqual({ name: "amount", kind: "int", min: 100, max: 5000 });
  expect(saved.body.spec.open.seed).toBe(4711);
  expect(page.__errors).toEqual([]);
});

test("a field's kind decides which parameters it shows", async ({ page }) => {
  await switchToBatch(page);
  await page.locator('#pg-setup button[data-source="generated"]').click();
  const field = page.locator(".pg-field").first();

  // A whole number is bounded; a sequence is not bounded at all, it is prefixed.
  await expect(field.locator('[data-gen="min"]')).toBeVisible();
  await field.locator('[data-gen="kind"]').selectOption("sequence");
  await expect(field.locator('[data-gen="min"]')).toHaveCount(0);
  await field.locator('[data-gen="prefix"]').fill("ORDER-");

  await page.locator("#pg-gen-preview").click();
  await expect(page.locator(".pg-preview td").first()).toHaveText("ORDER-001");

  // A removed field is gone from what runs, not merely hidden.
  await page.locator("[data-gen-del]").first().click();
  await expect(page.locator(".pg-field")).toHaveCount(0);
  await page.locator("#pg-batch").click();
  const started = (await calls(page)).find((c) => c.method === "POST" && /\/runs$/.test(c.url));
  expect(started.body.generate.fields).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("the mode lays the editor out in three columns and a strip", async ({ page }) => {
  await switchToBatch(page);
  // What decides the run is on the left; what it did is on the right. Stacked in one
  // column, a finished run pushed the dataset off the screen.
  await expect(page.locator("#pg-setup")).toContainText("Dataset");
  await expect(page.locator("#pg-setup")).toContainText("Timing");
  await expect(page.locator("#pg-panel")).not.toContainText("Timing");
  // Before anything has run the analysis column says so rather than sitting blank.
  await expect(page.locator("#pg-panel")).toContainText("Nothing has run yet");
  // And the strip is not there at all: an empty band under the diagram would be a
  // promise the panel has not kept.
  await expect(page.locator("#pg-results")).toBeHidden();

  await page.locator("#pg-batch").click();
  await expect(page.locator(".pg-facts").first()).toBeVisible();
  await expect(page.locator("#pg-panel")).toContainText("Outcomes");
  await expect(page.locator("#pg-setup")).toContainText("Dataset");

  // The setup column is still readable with a report on screen — the whole point of
  // splitting them.
  await expect(page.locator("#pg-cases")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("the cases are read a page at a time under the diagram", async ({ page }) => {
  // A hundred and twenty of them: the strip has to page rather than hold the run.
  await page.evaluate(() => window.__setCases(120));
  await switchToBatch(page);
  await page.locator("#pg-batch").click();
  await expect(page.locator("#pg-results")).toBeVisible();

  // A window, asked of the server — not the whole run built in the browser. That is
  // what makes the fifty-thousandth case cost the same as the fiftieth.
  const first = (await calls(page)).find((c) => c.method === "GET" && /\/results\?/.test(c.url));
  expect(first.url).toContain("offset=0");
  expect(first.url).toContain("limit=50");
  await expect(page.locator(".pg-cases tbody tr")).toHaveCount(50);
  await expect(page.locator(".pg-results-head")).toContainText("1–50 of 120 cases");
  await expect(page.locator("#pg-page-prev")).toBeDisabled();
  // A case parked behind an incident is marked, which is the row somebody is looking
  // for in a table of a hundred.
  await expect(page.locator(".pg-cases tr.pg-bad")).toHaveCount(16);

  await page.locator("#pg-page-next").click();
  await expect(page.locator(".pg-results-head")).toContainText("51–100 of 120 cases");
  await expect(page.locator("#pg-page-prev")).toBeEnabled();

  await page.locator("#pg-page-next").click();
  await expect(page.locator(".pg-results-head")).toContainText("101–120 of 120 cases");
  await expect(page.locator("#pg-page-next")).toBeDisabled();

  await page.locator("#pg-page-prev").click();
  await expect(page.locator(".pg-results-head")).toContainText("51–100 of 120 cases");

  // The variables each case ran with are columns of their own, so a row can be read
  // back against the dataset that produced it.
  await expect(page.locator(".pg-cases th")).toContainText(["case", "outcome", "duration", "incidents", "amount", "kunde"]);
  expect(page.__errors).toEqual([]);
});

test("a per-case rule is written against the diagram and judged case by case", async ({ page }) => {
  await switchToBatch(page);
  // Nothing is asserted per case until an author says so — and the panel says what
  // the kind of statement is for rather than showing an empty box.
  await expect(page.locator("#pg-setup")).toContainText("A rule holds a class of cases to an outcome");
  await page.locator("#pg-rule-add").click();

  const rule = page.locator(".pg-rule").first();
  await rule.locator('[data-rule="when"]').fill("betrag < 1000");
  // The end events come off the canvas, the way the pool rows do: an author asserts
  // against the outcome they drew rather than one they retyped.
  await rule.locator("[data-rule-end]").selectOption("done");
  await expect(rule.locator('[data-rule="then"]')).toHaveValue('end = "done"');
  // And the box stays editable, because not every assertion is about an end event.
  await rule.locator('[data-rule="then"]').fill('end = "approved"');

  await page.locator("#pg-rule-add").click();
  const second = page.locator(".pg-rule").nth(1);
  await second.locator('[data-rule="when"]').fill("betrag > 1000");
  await second.locator('[data-rule="then"]').fill('end = "approved"');

  await page.locator("#pg-batch").click();
  await expect(page.locator(".pg-verdict")).toBeVisible();

  // The rules travel in the same body the run-wide bounds do, so one request
  // decides the verdict and a scenario stores both together.
  const judged = (await calls(page)).find((c) => /\/verdict$/.test(c.url));
  expect(judged.body.rules).toEqual([
    { when: "betrag < 1000", then: 'end = "approved"' },
    { when: "betrag > 1000", then: 'end = "approved"' },
  ]);

  // What came back is shown as a split, not as one number: held, broke it, and
  // which cases did.
  await expect(page.locator(".pg-rule-result")).toHaveCount(2);
  await expect(page.locator(".pg-rule-result").first()).toContainText("2 held");
  await expect(page.locator(".pg-rule-result").nth(1)).toContainText("1 broke it");
  await expect(page.locator(".pg-rule-result").nth(1)).toContainText("cases 3");
  await expect(page.locator(".pg-rule-result").nth(1)).toHaveClass(/bad/);
  await expect(page.locator(".pg-rule-result").first()).not.toHaveClass(/bad/);

  // And the offending case is marked in the results strip, so the number in the
  // panel and the row under the diagram are the same case.
  await expect(page.locator(".pg-cases tbody tr").nth(2)).toHaveClass(/pg-bad/);
  await expect(page.locator(".pg-cases tbody tr").first()).not.toHaveClass(/pg-bad/);
  expect(page.__errors).toEqual([]);
});

test("a rule is stored in the scenario and read back into the boxes", async ({ page }) => {
  await switchToBatch(page);
  await page.locator("#pg-rule-add").click();
  const rule = page.locator(".pg-rule").first();
  await rule.locator('[data-rule="when"]').fill("betrag < 1000");
  await rule.locator('[data-rule="then"]').fill('end = "done"');
  await page.locator("#pg-scenario-name").fill("Kleine Anträge");
  await page.locator("#pg-scenario-save").click();

  const saved = (await calls(page)).find((c) => c.method === "POST" && /\/playground\/scenarios$/.test(c.url));
  expect(saved.body.spec.expect.rules).toEqual([{ when: "betrag < 1000", then: 'end = "done"' }]);

  // A rule with nothing to show is a row somebody has not filled in yet, not a
  // rule: it is left out rather than stored as an assertion that checks nothing.
  await page.locator("#pg-rule-add").click();
  await page.locator("#pg-scenario-save").click();
  const again = (await calls(page)).filter((c) => c.method === "POST" && /\/playground\/scenarios$/.test(c.url));
  expect(again[again.length - 1].body.spec.expect.rules).toHaveLength(1);
  expect(page.__errors).toEqual([]);
});

test("the overlay shades the diagram by one measure at a time", async ({ page }) => {
  await switchToBatch(page);
  await page.locator("#pg-batch").click();
  await expect(page.locator("#pg-overlay")).toBeVisible();

  // A run opens on the token counts, and the legend says what the darkest shade is
  // worth — a colour means nothing until it is read against a scale.
  await expect(page.locator('#pg-overlay button[data-overlay="runs"]')).toHaveClass(/active/);
  await expect(page.locator(".pg-scale")).toContainText("3");
  await expect(page.locator('[data-container-id="review"] .token-badge')).toHaveText("3");

  // Waiting is a different quantity, so the badges change with the shading rather
  // than staying behind as counts under a scale that is now in hours.
  await page.locator('#pg-overlay button[data-overlay="wait"]').click();
  await expect(page.locator('[data-container-id="review"] .token-badge')).toHaveText("3h");
  await expect(page.locator(".pg-scale")).toContainText("3h");
  // An element with nothing to say is left alone rather than drawn cold: the dashed
  // "never reached" style belongs to the token counts, where zero means the data did
  // not get there. Here zero means "no case waited", which is most of a healthy
  // diagram — and a start event has no badge either.
  await expect(page.locator('.djs-element[data-element-id="start"].pg-heat-0')).toHaveCount(0);
  await expect(page.locator('[data-container-id="start"] .token-badge')).toHaveCount(0);
  // And the flows step out: an edge has no waiting time, so shading it from the
  // token counts would put two quantities on one picture.
  await expect(page.locator("#pg-overlay")).toContainText("shapes only");
  await expect(page.locator('.djs-element[data-element-id="f1"].pg-heat-5')).toHaveCount(0);

  await page.locator('#pg-overlay button[data-overlay="incidents"]').click();
  await expect(page.locator('[data-container-id="score"] .token-badge')).toHaveText("2");
  await expect(page.locator('[data-container-id="review"] .token-badge')).toHaveCount(0);

  // Duration is the work itself, which is a different picture again: the service
  // task that runs in seconds is cold where the human task is hot.
  await page.locator('#pg-overlay button[data-overlay="work"]').click();
  await expect(page.locator('[data-container-id="review"] .token-badge')).toHaveText("3h");
  await expect(page.locator('[data-container-id="score"] .token-badge')).toHaveText("3m");
  expect(page.__errors).toEqual([]);
});
