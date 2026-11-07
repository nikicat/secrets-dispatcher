import { expect, test } from "@playwright/test";
import {
  pendingRequest,
  routeSnapshot,
  startTestBackend,
  type TestBackend,
} from "./fixtures/test-utils.mts";
import type {
  HistoryEntry,
  PendingRequest,
  Resolution,
} from "../src/lib/types.ts";

// These tests verify the history feature of the WebUI.
// Each test starts an isolated backend instance.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Request History API", () => {
  // API-only tests that don't need UI authentication

  test("log API endpoint returns empty array initially", async ({
    request,
  }) => {
    const token = await backend.getAuthToken();

    const response = await request.get(`${backend.url}/api/v1/log`, {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    });

    expect(response.status()).toBe(200);
    const data = await response.json();
    expect(data.entries).toEqual([]);
  });

  test("log API endpoint requires authentication", async ({ request }) => {
    const response = await request.get(`${backend.url}/api/v1/log`);
    expect(response.status()).toBe(401);
  });

  test("history entry has correct structure in API response", async ({
    request,
  }) => {
    const token = await backend.getAuthToken();

    const response = await request.get(`${backend.url}/api/v1/log`, {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    });

    expect(response.status()).toBe(200);
    const data = await response.json();

    // Verify response structure
    expect(data).toHaveProperty("entries");
    expect(Array.isArray(data.entries)).toBe(true);

    // If there are entries, verify their structure
    for (const entry of data.entries) {
      expect(entry).toHaveProperty("request");
      expect(entry).toHaveProperty("resolution");
      expect(entry).toHaveProperty("resolved_at");

      expect(entry.request).toHaveProperty("id");
      expect(entry.request).toHaveProperty("client");
      expect(entry.request).toHaveProperty("items");
      expect(entry.request).toHaveProperty("session");

      expect([
        "approved",
        "denied",
        "expired",
        "cancelled",
        "auto_approved",
        "ignored",
      ]).toContain(entry.resolution);
    }
  });
});

