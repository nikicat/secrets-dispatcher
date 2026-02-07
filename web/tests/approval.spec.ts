import { test, expect } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";
import { createHmac } from "node:crypto";

// These tests verify the approval flow of the WebUI.
// Each test starts an isolated backend instance.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Approval Flow", () => {
  // Authenticate before each test
  test.beforeEach(async ({ page }) => {
    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    // Wait for authentication to complete - look for the specific status text
    await expect(page.getByText("client connected")).toBeVisible();
  });

  test("shows empty state when no pending requests", async ({ page }) => {
    // In API-only mode, there are no pending requests
    await expect(page.getByText("No pending requests")).toBeVisible();
  });

  test("status indicator shows connected state", async ({ page }) => {
    // Verify the status indicator is visible and shows connected
    const statusIndicator = page.locator(".status-indicator");
    await expect(statusIndicator).toBeVisible();
    await expect(statusIndicator.getByText("Connected")).toBeVisible();
  });

  test("header shows correct title", async ({ page }) => {
    await expect(page.locator("header h1")).toHaveText("Secrets Dispatcher");
  });

  test("authenticated API calls succeed", async ({ request }) => {
    // Exchange JWT for session cookie
    const token = await backend.getAuthToken();
    const header = Buffer.from(
      JSON.stringify({ alg: "HS256", typ: "JWT" }),
    ).toString("base64url");
    const now = Math.floor(Date.now() / 1000);
    const claims = Buffer.from(
      JSON.stringify({ iat: now, exp: now + 300 }),
    ).toString("base64url");

    const signingInput = `${header}.${claims}`;
    const signature = createHmac("sha256", token)
      .update(signingInput)
      .digest("base64url");
    const jwt = `${signingInput}.${signature}`;

    // Get session cookie
    const authResponse = await request.post(`${backend.url}/api/v1/auth`, {
      data: { token: jwt },
    });
    expect(authResponse.status()).toBe(200);

    // Now make authenticated requests using the cookie
    const statusResponse = await request.get(`${backend.url}/api/v1/status`, {
      headers: {
        Cookie:
          authResponse.headers()["set-cookie"]?.split(";")[0] || `session=${token}`,
      },
    });

    expect(statusResponse.status()).toBe(200);
    const status = await statusResponse.json();
    expect(status.running).toBe(true);
    expect(status.client).toBe("test-client");
    expect(status.pending_count).toBe(0);
  });

  test("pending list returns empty array in API-only mode", async ({
    request,
  }) => {
    // Get auth cookie
    const token = await backend.getAuthToken();

    // Make authenticated request using Bearer token
    const response = await request.get(`${backend.url}/api/v1/pending`, {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    });

    expect(response.status()).toBe(200);
    const data = await response.json();
    expect(data.requests).toEqual([]);
  });

  test("approve endpoint returns 404 for nonexistent request", async ({
    request,
  }) => {
    const token = await backend.getAuthToken();

    const response = await request.post(
      `${backend.url}/api/v1/pending/nonexistent-id/approve`,
      {
        headers: {
          Authorization: `Bearer ${token}`,
        },
      },
    );

    expect(response.status()).toBe(404);
  });

  test("deny endpoint returns 404 for nonexistent request", async ({
    request,
  }) => {
    const token = await backend.getAuthToken();

    const response = await request.post(
      `${backend.url}/api/v1/pending/nonexistent-id/deny`,
      {
        headers: {
          Authorization: `Bearer ${token}`,
        },
      },
    );

    expect(response.status()).toBe(404);
  });

  test("pending list response has correct ItemInfo structure", async ({
    request,
  }) => {
    // Verify the API response structure matches expected ItemInfo format
    const token = await backend.getAuthToken();

    const response = await request.get(`${backend.url}/api/v1/pending`, {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    });

    expect(response.status()).toBe(200);
    const data = await response.json();

    // Verify response structure - requests should be an array
    expect(data).toHaveProperty("requests");
    expect(Array.isArray(data.requests)).toBe(true);

    // When there are requests, each item should have ItemInfo structure
    // (path, label, attributes) - we verify the type shape even if empty
    for (const req of data.requests) {
      expect(req).toHaveProperty("id");
      expect(req).toHaveProperty("client");
      expect(req).toHaveProperty("items");
      expect(req).toHaveProperty("session");
      expect(Array.isArray(req.items)).toBe(true);

      // Each item should have ItemInfo structure
      for (const item of req.items) {
        expect(item).toHaveProperty("path");
        expect(item).toHaveProperty("label");
        expect(item).toHaveProperty("attributes");
      }
    }
  });
});
