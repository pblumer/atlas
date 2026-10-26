// The confirm for deleting a DMN reference (api/web/dmnref-impact.js,
// ADR-0331).
//
// The point of the feature is a sentence somebody reads instead of clicks
// through, so the sentence is what these assert — not that a function returned
// something truthy. The sentence comes from the catalogue (ADR-0267), so it is
// read in the default locale, German.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/dmnref-impact-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const warn = (page, impact) => page.evaluate((i) => window.__warn(i), impact);

test("nothing at risk reads as the plain sentence", async ({ page }) => {
  // Three ways for nothing to be at risk, all of which must stay quiet: no impact
  // at all (the fetch failed), a handle that names no model, and a decision that
  // another reference also provides.
  for (const impact of [
    null,
    { resolved: false, decisions: [], exclusive: [], blocked: [] },
    { resolved: true, decisions: ["eligibility"], exclusive: [], blocked: [] },
  ]) {
    const text = await warn(page, impact);
    expect(text).toBe(
      "Diese DMN-Referenz löschen? Die Modelldatei bleibt im Modellspeicher und erscheint unter Nicht zugeordnet, " +
      "wo sich wieder eine Referenz darauf setzen lässt.");
  }
});

test("the last model for a decision is said so, even when nothing would break", async ({ page }) => {
  const text = await warn(page, {
    resolved: true, decisions: ["eligibility"], exclusive: ["eligibility"], blocked: [], blockedHidden: 0,
  });
  expect(text).toContain("letzte Modell für die Entscheidung eligibility");
  expect(text).toContain("Heute würde nichts ein Deployment verhindern");
});

test("each blocked artefact is named with its binding", async ({ page }) => {
  const text = await warn(page, {
    resolved: true,
    decisions: ["eligibility"],
    exclusive: ["eligibility"],
    blocked: [
      { kind: "deployed", processId: "orders", name: "Orders", key: 7, version: 2, decisionId: "eligibility", binding: "deployment" },
      { kind: "draft", processId: "reise", name: "", decisionId: "eligibility", binding: "latest" },
    ],
    blockedHidden: 0,
  });

  expect(text).toContain("2 Artefakte liessen sich danach nicht mehr deployen:");
  // A deployed definition says which version, so the reader can find it in Operations.
  expect(text).toContain("Orders (deployt v2) — eligibility, Bindung deployment");
  // A draft with no name falls back to its process id rather than showing a blank.
  expect(text).toContain("reise (Entwurf) — eligibility, Bindung latest");
  // And the thing that is *not* at risk is said too, because that is the fear.
  expect(text).toContain("Laufende Instanzen sind nicht betroffen");
  // And where the model itself went, because "not affected" was the old sentence's
  // reassurance and it is what left the file unreachable.
  expect(text).toContain("erscheint unter Nicht zugeordnet");
});

test("artefacts in applications the reader cannot see are counted, not named", async ({ page }) => {
  const text = await warn(page, {
    resolved: true,
    decisions: ["eligibility"],
    exclusive: ["eligibility"],
    blocked: [],
    blockedHidden: 3,
  });
  expect(text).toContain("3 Artefakte liessen sich danach nicht mehr deployen:");
  expect(text).toContain("3 weitere in Anwendungen, die du nicht sehen darfst");
});

test("the count covers what is named and what is only counted", async ({ page }) => {
  const text = await warn(page, {
    resolved: true,
    decisions: ["eligibility", "discount"],
    exclusive: ["eligibility", "discount"],
    blocked: [{ kind: "draft", processId: "reise", decisionId: "eligibility", binding: "deployment" }],
    blockedHidden: 1,
  });
  expect(text).toContain("letzte Modell für die Entscheidungen eligibility, discount");
  expect(text).toContain("2 Artefakte liessen sich danach nicht mehr deployen:");
  expect(text).toContain("1 weiteres in einer Anwendung, die du nicht sehen darfst");
});

// Which deployed version of a decision the Console offers to remove
// (api/web/decision-cleanup.js, ADR-0336). The
// server decides; this is the affordance that keeps a reader from clicking into a
// refusal, and it mirrors the server's two guards rather than approximating them.

const state = (page, row, rows) => page.evaluate(([r, all]) => window.__versionState(r, all), [row, rows]);

test("a version nothing holds may go", async ({ page }) => {
  const rows = [{ key: 1, version: 1, current: true }];
  expect(await state(page, rows[0], rows)).toEqual({ deletable: true, why: "" });
});

test("a pinned version says which process is holding it", async ({ page }) => {
  const rows = [
    { key: 1, version: 1, current: false, pinnedBy: [{ key: 9, processId: "orders", version: 2, decisionId: "eligibility" }] },
    { key: 2, version: 2, current: true },
  ];
  const got = await state(page, rows[0], rows);
  expect(got.deletable).toBe(false);
  // Named, because the reader's next move is to go and look at that process.
  expect(got.why).toContain("orders");
  expect(got.why).toContain("pinned this version when deployed");
});

test("the current version is held while older ones remain, and free once they are gone", async ({ page }) => {
  const two = [{ key: 1, version: 1 }, { key: 2, version: 2, current: true }];
  const held = await state(page, two[1], two);
  expect(held.deletable).toBe(false);
  expect(held.why).toContain("remove those first");

  // The last version of a decision may go: nothing survives to disagree about what
  // the next deploy's version number means.
  const one = [{ key: 2, version: 2, current: true }];
  expect(await state(page, one[0], one)).toEqual({ deletable: true, why: "" });
});

test("a superseded version with nothing pinned to it may go, which is the ordinary cleanup", async ({ page }) => {
  const rows = [{ key: 1, version: 1 }, { key: 2, version: 2 }, { key: 3, version: 3, current: true }];
  expect((await state(page, rows[0], rows)).deletable).toBe(true);
  expect((await state(page, rows[1], rows)).deletable).toBe(true);
  expect((await state(page, rows[2], rows)).deletable).toBe(false);
});

test("a pin outranks everything, including on the last version", async ({ page }) => {
  const rows = [{ key: 1, version: 1, current: true, pinnedBy: [{ key: 9, processId: "orders" }, { key: 10, processId: "billing" }] }];
  const got = await state(page, rows[0], rows);
  expect(got.deletable).toBe(false);
  expect(got.why).toContain("orders, billing");
  expect(got.why).toContain("those processes");
});