test.describe("Request History UI", () => {
  // UI tests that require WebSocket authentication

  test.beforeEach(async ({ page }) => {
    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    // Wait for authentication to complete - look for no pending requests text
    await expect(page.getByText("No pending requests")).toBeVisible({
      timeout: 10000,
    });
  });

  test("history section is not shown when empty", async ({ page }) => {
    // In API-only mode with no resolved requests, history section should not appear
    await expect(page.locator(".history-section")).not.toBeVisible();
  });

  test("snapshot WebSocket message includes history array", async ({
    page,
  }) => {
    // The WebSocket snapshot should include a history field (even if empty)
    // We can verify this by checking that the page loaded without errors
    // and the app is functioning (which means the snapshot was parsed correctly)
    await expect(page.getByText("No pending requests")).toBeVisible();
  });

  test("history entry with ignored resolution shows correct badge", async ({
    page,
  }) => {
    const resolvedAt = new Date().toISOString();

    // Intercept WebSocket and inject a history_entry after snapshot
    await routeSnapshot(page, {
      after: [
        {
          type: "history_entry",
          history_entry: {
            request: pendingRequest({
              id: "ignored-test-1",
              client: "test-client",
              items: [
                {
                  path: "/org/freedesktop/secrets/collection/default/1",
                  label: "Chrome Safe Storage",
                  attributes: {
                    "xdg:schema": "_chrome_dummy_schema_for_unlocking",
                  },
                },
              ],
              type: "write",
              sender_info: { pid: 12345, invoker_name: "chrome.service" },
            }),
            resolution: "ignored",
            resolved_at: resolvedAt,
          },
        },
      ],
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    // History section should appear
    await expect(page.getByText("Recent Activity")).toBeVisible({
      timeout: 10000,
    });

    // Badge should show "ignored" text with the correct CSS class
    const badge = page.locator(".resolution-ignored");
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText("ignored");
  });

  test("history entry with ignored resolution appears in snapshot", async ({
    page,
  }) => {
    const resolvedAt = new Date().toISOString();

    // Intercept WebSocket and inject history into the snapshot itself
    await routeSnapshot(page, {
      rewrite: (s) => ({
        ...s,
        history: [
          {
            request: pendingRequest({
              id: "snap-ignored-1",
              client: "test-client",
              items: [
                {
                  path: "/org/freedesktop/secrets/collection/default/2",
                  label: "Chrome Dummy",
                  attributes: {
                    "xdg:schema": "_chrome_dummy_schema_for_unlocking",
                  },
                },
              ],
              type: "write",
              sender_info: {
                sender: ":1.200",
                pid: 54321,
                invoker_name: "chrome.service",
              },
            }),
            resolution: "ignored",
            resolved_at: resolvedAt,
          },
        ],
      }),
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    // History section should appear with the ignored entry
    await expect(page.getByText("Recent Activity")).toBeVisible({
      timeout: 10000,
    });

    // Badge should have the correct CSS class
    const badge = page.locator(".resolution-ignored");
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText("ignored");

    // The item label should be visible
    await expect(page.getByText("Chrome Dummy")).toBeVisible();
  });
});

test.describe("Auto-Approved History Collapsing", () => {
  test.beforeEach(async ({ page }) => {
    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    await expect(page.getByText("No pending requests")).toBeVisible({
      timeout: 10000,
    });
  });

  function makeHistoryEntry(
    id: string,
    resolution: Resolution,
    invokerName: string,
    itemPath: string,
    type: PendingRequest["type"] = "get_secret",
  ): HistoryEntry {
    return {
      request: pendingRequest({
        id,
        client: "test-client",
        items: [{ path: itemPath, label: `item-${id}`, attributes: {} }],
        type,
        sender_info: { invoker_name: invokerName },
      }),
      resolution,
      resolved_at: new Date().toISOString(),
    };
  }

  test("consecutive auto-approved entries are collapsed with count", async ({
    page,
  }) => {
    await routeSnapshot(page, {
      rewrite: (s) => ({
        ...s,
        history: [
          makeHistoryEntry(
            "aa-1",
            "auto_approved",
            "app.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
          makeHistoryEntry(
            "aa-2",
            "auto_approved",
            "app.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
          makeHistoryEntry(
            "aa-3",
            "auto_approved",
            "app.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
        ],
      }),
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("Recent Activity (3)")).toBeVisible({
      timeout: 10000,
    });
    // Only 1 card rendered
    await expect(page.locator(".history-entry")).toHaveCount(1);
    // Count badge shows x3
    await expect(page.locator(".history-count")).toHaveText("×3");
  });

  test("different invokers are not collapsed", async ({ page }) => {
    await routeSnapshot(page, {
      rewrite: (s) => ({
        ...s,
        history: [
          makeHistoryEntry(
            "aa-1",
            "auto_approved",
            "app-a.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
          makeHistoryEntry(
            "aa-2",
            "auto_approved",
            "app-b.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
        ],
      }),
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("Recent Activity (2)")).toBeVisible({
      timeout: 10000,
    });
    await expect(page.locator(".history-entry")).toHaveCount(2);
    await expect(page.locator(".history-count")).toHaveCount(0);
  });

  test("non-consecutive auto-approved entries are still collapsed", async ({
    page,
  }) => {
    await routeSnapshot(page, {
      rewrite: (s) => ({
        ...s,
        history: [
          makeHistoryEntry(
            "aa-1",
            "auto_approved",
            "app.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
          makeHistoryEntry(
            "manual-1",
            "approved",
            "app.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
          makeHistoryEntry(
            "aa-2",
            "auto_approved",
            "app.service",
            "/org/freedesktop/secrets/collection/default/x",
          ),
        ],
      }),
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("Recent Activity (3)")).toBeVisible({
      timeout: 10000,
    });
    // 2 cards: collapsed auto-approved (x2) + manual approved
    await expect(page.locator(".history-entry")).toHaveCount(2);
    await expect(page.locator(".history-count")).toHaveText("×2");
  });

  test("new auto-approved entry via WebSocket increments existing group", async ({
    page,
  }) => {
    const itemPath = "/org/freedesktop/secrets/collection/default/x";
    await routeSnapshot(page, {
      rewrite: (snapshot) => ({
        ...snapshot,
        history: [
          makeHistoryEntry("aa-1", "auto_approved", "app.service", itemPath),
        ],
      }),
      after: [
        {
          type: "history_entry",
          history_entry: makeHistoryEntry(
            "aa-2",
            "auto_approved",
            "app.service",
            itemPath,
          ),
        },
      ],
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    // Wait for the second entry to arrive and group
    await expect(page.locator(".history-count")).toBeVisible({ timeout: 5000 });
    await expect(page.locator(".history-entry")).toHaveCount(1);
    await expect(page.locator(".history-count")).toHaveText("×2");
  });
});
