// e2e for what the Console's "New connector" form actually posts
// (api/web/connectordialog.js, connectorCreateBody).
//
// The regression these pin: adding a jira connector was refused with
// "connectionString applies only to a SQL connector (postgres, mariadb, mssql)" —
// a message about databases, on a create that had nothing to do with one. The form
// keeps the connection-string field in the DOM for every kind and only hides it, and
// the old guard asked whether that field was non-empty rather than whether the kind
// was a SQL one. A DSN left from a kind picked earlier — or one a password manager
// filled into a type="password" input nobody can see — then rode along and got the
// create refused, with no field on screen to explain it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/connector-create-body-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const build = (page, values) =>
  page.evaluate((v) => { window.set(v); return window.build(); }, values);

test("a jira create carries no connection string, even with one left in the hidden field", async ({ page }) => {
  const body = await build(page, {
    kind: "jira",
    name: "jira-pb",
    endpoint: "https://blumer.atlassian.net/",
    credentialsRef: "jira_pb",
    connectionString: "postgresql://someone:secret@db.example.com:5432/postgres",
  });
  expect(body.kind).toBe("jira");
  expect(body.credentialsRef).toBe("jira_pb");
  expect(body).not.toHaveProperty("connectionString");
});

test("a SQL create still carries the connection string it was given", async ({ page }) => {
  const body = await build(page, {
    kind: "postgres",
    name: "supabase",
    connectionString: "postgresql://postgres.abc:pw@aws-0.pooler.supabase.com:5432/postgres",
  });
  expect(body.connectionString).toBe("postgresql://postgres.abc:pw@aws-0.pooler.supabase.com:5432/postgres");
});

// The whole field is optional even for SQL: an operator who already keeps the DSN in
// the vault names its key in the reference field instead, and the server refuses a
// record with neither.
test("a SQL create with no connection string omits the field rather than sending an empty one", async ({ page }) => {
  const body = await build(page, {
    kind: "postgres",
    name: "by-reference",
    credentialsRef: "pg_dsn",
    connectionString: "",
  });
  expect(body).not.toHaveProperty("connectionString");
  expect(body.credentialsRef).toBe("pg_dsn");
});

// Values are trimmed, and a mail create carries its provider — the two other things
// the builder is responsible for beyond the gate above.
test("a mail create carries its provider, and values arrive trimmed", async ({ page }) => {
  const body = await build(page, {
    kind: "mail",
    name: "  postbox  ",
    endpoint: " smtp.example.com:587 ",
    sender: " bot@example.com ",
    provider: "gmail",
  });
  expect(body.name).toBe("postbox");
  expect(body.endpoint).toBe("smtp.example.com:587");
  expect(body.sender).toBe("bot@example.com");
  expect(body.provider).toBe("gmail");
});
