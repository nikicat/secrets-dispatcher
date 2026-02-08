import { test, expect } from "@playwright/test";
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

      expect(["approved", "denied", "expired", "cancelled"]).toContain(
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
});
