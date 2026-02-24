import { test, expect, type Page } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

// These tests verify the single-request focus mode, where clicking a desktop
// notification opens the web UI showing only the targeted request.
//
// Auto-close behavior:
//   - Approved/Denied (from any channel) → window.close()
//   - Cancelled/Expired → keep page open, show "no longer pending" message

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

/** POST a GPG sign request and return the request_id. */
async function createRequest(
  overrides: Record<string, unknown> = {},
): Promise<string> {
  const token = await backend.getAuthToken();
  const info = {
    repo_name: "test-repo",
    commit_msg: "test: focus mode commit",
    author: "Dev <dev@example.com> 1700000000 +0000",
    committer: "Dev <dev@example.com> 1700000000 +0000",
    key_id: "FOCUS001",
    changed_files: ["main.go"],
    ...overrides,
  };
  const res = await fetch(`${backend.url}/api/v1/gpg-sign/request`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ client: "focus-test", gpg_sign_info: info }),
  });
  if (!res.ok) throw new Error(`POST gpg-sign/request failed: ${res.status}`);
  const data = (await res.json()) as { request_id: string };
  return data.request_id;
}

/** Deny a request via API. */
async function denyRequest(reqId: string): Promise<void> {
  const token = await backend.getAuthToken();
  await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
  });
}

/** Cancel a request via API. */
async function cancelRequest(reqId: string): Promise<void> {
  const token = await backend.getAuthToken();
  await fetch(`${backend.url}/api/v1/pending/${reqId}/cancel`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
  });
}

/** Authenticate page, then navigate to focus URL for given request ID. */
async function openFocused(page: Page, requestId: string): Promise<void> {
  const loginURL = await backend.generateLoginURL();
  await page.goto(loginURL);
  await expect(page.getByText("client connected")).toBeVisible();
  await page.goto(`${backend.url}/?request=${requestId}`);
}

/** Spy on window.close() to capture calls without actually closing. */
async function spyWindowClose(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as any).__closeCalled = false;
    window.close = () => {
      (window as any).__closeCalled = true;
    };
  });
}

/** Check whether window.close() was called. */
async function wasWindowCloseCalled(page: Page): Promise<boolean> {
  return page.evaluate(() => (window as any).__closeCalled);
}

test.describe("Focus Mode — Basic Display", () => {
  test("shows only the focused request", async ({ page }) => {
    const reqId = await createRequest({ commit_msg: "feat: focused request" });

    await openFocused(page, reqId);

    const card = page.locator(".card");
    await expect(card).toBeVisible();
    await expect(card.locator(".item-summary")).toContainText(
      "feat: focused request",
    );
    await expect(page.locator(".card")).toHaveCount(1);

    await denyRequest(reqId);
  });

  test("hides sidebar and header actions", async ({ page }) => {
    const reqId = await createRequest();

    await openFocused(page, reqId);
    await expect(page.locator(".card")).toBeVisible();

    // Header title still shown
    await expect(page.locator("header h1")).toHaveText("Secrets Dispatcher");

    // Sidebar and header actions not rendered
    await expect(page.locator(".sidebar")).not.toBeAttached();
    await expect(page.locator(".header-actions")).not.toBeAttached();

    await denyRequest(reqId);
  });

  test("does not show other pending requests", async ({ page }) => {
    const reqId1 = await createRequest({ commit_msg: "feat: request one" });
    const reqId2 = await createRequest({ commit_msg: "feat: request two" });

    await openFocused(page, reqId1);

    await expect(page.locator(".card")).toHaveCount(1);
    await expect(page.locator(".item-summary")).toContainText(
      "feat: request one",
    );
    await expect(page.locator(".item-summary")).not.toContainText(
      "feat: request two",
    );

    await denyRequest(reqId1);
    await denyRequest(reqId2);
  });

  test("does not show history section", async ({ page }) => {
    const reqId = await createRequest();

    await openFocused(page, reqId);
    await expect(page.locator(".card")).toBeVisible();
    await expect(page.locator(".history-section")).not.toBeVisible();

    await denyRequest(reqId);
  });
});

