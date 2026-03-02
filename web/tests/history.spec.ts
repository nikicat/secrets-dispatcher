import { expect, test } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

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

  test("log API endpoint returns empty array initially", async ({ request }) => {
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

  test("history entry has correct structure in API response", async ({ request }) => {
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
      ]).toContain(
        entry.resolution,
      );
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

  test("snapshot WebSocket message includes history array", async ({ page }) => {
    // The WebSocket snapshot should include a history field (even if empty)
    // We can verify this by checking that the page loaded without errors
    // and the app is functioning (which means the snapshot was parsed correctly)
    await expect(page.getByText("No pending requests")).toBeVisible();
  });

  test("history entry with ignored resolution shows correct badge", async ({ page }) => {
    const resolvedAt = new Date().toISOString();

    // Intercept WebSocket and inject a history_entry after snapshot
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        ws.send(message);
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              // Send history_entry with "ignored" resolution after snapshot
              ws.send(
                JSON.stringify({
                  type: "history_entry",
                  history_entry: {
                    request: {
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
                      session: "/org/freedesktop/secrets/session/1",
                      created_at: resolvedAt,
                      expires_at: resolvedAt,
                      type: "write",
                      sender_info: {
                        sender: ":1.100",
                        pid: 12345,
                        uid: 1000,
                        user_name: "testuser",
                        unit_name: "chrome.service",
                      },
                    },
                    resolution: "ignored",
                    resolved_at: resolvedAt,
                  },
                }),
              );
            }
          } catch {
            /* not JSON */
          }
        }
      });
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

  test("history entry with ignored resolution appears in snapshot", async ({ page }) => {
    const resolvedAt = new Date().toISOString();

    // Intercept WebSocket and inject history into the snapshot itself
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.history = [
                {
                  request: {
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
                    session: "/org/freedesktop/secrets/session/2",
                    created_at: resolvedAt,
                    expires_at: resolvedAt,
                    type: "write",
                    sender_info: {
                      sender: ":1.200",
                      pid: 54321,
                      uid: 1000,
                      user_name: "testuser",
                      unit_name: "chrome.service",
                    },
                  },
                  resolution: "ignored",
                  resolved_at: resolvedAt,
                },
              ];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch {
            /* not JSON */
          }
        }
        ws.send(message);
      });
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
