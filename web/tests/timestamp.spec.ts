import { test, expect } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

// These tests verify the timestamp display toggle feature in the WebUI.
// Users can click on timestamps to toggle between relative ("5 minutes ago")
// and absolute ("14:32:15") display formats.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Timestamp Display Toggle", () => {
  test.beforeEach(async ({ page }) => {
    // Clear localStorage before each test
    await page.goto(backend.url);
    await page.evaluate(() => localStorage.clear());

    // Inject test history entry via API with unique ID
    const token = await backend.getAuthToken();
    const uniqueId = `test-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    await page.request.post(`${backend.url}/api/v1/test/history`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {
        request: {
          id: uniqueId,
          client: "test-client",
          items: [{ path: "/test/item", label: "Test Secret", attributes: {} }],
          session: "/session/1",
          created_at: new Date().toISOString(),
          expires_at: new Date(Date.now() + 300000).toISOString(),
          type: "get_secret",
          sender_info: { sender: "", pid: 0, uid: 0, user_name: "", unit_name: "" },
        },
        resolution: "approved",
        resolved_at: new Date().toISOString(),
      },
    });

    // Login and wait for history to appear
    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    await expect(page.locator(".history-entry").first()).toBeVisible({ timeout: 10000 });
  });

  test("shows relative time by default", async ({ page }) => {
    const timestamp = page.locator(".history-time").first();
    await expect(timestamp).toContainText(/just now|minute|hour|day/);
  });

  test("click toggles to absolute time", async ({ page }) => {
    const timestamp = page.locator(".history-time").first();

    // Verify initial relative format
    await expect(timestamp).toContainText(/just now|minute|hour|day/);

    // Click to toggle
    await timestamp.click();

    // Should now show time format (HH:MM:SS or locale-specific format)
    await expect(timestamp).toHaveText(/\d{1,2}:\d{2}:\d{2}/);
  });

  test("click again toggles back to relative time", async ({ page }) => {
    const timestamp = page.locator(".history-time").first();

    // Toggle to absolute
    await timestamp.click();
    await expect(timestamp).toHaveText(/\d{1,2}:\d{2}:\d{2}/);

    // Toggle back to relative
    await timestamp.click();
    await expect(timestamp).toContainText(/just now|minute|hour|day/);
  });

  test("preference persists after page reload", async ({ page }) => {
    const timestamp = page.locator(".history-time").first();

    // Toggle to absolute
    await timestamp.click();
    await expect(timestamp).toHaveText(/\d{1,2}:\d{2}:\d{2}/);

    // Reload page
    await page.reload();
    await expect(page.locator(".history-entry").first()).toBeVisible({ timeout: 10000 });

    // Should still show absolute time
    const reloadedTimestamp = page.locator(".history-time").first();
    await expect(reloadedTimestamp).toHaveText(/\d{1,2}:\d{2}:\d{2}/);
  });

  test("preference stored in localStorage", async ({ page }) => {
    // Initially relative (default)
    let format = await page.evaluate(() => localStorage.getItem("timeFormat"));
    expect(format).toBeNull();

    // Toggle to absolute
    await page.locator(".history-time").first().click();

    // Check localStorage
    format = await page.evaluate(() => localStorage.getItem("timeFormat"));
    expect(format).toBe("absolute");

    // Toggle back
    await page.locator(".history-time").first().click();
    format = await page.evaluate(() => localStorage.getItem("timeFormat"));
    expect(format).toBe("relative");
  });
});
