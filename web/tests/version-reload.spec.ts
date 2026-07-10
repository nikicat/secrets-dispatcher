import { expect, test } from "@playwright/test";
import { startTestBackend } from "./fixtures/test-utils.mts";

// These tests verify the version mismatch auto-reload functionality and the
// session behavior across daemon restarts.
//
// Since sessions became server-side and in-memory (single-use login JWT fix),
// a daemon restart revokes every browser session: the WebSocket reconnect hits
// 401 and the UI drops to the login prompt instead of silently reloading. The
// version-mismatch reload logic itself is exercised via WebSocket interception,
// which can change the snapshot version without restarting the backend.

test.describe("Sessions Across Restart", () => {
  test("restart revokes browser session; fresh login recovers", async ({ page }) => {
    const backend = await startTestBackend({ version: "version_aaa1" });

    try {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);
      await expect(page.getByText("No pending requests")).toBeVisible();

      let reloadCount = 0;
      page.on("load", () => {
        reloadCount++;
      });

      // Restart with a different version. The session set is in-memory, so
      // the page's session dies with the old process; the version bump is
      // invisible to the page because the WS can no longer authenticate.
      await backend.restart({ version: "version_bbb2" });

      await expect(page.getByText("Authentication Required")).toBeVisible({
        timeout: 15000,
      });
      expect(reloadCount).toBe(0);

      // A fresh login URL (new JWT, same master token) recovers the session.
      const freshLoginURL = await backend.generateLoginURL();
      await page.goto(freshLoginURL);
      await expect(page.getByText("No pending requests")).toBeVisible();
    } finally {
      await backend.cleanup();
    }
  });
});

test.describe("Version Mismatch Auto-Reload", () => {
  test("reloads on version change across reconnects, not on match", async ({ page }) => {
    const backend = await startTestBackend({ version: "real_version" });

    try {
      // Rewrite the snapshot version per WS connection:
      //   1st connect: "" (empty, like a dev build) — stored as the baseline
      //   2nd connect: ""  — same version, must NOT reload
      //   3rd connect: "version_new" — mismatch, must reload
      const versions = ["", "", "version_new"];
      let connectCount = 0;
      let currentRoute: import("@playwright/test").WebSocketRoute | null = null;

      await page.routeWebSocket("**/api/v1/ws", (ws) => {
        const version = versions[Math.min(connectCount, versions.length - 1)];
        connectCount++;
        currentRoute = ws;
        const server = ws.connectToServer();
        server.onMessage((message) => {
          if (typeof message === "string") {
            try {
              const parsed = JSON.parse(message);
              if (parsed.type === "snapshot") {
                parsed.version = version;
                ws.send(JSON.stringify(parsed));
                return;
              }
            } catch { /* not JSON */ }
          }
          ws.send(message);
        });
      });

      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);
      await expect(page.getByText("No pending requests")).toBeVisible();

      let reloadCount = 0;
      page.on("load", () => {
        reloadCount++;
      });

      // Force a reconnect with the SAME version — no reload expected.
      currentRoute!.close({ code: 1006, reason: "test-induced drop" });
      await expect(page.getByText("client connected")).toBeVisible({
        timeout: 15000,
      });
      await page.waitForTimeout(1000);
      expect(reloadCount).toBe(0);

      // Force a reconnect with a DIFFERENT version — the page must reload.
      const reloadPromise = page.waitForEvent("load", { timeout: 15000 });
      currentRoute!.close({ code: 1006, reason: "test-induced drop" });
      await reloadPromise;

      // After reload the session cookie is still valid (no restart happened),
      // so the page comes back up authenticated.
      await expect(page.getByText("No pending requests")).toBeVisible();
    } finally {
      await backend.cleanup();
    }
  });

  test("WebSocket snapshot includes version field", async ({ page }) => {
    const backend = await startTestBackend({ version: "test_version" });

    try {
      // Capture WebSocket messages
      const wsMessages: unknown[] = [];

      await page.exposeFunction("captureWSMessage", (data: string) => {
        try {
          wsMessages.push(JSON.parse(data));
        } catch {
          // Ignore non-JSON messages
        }
      });

      // Inject WebSocket interceptor before page loads
      await page.addInitScript(() => {
        const OriginalWebSocket = window.WebSocket;
        window.WebSocket = class extends OriginalWebSocket {
          constructor(url: string | URL, protocols?: string | string[]) {
            super(url, protocols);
            this.addEventListener("message", (event) => {
              // @ts-expect-error injected function
              window.captureWSMessage(event.data);
            });
          }
        };
      });

      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);

      // Wait for WebSocket connection and snapshot
      await expect(page.getByText("No pending requests")).toBeVisible();

      // Find the snapshot message
      const snapshotMsg = wsMessages.find(
        (msg: unknown) =>
          typeof msg === "object" &&
          msg !== null &&
          "type" in msg &&
          (msg as { type: string }).type === "snapshot",
      ) as { type: string; version?: string } | undefined;

      expect(snapshotMsg).toBeDefined();
      expect(snapshotMsg?.version).toBe("test_version");
    } finally {
      await backend.cleanup();
    }
  });
});
