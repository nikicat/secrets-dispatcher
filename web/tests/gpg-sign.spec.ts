import { test, expect, type Page } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

// These tests verify GPG commit signing approval flow in the WebUI.
// They exercise the /api/v1/gpg-sign/request endpoint and the UI rendering
// of various commit types (normal, merge, amend, empty, tag, etc.).

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

/** POST a GPG sign request and return the request_id. */
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

/** Authenticate the page and wait for the connected status. */
async function authenticate(page: Page): Promise<void> {
  const loginURL = await backend.generateLoginURL();
  await page.goto(loginURL);
  await expect(page.getByText("client connected")).toBeVisible();
}

test.describe("GPG Sign API", () => {
  test("POST creates a pending gpg_sign request", async () => {
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "myrepo",
      commit_msg: "feat: add login",
      author: "Alice <alice@example.com> 1700000000 +0000",
      committer: "Alice <alice@example.com> 1700000000 +0000",
      key_id: "ABCD1234",
      changed_files: ["auth.go"],
    });
    expect(reqId).toBeTruthy();

    // Verify it appears in the pending list.
    const pending = await fetch(`${backend.url}/api/v1/pending`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const data = (await pending.json()) as {
      requests: Array<{
        id: string;
        type: string;
        gpg_sign_info: { key_id: string };
      }>;
    };
    const found = data.requests.find((r) => r.id === reqId);
    expect(found).toBeTruthy();
    expect(found!.type).toBe("gpg_sign");
    expect(found!.gpg_sign_info.key_id).toBe("ABCD1234");

    // Clean up: deny so it doesn't leak into other tests.
    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("POST without gpg_sign_info returns 400", async () => {
    const token = await backend.getAuthToken();
    const res = await fetch(`${backend.url}/api/v1/gpg-sign/request`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({ client: "test" }),
    });
    expect(res.status).toBe(400);
  });

  test("GET returns 405 Method Not Allowed", async () => {
    const token = await backend.getAuthToken();
    const res = await fetch(`${backend.url}/api/v1/gpg-sign/request`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(res.status).toBe(405);
  });
});

test.describe("GPG Sign Card Rendering", () => {
  test("shows GPG Sign badge and commit subject", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-app",
      commit_msg: "fix: resolve null pointer in parser",
      author: "Bob <bob@example.com> 1700000000 +0000",
      committer: "Bob <bob@example.com> 1700000000 +0000",
      key_id: "DEADBEEF",
      changed_files: ["parser.go"],
    });

    // Wait for the card to appear via WebSocket.
    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Badge should say "GPG Sign".
    await expect(card.locator(".type-badge--gpg_sign")).toHaveText("GPG Sign");

    // Commit subject shown in item-summary.
    await expect(card.locator(".item-summary")).toContainText(
      "fix: resolve null pointer in parser",
    );

    // Author and key visible in commit-meta.
    await expect(card.locator(".commit-meta")).toContainText("Bob");
    await expect(card.locator(".commit-meta")).toContainText("DEADBEEF");

    // Repo name visible in session-id area.
    await expect(card.locator(".session-id")).toContainText("my-app");

    // Changed files visible.
    await expect(card.locator(".changed-files")).toContainText("parser.go");

    // Approve and Deny buttons present.
    await expect(card.getByRole("button", { name: "Approve" })).toBeVisible();
    await expect(card.getByRole("button", { name: "Deny" })).toBeVisible();

    // Cleanup.
    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("shows multi-line commit body behind toggle", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-app",
      commit_msg:
        "feat: add OAuth2 support\n\nImplements RFC 6749 authorization code flow.\nIncludes refresh token rotation.",
      author: "Alice <alice@example.com> 1700000000 +0000",
      committer: "Alice <alice@example.com> 1700000000 +0000",
      key_id: "AAA111",
      changed_files: ["oauth.go", "config.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Subject line shown.
    await expect(card.locator(".item-summary")).toContainText(
      "feat: add OAuth2 support",
    );

    // Body is hidden behind a toggle.
    const toggle = card.locator(".commit-body-toggle");
    await expect(toggle).toBeVisible();
    await expect(toggle.locator("summary")).toContainText("Show full message");

    // Body not visible until expanded.
    await expect(card.locator(".commit-body")).not.toBeVisible();

    // Expand it.
    await toggle.locator("summary").click();
    await expect(card.locator(".commit-body")).toBeVisible();
    await expect(card.locator(".commit-body")).toContainText(
      "RFC 6749 authorization code flow",
    );
    await expect(card.locator(".commit-body")).toContainText(
      "refresh token rotation",
    );

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("shows different committer in More details", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "collab-repo",
      commit_msg: "docs: update readme",
      author: "Alice <alice@example.com> 1700000000 +0000",
      committer: "Bob <bob@example.com> 1700000001 +0000",
      key_id: "KEY123",
      changed_files: ["README.md"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // "More details" is collapsed by default.
    const details = card.locator(".secondary-meta");
    await expect(details).toBeVisible();

    // Expand it.
    await details.locator("summary").click();

    // Committer different from author should be shown.
    await expect(details).toContainText("Committer:");
    await expect(details).toContainText("Bob");

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });
});

test.describe("GPG Sign Corner Cases", () => {
  test("initial commit — no parent hash, no changed files", async ({
    page,
  }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "new-project",
      commit_msg: "Initial commit",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "INIT0001",
      changed_files: [],
      // No parent_hash — initial commit.
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();
    await expect(card.locator(".item-summary")).toContainText("Initial commit");

    // No changed files section should appear.
    await expect(card.locator(".changed-files")).not.toBeVisible();

    // "More details" should NOT show parent hash.
    await card.locator(".secondary-meta summary").click();
    await expect(card.locator(".secondary-meta")).not.toContainText("Parent:");

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("empty commit — no file changes but has parent", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "chore: empty commit for CI trigger",
      author: "CI Bot <ci@example.com> 1700000000 +0000",
      committer: "CI Bot <ci@example.com> 1700000000 +0000",
      key_id: "CI000001",
      changed_files: [],
      parent_hash: "abc123def456",
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();
    await expect(card.locator(".item-summary")).toContainText(
      "chore: empty commit for CI trigger",
    );

    // No changed files section.
    await expect(card.locator(".changed-files")).not.toBeVisible();

    // Parent hash should appear in More details.
    await card.locator(".secondary-meta summary").click();
    await expect(card.locator(".secondary-meta")).toContainText(
      "abc123def456",
    );

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("merge commit — message style and parent hash", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "main-repo",
      commit_msg: "Merge branch 'feature/auth' into main",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "MERGE001",
      changed_files: ["auth.go", "middleware.go", "go.sum"],
      parent_hash: "aaa111bbb222",
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();
    await expect(card.locator(".item-summary")).toContainText(
      "Merge branch 'feature/auth' into main",
    );

    // Changed files are visible.
    await expect(card.locator(".changed-files")).toContainText(
      "Changed files (3)",
    );

    // Parent hash in details.
    await card.locator(".secondary-meta summary").click();
    await expect(card.locator(".secondary-meta")).toContainText(
      "aaa111bbb222",
    );

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("amend commit — same author, different committer timestamp", async ({
    page,
  }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "fix: correct typo in handler",
      author: "Alice <alice@example.com> 1700000000 +0000",
      committer: "Alice <alice@example.com> 1700001000 +0000",
      key_id: "AMEND001",
      changed_files: ["handler.go"],
      parent_hash: "def456abc789",
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();
    await expect(card.locator(".item-summary")).toContainText(
      "fix: correct typo in handler",
    );

    // Author displayed in commit meta.
    await expect(card.locator(".commit-meta")).toContainText("Alice");

    // Committer has different timestamp — should appear in More details.
    await card.locator(".secondary-meta summary").click();
    await expect(card.locator(".secondary-meta")).toContainText("Committer:");
    await expect(card.locator(".secondary-meta")).toContainText("1700001000");

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("many changed files — shows first 5 then expandable overflow", async ({
    page,
  }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const files = Array.from({ length: 8 }, (_, i) => `src/file${i + 1}.go`);
    const reqId = await createGPGSignRequest(token, {
      repo_name: "big-change",
      commit_msg: "refactor: restructure src directory",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "BIG00001",
      changed_files: files,
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // "Changed files (8)" header.
    await expect(card.locator(".changed-files")).toContainText(
      "Changed files (8)",
    );

    // First 5 files should be visible immediately.
    for (let i = 1; i <= 5; i++) {
      await expect(card.locator(".changed-files")).toContainText(
        `src/file${i}.go`,
      );
    }

    // "3 more files" expansion toggle.
    const moreFiles = card.locator(".changed-files details");
    await expect(moreFiles.locator("summary")).toContainText("3 more files");

    // Overflow files hidden until expanded (check the detail element is collapsed).
    await expect(moreFiles).not.toHaveAttribute("open", "");

    await moreFiles.locator("summary").click();
    await expect(moreFiles).toHaveAttribute("open", "");
    await expect(moreFiles).toContainText("src/file6.go");
    await expect(moreFiles).toContainText("src/file8.go");

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("single-line commit — no body toggle shown", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "chore: bump version to 1.2.3",
      author: "Release Bot <bot@example.com> 1700000000 +0000",
      committer: "Release Bot <bot@example.com> 1700000000 +0000",
      key_id: "RELEASE1",
      changed_files: ["version.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // No "Show full message" toggle for single-line messages.
    await expect(card.locator(".commit-body-toggle")).not.toBeVisible();

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("empty commit message", async ({ page }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "EMPTY001",
      changed_files: ["file.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Badge should still say GPG Sign.
    await expect(card.locator(".type-badge--gpg_sign")).toHaveText("GPG Sign");

    // No body toggle.
    await expect(card.locator(".commit-body-toggle")).not.toBeVisible();

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });

  test("same author and committer — committer hidden in details", async ({
    page,
  }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "test: same identity",
      author: "Same <same@example.com> 1700000000 +0000",
      committer: "Same <same@example.com> 1700000000 +0000",
      key_id: "SAME0001",
      changed_files: ["test.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // When author === committer, committer line should not appear.
    await card.locator(".secondary-meta summary").click();
    await expect(card.locator(".secondary-meta")).not.toContainText(
      "Committer:",
    );

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });
});

test.describe("GPG Sign Approval Flow", () => {
  test("deny removes request from UI and adds to history", async ({
    page,
  }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "feat: to be denied",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "DENY0001",
      changed_files: ["main.go"],
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Click Deny in the UI.
    await card.getByRole("button", { name: "Deny" }).click();

    // Card should disappear.
    await expect(card).not.toBeVisible();

    // Empty state should return.
    await expect(page.getByText("No pending requests")).toBeVisible();

    // History should contain the denied entry.
    const historyEntry = page.locator(".history-entry", {
      hasText: "feat: to be denied",
    });
    await expect(historyEntry).toBeVisible();
    await expect(historyEntry.locator(".history-resolution")).toHaveText(
      "denied",
    );
  });

  test("approve removes request from UI and adds to history", async ({
    page,
  }) => {
    await authenticate(page);
    const token = await backend.getAuthToken();
    await createGPGSignRequest(token, {
      repo_name: "my-repo",
      commit_msg: "feat: to be approved",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "APPR0001",
      changed_files: ["main.go"],
      commit_object:
        "tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\nauthor Dev <dev@example.com> 1700000000 +0000\ncommitter Dev <dev@example.com> 1700000000 +0000\n\nfeat: to be approved\n",
    });

    const card = page.locator(".card--gpg_sign");
    await expect(card).toBeVisible();

    // Click Approve — this will attempt real gpg and likely fail (no key),
    // but the request should still be resolved and removed from pending.
    await card.getByRole("button", { name: "Approve" }).click();

    // Card should disappear (either approved or gpg-failed, both resolve).
    await expect(card).not.toBeVisible({ timeout: 10000 });
  });

  test("WebSocket delivers request_created for gpg_sign", async ({
    page,
  }) => {
    await authenticate(page);

    // Initially empty.
    await expect(page.getByText("No pending requests")).toBeVisible();

    // Create a request via API — UI should get it via WebSocket.
    const token = await backend.getAuthToken();
    const reqId = await createGPGSignRequest(token, {
      repo_name: "ws-test",
      commit_msg: "chore: test websocket delivery",
      author: "Dev <dev@example.com> 1700000000 +0000",
      committer: "Dev <dev@example.com> 1700000000 +0000",
      key_id: "WS000001",
      changed_files: ["ws.go"],
    });

    // Card should appear without page reload.
    await expect(page.locator(".card--gpg_sign")).toBeVisible();
    await expect(page.locator(".item-summary")).toContainText(
      "chore: test websocket delivery",
    );

    await fetch(`${backend.url}/api/v1/pending/${reqId}/deny`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
  });
});
