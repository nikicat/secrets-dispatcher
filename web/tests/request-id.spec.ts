import { expect, type Page, test } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

// These tests verify that request IDs are displayed on pending and history cards.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

async function createGPGSignRequest(
  token: string,
  info: Record<string, unknown>,
  client = "test-repo",
): Promise<string> {
  const res = await fetch(`${backend.url}/api/v1/gpg-sign/request`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ client, gpg_sign_info: info }),
  });
  if (!res.ok) throw new Error(`POST gpg-sign/request failed: ${res.status}`);
  const data = (await res.json()) as { request_id: string };
  return data.request_id;
}

async function authenticate(page: Page): Promise<void> {
  const loginURL = await backend.generateLoginURL();
  await page.goto(loginURL);
  await expect(page.getByText("client connected")).toBeVisible();
}

test.describe("Request ID on Pending Cards", () => {
  test("pending card shows truncated request ID", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
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
                    id: fakeId,
                    client: "test-client",
                    items: [
                      {
                        path: "/org/freedesktop/secrets/collection/default/1",
                        label: "ID Test Secret",
                        attributes: {},
                      },
                    ],
                    session: "/org/freedesktop/secrets/session/1",
                    created_at: resolvedAt,
                    expires_at: resolvedAt,
                    type: "get_secret",
                    sender_info: {
                      sender: ":1.100",
                      pid: 12345,
                      uid: 1000,
                      user_name: "testuser",
                      unit_name: "test.service",
                    },
                  },
                  resolution: "approved",
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

  test("history entry from real approval shows request ID", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
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
