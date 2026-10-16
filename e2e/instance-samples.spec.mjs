// e2e for api/web/instance-samples.js — the live values the Developer View and the
// Variables panel show (ADR-0145). What matters is which instances are offered (every
// version, running first, then the newest), which requests that costs (one per
// version, once per session until a reload), and that a search hit is shown with its
// whole variable set even though the search itself only answers with what matched.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/instance-samples-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// Three versions of "order" and one other process, whose instances must never show.
async function seed(page) {
  await page.evaluate(() => {
    const v = (name, value, kind = "string") => ({ name, value, kind });
    window.__db.processes = [
      { key: 101, processId: "order", version: 1 },
      { key: 103, processId: "order", version: 3 },
      { key: 102, processId: "order", version: 2 },
      { key: 201, processId: "other", version: 1 },
    ];
    window.__db.lists = {
      103: [{ key: 31, processDefKey: 103, version: 3, state: "completed", createdAt: 300, variables: [v("amount", "3", "number")] }],
      102: [{ key: 21, processDefKey: 102, version: 2, state: "active", createdAt: 100, variables: [v("amount", "2", "number")] }],
      101: [{ key: 11, processDefKey: 101, version: 1, state: "completed", createdAt: 400, variables: [v("amount", "1", "number")] }],
      201: [{ key: 99, processDefKey: 201, version: 1, state: "active", createdAt: 999, variables: [] }],
    };
  });
}

test("the list spans every version: running first, then the newest", async ({ page }) => {
  await seed(page);
  const got = await page.evaluate(() => window.__lookup("order", {}));
  expect(got.instances.map((i) => [i.key, i.version])).toEqual([[21, 2], [11, 1], [31, 3]]);
  // The running instance is the default, with its own values.
  expect(got.instance.key).toBe(21);
  expect(got.values).toEqual({ amount: { value: "2", kind: "number" } });
  expect(got.query).toBe("");
  expect(got.truncated).toBe(false);

  // One request for the versions, one page per version of this process, none for the
  // other process — and each page asks for no more than the picker can offer.
  const calls = await page.evaluate(() => window.__calls);
  expect(calls[0]).toBe("/api/v1/processes");
  expect(calls.slice(1).sort()).toEqual([
    "/api/v1/instances?process=101&limit=50",
    "/api/v1/instances?process=102&limit=50",
    "/api/v1/instances?process=103&limit=50",
  ]);
});

test("picking an instance is a re-render, and a forced read asks again", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => window.__lookup("order", {}));
  const before = await page.evaluate(() => window.__calls.length);

  const picked = await page.evaluate(() => window.__lookup("order", { instanceKey: 31 }));
  expect(picked.instance.key).toBe(31);
  expect(picked.values.amount.value).toBe("3");
  expect(await page.evaluate(() => window.__calls.length)).toBe(before);

  await page.evaluate(() => window.__lookup("order", { force: true, instanceKey: 31 }));
  expect(await page.evaluate(() => window.__calls.length)).toBe(before * 2);
});

test("a version that cannot be read leaves the others offered", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => { window.__fail = ["/api/v1/instances?process=102"]; });
  const got = await page.evaluate(() => window.__lookup("order", {}));
  expect(got.instances.map((i) => i.key)).toEqual([11, 31]);
});

test("a process that never ran, or was never deployed, has no samples", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => { window.__db.lists = {}; });
  expect(await page.evaluate(() => window.__lookup("order", {}))).toBeNull();
  expect(await page.evaluate(() => window.__lookup("nowhere", {}))).toBeNull();
});

test("a search asks every version, and the picked hit is read whole", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => {
    const v = (name, value, kind = "string") => ({ name, value, kind });
    window.__db.search = (def, q) => {
      // The content search: only the variable that matched.
      if (q === "Test=Versuch 2" && def === 103) {
        return { items: [{ key: 32, processDefKey: 103, version: 3, state: "completed", createdAt: 350, variables: [v("Test", "Versuch 2")] }], truncated: false };
      }
      if (q === "Test=Versuch 2" && def === 101) {
        return { items: [{ key: 12, processDefKey: 101, version: 1, state: "completed", createdAt: 120, variables: [v("Test", "Versuch 2")] }], truncated: false };
      }
      // The point read by key, scoped to the hit's own version: every variable.
      if (q === "32" && def === 103) {
        return { items: [{ key: 32, processDefKey: 103, version: 3, state: "completed", createdAt: 350, variables: [v("Test", "Versuch 2"), v("amount", "7", "number")] }], truncated: false };
      }
      if (q === "12" && def === 101) {
        return { items: [{ key: 12, processDefKey: 101, version: 1, state: "completed", createdAt: 120, variables: [v("Test", "Versuch 2"), v("amount", "5", "number")] }], truncated: false };
      }
      return { items: [], truncated: false };
    };
  });

  const got = await page.evaluate(() => window.__lookup("order", { query: " Test=Versuch 2 " }));
  expect(got.query).toBe("Test=Versuch 2");
  expect(got.instances).toEqual([
    { key: 32, state: "completed", version: 3, createdAt: 350, match: "Test=Versuch 2" },
    { key: 12, state: "completed", version: 1, createdAt: 120, match: "Test=Versuch 2" },
  ]);
  expect(got.instance.key).toBe(32);
  expect(got.values).toEqual({ Test: { value: "Versuch 2", kind: "string" }, amount: { value: "7", kind: "number" } });

  const calls = await page.evaluate(() => window.__calls);
  const q = encodeURIComponent("Test=Versuch 2");
  expect(calls.filter((c) => c.includes(`q=${q}`)).sort()).toEqual([
    `/api/v1/instances/search?process=101&q=${q}`,
    `/api/v1/instances/search?process=102&q=${q}`,
    `/api/v1/instances/search?process=103&q=${q}`,
  ]);
  expect(calls).toContain("/api/v1/instances/search?process=103&q=32");

  // Picking the other hit reads it whole once; picking it again costs nothing.
  const other = await page.evaluate(() => window.__lookup("order", { query: "Test=Versuch 2", instanceKey: 12 }));
  expect(other.values.amount.value).toBe("5");
  const n = await page.evaluate(() => window.__calls.length);
  await page.evaluate(() => window.__lookup("order", { query: "Test=Versuch 2", instanceKey: 12 }));
  expect(await page.evaluate(() => window.__calls.length)).toBe(n);
});

