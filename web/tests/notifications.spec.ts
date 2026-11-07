import { expect, type Page, test } from "@playwright/test";
import {
  pendingRequest,
  routeSnapshot,
  startTestBackend,
  type TestBackend,
} from "./fixtures/test-utils.mts";
import type { PendingRequest } from "../src/lib/types.ts";

declare global {
  interface Window {
    __notificationCalls: Array<{
      title: string;
      options: NotificationOptions;
    }>;
  }
}

// These tests verify the browser notification functionality.
// Due to Playwright limitations (headless mode doesn't support real notifications,
// WebKit doesn't work at all), we use a spy pattern to track Notification API calls.

/** The notification permission and page visibility the spy reports. */
interface SpyState {
  permission: NotificationPermission;
  hidden: boolean;
}

const HIDDEN_GRANTED: SpyState = { permission: "granted", hidden: true };

/**
 * Replaces `window.Notification` in every document the page loads with a spy.
 * The spy records each constructed notification in `window.__notificationCalls`
 * and reports `permission` as the permission state. When `hidden` is true,
 * `document.hidden` also reads true.
 */
async function installNotificationSpy(page: Page, spy: SpyState) {
  await page.addInitScript(({ permission, hidden }) => {
    window.__notificationCalls = [];

    const SpyNotification = function (
      this: Notification,
      title: string,
      options?: NotificationOptions,
    ) {
      window.__notificationCalls.push({ title, options: options ?? {} });
      return { onclick: null, close: () => {} } as unknown as Notification;
    } as unknown as typeof Notification;

    Object.defineProperty(SpyNotification, "permission", {
      get: () => permission,
      configurable: true,
    });
    SpyNotification.requestPermission = () => Promise.resolve(permission);

    Object.defineProperty(window, "Notification", {
      value: SpyNotification,
      writable: true,
      configurable: true,
    });

    if (hidden) {
      Object.defineProperty(document, "hidden", {
        get: () => true,
        configurable: true,
      });
    }
  }, spy);
}

let backend: TestBackend;

/**
 * Opens the app on `page` with the notification spy reporting `spy`, and with
 * `requests` pending. Resolves once the app shows them, by which time it has
 * decided whether to notify.
 */
async function openWithRequests(
  page: Page,
  spy: SpyState,
  requests: PendingRequest[],
) {
  await installNotificationSpy(page, spy);
  await routeSnapshot(page, {
    // With no delay, the app decides on each notification as the request arrives.
    rewrite: (s) => ({ ...s, notification_delay_ms: 0 }),
    after: requests.map((request) => ({ type: "request_created", request })),
  });
  await page.goto(await backend.generateLoginURL());
  await expect(page.locator(".card-header")).toHaveCount(requests.length);
}

function notificationCalls(page: Page) {
  return page.evaluate(() => window.__notificationCalls);
}

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Browser Notifications", () => {
  test("app loads successfully with notification permission denied", async ({
    page,
  }) => {
    await installNotificationSpy(page, { permission: "denied", hidden: false });

    await page.goto(await backend.generateLoginURL());

    // App should still work without notifications
    await expect(page.getByText("No pending requests")).toBeVisible();
  });

  test("a request that arrives while the page is hidden raises a notification", async ({
    page,
  }) => {
    await openWithRequests(page, HIDDEN_GRANTED, [
      pendingRequest({
        id: "test-123",
        client: "my-app",
        items: [{ path: "/secrets/api-key", label: "API Key", attributes: {} }],
        sender_info: { pid: 1234, invoker_name: "my-app.service" },
      }),
    ]);

    expect(await notificationCalls(page)).toMatchObject([
      {
        title: "Secret Request",
        options: {
          body: "Client: my-app\nProcess: my-app.service\nSecret: API Key",
          tag: "test-123",
          requireInteraction: true,
        },
      },
    ]);
  });

  test("no notification while the page is visible", async ({ page }) => {
    await openWithRequests(page, { permission: "granted", hidden: false }, [
      pendingRequest({ id: "test-visible" }),
    ]);

    expect(await notificationCalls(page)).toEqual([]);
  });

  test("no notification when permission is denied", async ({ page }) => {
    await openWithRequests(page, { permission: "denied", hidden: true }, [
      pendingRequest({ id: "test-denied" }),
    ]);

    expect(await notificationCalls(page)).toEqual([]);
  });

  test("notification body counts multiple items and falls back to the PID", async ({
    page,
  }) => {
    await openWithRequests(page, HIDDEN_GRANTED, [
      pendingRequest({
        id: "test-multi",
        client: "batch-app",
        items: [
          { path: "/secrets/key1", label: "Key 1", attributes: {} },
          { path: "/secrets/key2", label: "Key 2", attributes: {} },
          { path: "/secrets/key3", label: "Key 3", attributes: {} },
        ],
        sender_info: { pid: 5678, invoker_name: "" },
      }),
    ]);

    expect(await notificationCalls(page)).toMatchObject([
      { options: { body: "Client: batch-app\nPID: 5678\nSecrets: 3 items" } },
    ]);
  });

  test("notification body names the type of a search request", async ({
    page,
  }) => {
    await openWithRequests(page, HIDDEN_GRANTED, [
      pendingRequest({
        id: "test-search",
        client: "search-app",
        type: "search",
        items: [],
        search_attributes: { username: "admin" },
        sender_info: { pid: 4321, invoker_name: "" },
      }),
    ]);

    expect(await notificationCalls(page)).toMatchObject([
      { options: { body: "Client: search-app\nPID: 4321\nType: search" } },
    ]);
  });

  test.describe("with notification permission granted", () => {
    test.use({ permissions: ["notifications"] });

    test("app loads successfully with notification permission granted", async ({
      page,
    }) => {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);

      // App should work normally with notifications enabled
      await expect(page.getByText("No pending requests")).toBeVisible();
      await expect(page.getByText("client connected")).toBeVisible();
    });

    test("notification toggle button is visible and defaults to enabled", async ({
      page,
    }) => {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);
      await expect(page.getByText("No pending requests")).toBeVisible();

      const toggle = page.getByRole("button", { name: "Toggle notifications" });
      await expect(toggle).toBeVisible();
      await expect(toggle).toHaveAttribute("title", "Notifications enabled");
    });

    test("notification toggle disables and persists in localStorage", async ({
      page,
    }) => {
      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);
      await expect(page.getByText("No pending requests")).toBeVisible();

      const toggle = page.getByRole("button", { name: "Toggle notifications" });

      // Click to disable
      await toggle.click();
      await expect(toggle).toHaveAttribute("title", "Notifications disabled");

      // Verify localStorage was set
      const stored = await page.evaluate(() =>
        localStorage.getItem("notificationsEnabled"),
      );
      expect(stored).toBe("false");

      // Reload and verify it persists
      await page.reload();
      await expect(page.getByText("No pending requests")).toBeVisible();
      const toggleAfterReload = page.getByRole("button", {
        name: "Toggle notifications",
      });
      await expect(toggleAfterReload).toHaveAttribute(
        "title",
        "Notifications disabled",
      );

      // Click to re-enable
      await toggleAfterReload.click();
      await expect(toggleAfterReload).toHaveAttribute(
        "title",
        "Notifications enabled",
      );
      const storedAfter = await page.evaluate(() =>
        localStorage.getItem("notificationsEnabled"),
      );
      expect(storedAfter).toBe("true");
    });
  });
});
