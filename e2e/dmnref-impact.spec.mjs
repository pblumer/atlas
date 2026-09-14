// The confirm for deleting a DMN reference (api/web/dmnref-impact.js,
// ADR-0331).
//
// The point of the feature is a sentence somebody reads instead of clicks
// through, so the sentence is what these assert — not that a function returned
// something truthy.
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
      "Delete this DMN reference? The model file stays in the model store and appears under Not assigned, " +
      "where a reference can be put back on it.");
  }
});

test("the last model for a decision is said so, even when nothing would break", async ({ page }) => {
  const text = await warn(page, {
    resolved: true, decisions: ["eligibility"], exclusive: ["eligibility"], blocked: [], blockedHidden: 0,
  });
  expect(text).toContain("last model for the decision eligibility");
  expect(text).toContain("Nothing would stop deploying today");
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

  expect(text).toContain("2 artefacts could then not be deployed:");
  // A deployed definition says which version, so the reader can find it in Operations.
  expect(text).toContain("Orders (deployed v2) — eligibility, binding deployment");
  // A draft with no name falls back to its process id rather than showing a blank.
  expect(text).toContain("reise (draft) — eligibility, binding latest");
  // And the thing that is *not* at risk is said too, because that is the fear.
  expect(text).toContain("Running instances are not affected");
  // And where the model itself went, because "not affected" was the old sentence's
  // reassurance and it is what left the file unreachable.
  expect(text).toContain("appears under Not assigned");
});

test("artefacts in applications the reader cannot see are counted, not named", async ({ page }) => {
  const text = await warn(page, {
    resolved: true,
    decisions: ["eligibility"],
    exclusive: ["eligibility"],
    blocked: [],
    blockedHidden: 3,
  });
  expect(text).toContain("3 artefacts could then not be deployed:");
  expect(text).toContain("3 more in applications you cannot see");
});

test("the count covers what is named and what is only counted", async ({ page }) => {
  const text = await warn(page, {
    resolved: true,
    decisions: ["eligibility", "discount"],
    exclusive: ["eligibility", "discount"],
    blocked: [{ kind: "draft", processId: "reise", decisionId: "eligibility", binding: "deployment" }],
    blockedHidden: 1,
  });
  expect(text).toContain("last model for the decisions eligibility, discount");
  expect(text).toContain("2 artefacts could then not be deployed:");
  expect(text).toContain("1 more in an application you cannot see");
});
