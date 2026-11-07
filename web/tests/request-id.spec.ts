import { expect, test } from "@playwright/test";
import {
  pendingRequest,
  routeSnapshot,
  startTestBackend,
  type TestBackend,
} from "./fixtures/test-utils.mts";

// These tests verify that request IDs are displayed on pending and history cards.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Request ID on Pending Cards", () => {
  test("pending card shows truncated request ID", async ({ page }) => {
    await backend.authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await backend.createGPGSignRequest({
      repo_name: "id-test-repo",
      commit_msg: "feat: test request id display",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "IDTEST01",
      changed_files: ["main.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Request ID element should show first 8 chars of the UUID
    const idEl = card.locator(".request-id");
    await expect(idEl).toBeVisible();
    await expect(idEl).toHaveText(reqId.slice(0, 8));

    // Full ID should be in the title attribute
    await expect(idEl).toHaveAttribute("title", reqId);

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });
});

test.describe("Request ID on History Cards", () => {
  test("history entry shows truncated request ID", async ({ page }) => {
    const resolvedAt = new Date().toISOString();
    const fakeId = "abcd1234-5678-9abc-def0-123456789abc";

    // Intercept WebSocket to inject a history entry with a known ID
    await routeSnapshot(page, {
      rewrite: (s) => ({
        ...s,
        history: [
          {
            request: pendingRequest({ id: fakeId }),
            resolution: "approved",
            resolved_at: resolvedAt,
          },
        ],
      }),
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    // Wait for history to appear
    await expect(page.getByText("Recent Activity")).toBeVisible({
      timeout: 10000,
    });

    // History entry should show truncated request ID
    const idEl = page.locator(".history-request-id");
    await expect(idEl).toBeVisible();
    await expect(idEl).toHaveText("abcd1234");
    await expect(idEl).toHaveAttribute("title", fakeId);
  });

  test("history entry from real approval shows request ID", async ({
    page,
  }) => {
    await backend.authenticate(page);
    const reqId = await backend.createGPGSignRequest({
      repo_name: "history-id-repo",
      commit_msg: "feat: history id test",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "HISTID01",
      changed_files: ["file.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Deny the request to move it to history
    await card.getByRole("button", { name: "Deny" }).click();
    await expect(card).not.toBeVisible();

    // History should now contain the entry with its request ID
    const historyEntry = page.locator(".history-entry", {
      hasText: "feat: history id test",
    });
    await expect(historyEntry).toBeVisible();

    const idEl = historyEntry.locator(".history-request-id");
    await expect(idEl).toBeVisible();
    await expect(idEl).toHaveText(reqId.slice(0, 8));
    await expect(idEl).toHaveAttribute("title", reqId);
  });
});
