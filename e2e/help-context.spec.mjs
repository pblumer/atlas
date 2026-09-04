// The "?" menu's first entry is help *for the view you are looking at*: app.js's
// handbookHelp() maps a route to a section id in handbuch.html, and setHelpContext
// builds `/handbuch.html#<anchor>` out of it.
//
// The two files know nothing about each other. Renaming a section, or adding a
// route and pointing it at a chapter that was never written, breaks the link in
// the one way nobody notices: the page still loads, it just lands at the top and
// the reader concludes the help is useless rather than that it is broken. So this
// checks the join itself — statically, from the two files, because the failure is
// a mismatch between them and not something a rendered page can show.
//
// It is deliberately not a list of expected pairs. A test that repeats the mapping
// is a third copy to keep in sync; what has to hold is that every anchor the code
// hands out is a section the handbook has.
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("every chapter the contextual help points at exists in the handbook", () => {
  const app = read("../api/web/app.js");
  const body = app.slice(app.indexOf("function handbookHelp("));
  const fn = body.slice(0, body.indexOf("\n}\n") + 3);

  // H("anchor", "label") is the one shape handbookHelp returns. The capture is
  // deliberately [^"]+ and not a charset of what an id looks like: a narrow
  // pattern silently skips exactly the malformed anchor this test is for.
  const anchors = [...fn.matchAll(/H\("([^"]+)",/g)].map((m) => m[1]);
  // A guard on the guard: if the helper is ever renamed this test must fail loudly
  // rather than pass on an empty list.
  expect(anchors.length).toBeGreaterThan(10);

  const handbook = read("../api/web/handbuch.html");
  const sections = new Set([...handbook.matchAll(/<section id="([^"]+)"/g)].map((m) => m[1]));
  expect([...new Set(anchors)].filter((a) => !sections.has(a))).toEqual([]);
});

test("the apps the shell offers are the apps the handbook teaches", () => {
  const app = read("../api/web/app.js");
  const table = app.slice(app.indexOf("const APPS = ["), app.indexOf("];", app.indexOf("const APPS = [")));
  const routes = [...table.matchAll(/route: "([^"]+)"/g)].map((m) => m[1]);
  expect(routes.length).toBe(6);

  // The welcome chapter's card grid is where a reader learns an app exists at all,
  // and it linked to four of six for as long as Panorama and Data went untaught.
  const handbook = read("../api/web/handbuch.html");
  const welcome = handbook.slice(handbook.indexOf('<section id="willkommen"'),
    handbook.indexOf("</section>", handbook.indexOf('<section id="willkommen"')));
  expect(routes.filter((r) => !welcome.includes(`href="/${r}"`))).toEqual([]);
});
