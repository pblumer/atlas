// End-to-end coverage for the Console → Backup view (api/web/app.js,
// viewConsoleBackup, ADR-0107). Backup and restore call the real Go API, which the
// static e2e harness does not run, so these drive the REAL app shell against a
// mocked /api/v1 surface — verifying the UI wiring (the nav entry, the download
// link, and the restore flow: validation, confirm, success reporting), not the
// server side, which the Go suite covers.
import { test, expect } from "@playwright/test";

// installMock answers /auth/me as single-user so the app never gates, fulfils
// POST /restore with a configurable summary, and returns a benign [] for every
// other /api/v1 call the shell makes on boot.
function installMock(page, { restore } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (path.endsWith("/restore")) {
      return route.fulfill(restore || { json: { restored: 0, restartRequired: true } });
    }
    return route.fulfill({ json: [] });
  });
}

// bootApp loads the real shell and waits until the first route() has rendered.
async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
}

const gotoBackup = async (page) => {
  await page.evaluate(() => { location.hash = "#/console/backup"; });
  await expect(page.locator("#view")).toContainText("Backup & restore");
};

test("the Console nav exposes a Backup entry that opens the backup view", async ({ page }) => {
  installMock(page);
  await bootApp(page);

  const backupNav = page.locator("#topnav a", { hasText: "Backup" });
  await expect(backupNav).toHaveAttribute("href", "#/console/backup");
  await backupNav.click();
  await expect(page.locator("#view h1")).toHaveText("Backup & restore");
});

test("the backup view offers a gzip download link and restore controls", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoBackup(page);

  // The download is a plain same-origin anchor so the session cookie authenticates it.
  const download = page.locator('#view a[href="/api/v1/backup"]');
  await expect(download).toBeVisible();
  await expect(download).toHaveAttribute("download", "");
  await expect(download).toContainText("Download");

  await expect(page.locator("#restore-file")).toBeVisible();
  await expect(page.locator("#restore-btn")).toBeVisible();
});

test("restoring without choosing a file is rejected before any upload", async ({ page }) => {
  installMock(page);
  await bootApp(page);
  await gotoBackup(page);

  await page.locator("#restore-btn").click();
  await expect(page.locator("#toast")).toContainText("Choose a backup file first");
  // The handler returns before touching the status line, so no upload was attempted.
  await expect(page.locator("#restore-status")).toBeHidden();
});

test("a successful restore reports the file count and the restart note", async ({ page }) => {
  installMock(page, { restore: { json: { restored: 3, restartRequired: true } } });
  await bootApp(page);
  await gotoBackup(page);

  // The confirm() guard must be accepted for the upload to proceed.
  page.on("dialog", (d) => d.accept());
  await page.locator("#restore-file").setInputFiles({
    name: "atlas-backup.tar.gz",
    mimeType: "application/gzip",
    buffer: Buffer.from("dummy archive bytes"),
  });
  await page.locator("#restore-btn").click();

  const status = page.locator("#restore-status");
  await expect(status).toContainText("Restored 3 file(s)");
  await expect(status).toContainText("Restart");
  await expect(page.locator("#toast")).toContainText("Restore complete");
});