test.describe("Focus Mode — Auto-close on Resolve", () => {
  test("deny in UI calls window.close()", async ({ page }) => {
    const reqId = await createRequest({ commit_msg: "feat: deny in UI" });

    await openFocused(page, reqId);
    await expect(page.locator(".card")).toBeVisible();
    await spyWindowClose(page);

    await page.getByRole("button", { name: "Deny" }).click();

    await expect
      .poll(() => wasWindowCloseCalled(page), { timeout: 5000 })
      .toBe(true);
  });

  test("approve in UI calls window.close()", async ({ page }) => {
    const reqId = await createRequest({
      commit_msg: "feat: approve in UI",
      commit_object:
        "tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\nauthor Dev <dev@example.com> 1700000000 +0000\ncommitter Dev <dev@example.com> 1700000000 +0000\n\nfeat: approve in UI\n",
    });

    await openFocused(page, reqId);
    await expect(page.locator(".card")).toBeVisible();
    await spyWindowClose(page);

    await page.getByRole("button", { name: "Approve" }).click();

    await expect
      .poll(() => wasWindowCloseCalled(page), { timeout: 10000 })
      .toBe(true);
  });

  test("deny via external API calls window.close()", async ({ page }) => {
    const reqId = await createRequest({
      commit_msg: "feat: external deny",
    });

    await openFocused(page, reqId);
    await expect(page.locator(".card")).toBeVisible();
    await spyWindowClose(page);

    // Deny from outside (simulates another tab or notification button)
    await denyRequest(reqId);

    await expect
      .poll(() => wasWindowCloseCalled(page), { timeout: 5000 })
      .toBe(true);
  });
});

test.describe("Focus Mode — Cancel/Expire Keep Open", () => {
  test("cancel shows message without closing", async ({ page }) => {
    const reqId = await createRequest({
      commit_msg: "feat: to be cancelled",
    });

    await openFocused(page, reqId);
    await expect(page.locator(".card")).toBeVisible();
    await spyWindowClose(page);

    await cancelRequest(reqId);

    await expect(
      page.getByText("Request is no longer pending"),
    ).toBeVisible();
    expect(await wasWindowCloseCalled(page)).toBe(false);
  });
});

test.describe("Focus Mode — Invalid/Missing Request", () => {
  test("shows 'not found' for nonexistent request ID", async ({ page }) => {
    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    await expect(page.getByText("client connected")).toBeVisible();

    await page.goto(`${backend.url}/?request=nonexistent-id-12345`);

    await expect(page.getByText("Request not found")).toBeVisible();
    await expect(page.locator(".card")).not.toBeAttached();
  });

  test("shows 'not found' for already-resolved request", async ({ page }) => {
    const reqId = await createRequest({ commit_msg: "feat: already resolved" });
    await denyRequest(reqId);

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    await expect(page.getByText("client connected")).toBeVisible();

    await page.goto(`${backend.url}/?request=${reqId}`);

    await expect(page.getByText("Request not found")).toBeVisible();
  });
});

test.describe("Focus Mode — Auth Integration", () => {
  test("token and request params work together", async ({ page }) => {
    const reqId = await createRequest({ commit_msg: "feat: auth + focus" });

    // Navigate with both token and request params
    const loginURL = await backend.generateLoginURL();
    await page.goto(`${loginURL}&request=${reqId}`);

    const card = page.locator(".card");
    await expect(card).toBeVisible();
    await expect(card.locator(".item-summary")).toContainText(
      "feat: auth + focus",
    );

    await denyRequest(reqId);
  });
});

test.describe("Focus Mode — Request Expiration", () => {
  let shortBackend: TestBackend;

  test.beforeAll(async () => {
    shortBackend = await startTestBackend({ extraArgs: ["--timeout", "2s"] });
  });

  test.afterAll(async () => {
    await shortBackend.cleanup();
  });

  test("shows message when focused request expires", async ({ page }) => {
    const token = await shortBackend.getAuthToken();

    const res = await fetch(
      `${shortBackend.url}/api/v1/gpg-sign/request`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          client: "expire-test",
          gpg_sign_info: {
            repo_name: "test-repo",
            commit_msg: "feat: will expire",
            author: "Dev <dev@example.com> 1700000000 +0000",
            committer: "Dev <dev@example.com> 1700000000 +0000",
            key_id: "EXPIRE01",
            changed_files: ["main.go"],
          },
        }),
      },
    );
    const { request_id: reqId } = (await res.json()) as {
      request_id: string;
    };

    // Authenticate and open focus page
    const loginURL = await shortBackend.generateLoginURL();
    await page.goto(loginURL);
    await expect(page.getByText("client connected")).toBeVisible();

    await page.goto(`${shortBackend.url}/?request=${reqId}`);
    await expect(page.locator(".card")).toBeVisible();

    // Install spy after page loads
    await page.evaluate(() => {
      (window as any).__closeCalled = false;
      window.close = () => {
        (window as any).__closeCalled = true;
      };
    });

    // Wait for expiration (2s timeout + buffer)
    await expect(
      page.getByText("Request is no longer pending"),
    ).toBeVisible({ timeout: 10000 });

    // Should NOT auto-close on expiration
    expect(
      await page.evaluate(() => (window as any).__closeCalled),
    ).toBe(false);
  });
});