test("an instance-key search answers whole, with no second read", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => {
    window.__db.search = (def, q) => def === 102 && q === "21"
      ? { items: [{ key: 21, processDefKey: 102, version: 2, state: "active", createdAt: 100, variables: [{ name: "a", value: "1", kind: "number" }, { name: "b", value: "x", kind: "string" }] }] }
      : { items: [] };
  });
  const got = await page.evaluate(() => window.__lookup("order", { query: "21" }));
  expect(got.instance).toEqual({ key: 21, state: "active", version: 2, createdAt: 100 });
  expect(Object.keys(got.values)).toEqual(["a", "b"]);
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.filter((c) => c.startsWith("/api/v1/instances/search"))).toHaveLength(3);
});

test("a search that finds nothing says so, and skips what only the archive holds", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => {
    window.__db.search = (def) => def === 103
      ? { items: [{ key: 5, processDefKey: 103, version: 3, state: "completed", archived: true, variables: [] }] }
      : { items: [] };
  });
  const got = await page.evaluate(() => window.__lookup("order", { query: "customer=Nobody" }));
  expect(got).toEqual({ instance: null, values: {}, instances: [], query: "customer=Nobody", truncated: false });
});

test("a search cut short by the server or by the picker says so", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => {
    window.__db.search = (def, q) => {
      // The point read of whichever hit is picked.
      if (/^\d+$/.test(q)) return { items: [{ key: Number(q), processDefKey: def, version: 1, state: "completed", variables: [] }] };
      if (q === "cut" && def === 101) return { items: [{ key: 1, processDefKey: 101, version: 1, state: "completed", createdAt: 1, variables: [{ name: "x", value: "cut", kind: "string" }] }], truncated: true };
      if (q === "many" && def === 103) {
        return { items: Array.from({ length: 60 }, (_, i) => ({ key: 1000 + i, processDefKey: 103, version: 3, state: "completed", createdAt: i, variables: [{ name: "x", value: "many", kind: "string" }] })) };
      }
      return { items: [] };
    };
  });
  expect((await page.evaluate(() => window.__lookup("order", { query: "cut" }))).truncated).toBe(true);
  const many = await page.evaluate(() => window.__lookup("order", { query: "many" }));
  expect(many.truncated).toBe(true);
  expect(many.instances).toHaveLength(await page.evaluate(() => window.__max));
  // The newest survive the cut.
  expect(many.instances[0].key).toBe(1059);
});

test("a failed search is reported, not read as no match — and is asked again", async ({ page }) => {
  await seed(page);
  await page.evaluate(() => { window.__fail = ["/api/v1/instances/search?process=102"]; });
  await expect(page.evaluate(() => window.__lookup("order", { query: "x" }))).rejects.toThrow(/boom/);
  await page.evaluate(() => { window.__fail = []; });
  const got = await page.evaluate(() => window.__lookup("order", { query: "x" }));
  expect(got.instance).toBeNull();
});

test("sampleFor reads a dotted name as a path into a JSON variable", async ({ page }) => {
  const read = (values, name) => page.evaluate(([vals, n]) => window.__sampleFor(vals, n), [values, name]);
  const values = {
    application: { value: '{"TENANT_ID":"T-1","LIMITS":{"max":3,"on":true},"LIST":[1],"NONE":null}', kind: "json" },
    "a.b": { value: "literal", kind: "string" },
    a: { value: '{"b":"nested"}', kind: "json" },
    plain: { value: "text", kind: "string" },
  };

  expect(await read(values, "plain")).toEqual({ value: "text", kind: "string" });
  expect(await read(values, "application.TENANT_ID")).toEqual({ value: "T-1", kind: "string" });
  expect(await read(values, "application.LIMITS.max")).toEqual({ value: "3", kind: "number" });
  expect(await read(values, "application.LIMITS.on")).toEqual({ value: "true", kind: "boolean" });
  expect(await read(values, "application.LIMITS")).toEqual({ value: '{"max":3,"on":true}', kind: "json" });
  expect(await read(values, "application.NONE")).toEqual({ value: "null", kind: "null" });
  // A variable whose own name holds the dot is what that name means.
  expect(await read(values, "a.b")).toEqual({ value: "literal", kind: "string" });
  // Nothing rather than something wrong: a missing member, an index into a list, a
  // path through a value that is not an object, a name nobody holds.
  expect(await read(values, "application.MISSING")).toBeNull();
  expect(await read(values, "application.LIST.0")).toBeNull();
  expect(await read(values, "plain.x")).toBeNull();
  expect(await read(values, "nobody")).toBeNull();
  expect(await read(null, "plain")).toBeNull();
});
