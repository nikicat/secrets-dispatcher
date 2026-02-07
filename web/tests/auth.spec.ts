import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";
import { test, expect } from "@playwright/test";
import { createHmac } from "node:crypto";

// These tests verify the authentication flow of the WebUI.
// Each test starts an isolated backend instance.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Authentication", () => {
  test.beforeEach(async ({ page }) => {
    // Set base URL for this test's backend
    await page.goto(backend.url);
  });

  test("shows login prompt when not authenticated", async ({ page }) => {
    // Should show the login prompt
    await expect(page.getByText("Authentication Required")).toBeVisible();
    await expect(page.getByText("secrets-dispatcher login")).toBeVisible();
  });

  test("login with valid JWT sets cookie and shows connected status", async ({
    page,
  }) => {
    // Generate a valid login URL
    const loginURL = await backend.generateLoginURL();

    // Navigate to the login URL
    await page.goto(loginURL);

    // Should redirect to / without token in URL
    await expect(page).toHaveURL(backend.url + "/");

    // Should show connected status
    await expect(page.getByText("Connected")).toBeVisible();

    // Should show empty state (no pending requests in API-only mode)
    await expect(page.getByText("No pending requests")).toBeVisible();
  });

  test("login with invalid JWT shows error", async ({ page }) => {
    // Navigate to URL with invalid token
    await page.goto(`${backend.url}/?token=invalid-token`);

    // Should redirect to / and show error
    await expect(page).toHaveURL(backend.url + "/");
    await expect(page.getByText("Invalid or expired login link")).toBeVisible();
  });

  test("session persists after page reload when authenticated", async ({
    page,
  }) => {
    // First, authenticate
    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    // Verify authenticated
    await expect(page.getByText("Connected")).toBeVisible();

    // Reload the page
    await page.reload();

    // Should still show connected status (cookie persists)
    await expect(page.getByText("Connected")).toBeVisible();
  });

  test("API calls return 401 when not authenticated", async ({ request }) => {
    // Try to access the API without authentication
    const response = await request.get(`${backend.url}/api/v1/status`);

    expect(response.status()).toBe(401);
  });

  test("auth endpoint rejects invalid JWT", async ({ request }) => {
    const response = await request.post(`${backend.url}/api/v1/auth`, {
      data: { token: "invalid-jwt-token" },
    });

    expect(response.status()).toBe(401);
  });

  test("auth endpoint rejects missing token", async ({ request }) => {
    const response = await request.post(`${backend.url}/api/v1/auth`, {
      data: {},
    });

    expect(response.status()).toBe(400);
  });

  test("auth endpoint accepts valid JWT and sets cookie", async ({
    request,
  }) => {
    // Generate a valid JWT
    const token = await backend.getAuthToken();

    // Create JWT manually for testing
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

    const response = await request.post(`${backend.url}/api/v1/auth`, {
      data: { token: jwt },
    });

    expect(response.status()).toBe(200);

    // Check that session cookie was set
    const cookies = response.headers()["set-cookie"];
    expect(cookies).toContain("session=");
  });
});
