import { test, expect } from "@playwright/test";
import { startTestBackend } from "./fixtures/test-utils.mts";

// These tests verify the version mismatch auto-reload functionality.
// Each test uses its own backend instance to allow version changes.

test.describe("Version Mismatch Auto-Reload", () => {
  test("auto-reloads on version mismatch after server restart", async ({
    page,
  }) => {
    // Start backend with a specific version
    const backend = await startTestBackend({ version: "version_aaa1" });

    try {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);

      // Wait for WebSocket connection
      await expect(page.getByText("No pending requests")).toBeVisible();

      // Set up load event listener before triggering the restart
      const reloadPromise = page.waitForEvent("load", { timeout: 15000 });

      // Restart backend with different version to simulate server upgrade
      await backend.restart({ version: "version_bbb2" });

      // Wait for the page to automatically reload
      // The WebSocket should reconnect, detect version mismatch, and trigger reload
      await reloadPromise;

      // After reload, page should still work
      await expect(page.getByText("No pending requests")).toBeVisible();
    } finally {
      await backend.cleanup();
    }
  });

  test("no reload when version matches after server restart", async ({
    page,
  }) => {
    // Start backend with a specific version
    const backend = await startTestBackend({ version: "version_same" });

    try {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);

      // Wait for WebSocket connection
      await expect(page.getByText("No pending requests")).toBeVisible();

      // Track page reload
      let reloadCount = 0;
      page.on("load", () => {
        reloadCount++;
      });

      // Restart backend with SAME version
      await backend.restart({ version: "version_same" });

      // Wait for WebSocket to reconnect.
      // Don't assert transient "Reconnecting..." state — it may resolve
      // faster than Playwright can observe. Instead, wait for the end state.
      await expect(page.getByText("No pending requests")).toBeVisible({ timeout: 15000 });

      // Give extra time to ensure no delayed reload happens
      await page.waitForTimeout(2000);
      expect(reloadCount).toBe(0);
    } finally {
      await backend.cleanup();
    }
  });

  test("reloads when version changes from empty to non-empty", async ({
    page,
  }) => {
    // Start backend without a version (empty string)
    const backend = await startTestBackend();

    try {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);

      // Wait for WebSocket connection
      await expect(page.getByText("No pending requests")).toBeVisible();

      // Set up load event listener before triggering the restart
      const reloadPromise = page.waitForEvent("load", { timeout: 15000 });

      // Restart backend WITH a version now
      // Since the initial version was empty, this IS a version change
      await backend.restart({ version: "new_version_1" });

      // Wait for the page to automatically reload
      await reloadPromise;

      // After reload, page should still work
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
          (msg as { type: string }).type === "snapshot"
      ) as { type: string; version?: string } | undefined;

      expect(snapshotMsg).toBeDefined();
      expect(snapshotMsg?.version).toBe("test_version");
    } finally {
      await backend.cleanup();
    }
  });
});
