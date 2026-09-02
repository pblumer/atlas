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

// The three database products share a form and share nothing else about a connection
// string: SQL Server takes a sqlserver:// URL, PostgreSQL a postgres:// one, and
// MariaDB a form that is not a URL at all. The form showed a PostgreSQL example for
// all three, so an operator configuring SQL Server was being shown the wrong syntax by
// the only thing on screen that says what is expected — and the driver's rejection
// arrives later and says nothing about which of the six parts is wrong.
test("each database product shows its own connection-string example", async ({ page }) => {
  const shape = (kind) => page.evaluate((k) => window.shape(k), kind);

  const mssql = await shape("mssql");
  expect(mssql.sql, "SQL Server is a database kind").toBe(true);
  expect(mssql.dsnPlaceholder, "SQL Server takes a sqlserver:// URL").toMatch(/^sqlserver:\/\//);
  expect(mssql.dsnPlaceholder).toContain("1433");

  const mariadb = await shape("mariadb");
  // Not a URL: the MySQL driver's own form. Showing it a scheme would be showing it
  // something the driver refuses.
  expect(mariadb.dsnPlaceholder, "MariaDB's DSN is not a URL").not.toMatch(/^[a-z]+:\/\//);
  expect(mariadb.dsnPlaceholder).toContain("tcp(");
  expect(mariadb.dsnPlaceholder).toContain("3306");

  const postgres = await shape("postgres");
  expect(postgres.dsnPlaceholder).toMatch(/^postgres(ql)?:\/\//);
  expect(postgres.dsnPlaceholder).toContain("5432");

  // No two products may show the same example — that is exactly the bug.
  const seen = new Set([mssql.dsnPlaceholder, mariadb.dsnPlaceholder, postgres.dsnPlaceholder]);
  expect(seen.size, "each product needs its own example").toBe(3);

  // The credential reference names a vault key holding the whole connection string,
  // so its example must not read like a password either.
  for (const kind of ["mssql", "mariadb", "postgres"]) {
    const sh = await shape(kind);
    expect(sh.credRefPlaceholder, `${kind} credential reference example`).toContain(kind);
  }
});

// A kind that is not a database has no connection string to show an example for.
test("a non-database kind offers no connection-string example", async ({ page }) => {
  const sh = await page.evaluate(() => window.shape("jira"));
  expect(sh.sql).toBe(false);
  expect(sh.dsnPlaceholder || "").toBe("");
});
