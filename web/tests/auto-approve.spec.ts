import { expect, test } from "@playwright/test";
import {
  pendingRequest,
  startTestBackend,
  type TestBackend,
} from "./fixtures/test-utils.mts";
import type {
  HistoryEntry,
  PendingRequest,
  WSSnapshotMessage,
} from "../src/lib/types.ts";

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Auto-Approve Rules API", () => {
  test("auto-approve list returns empty array initially", async ({
    request,
  }) => {
    const token = await backend.getAuthToken();
    const response = await request.get(`${backend.url}/api/v1/auto-approve`, {
      headers: { Authorization: `Bearer ${token}` },
    });

    expect(response.status()).toBe(200);
    const data = await response.json();
    expect(data).toEqual([]);
  });

  test("auto-approve list requires authentication", async ({ request }) => {
    const response = await request.get(`${backend.url}/api/v1/auto-approve`);
    expect(response.status()).toBe(401);
  });
});

test.describe("Auto-Approve Rules WebSocket", () => {
  test("snapshot includes auto_approve_rules field", async ({ page }) => {
    let snapshotMsg: WSSnapshotMessage | null = null;

    await backend.openWith(page, {
      rewrite: (s) => {
        snapshotMsg = s;
        return s;
      },
    });
    await expect(page.getByText("No pending requests")).toBeVisible();

    expect(snapshotMsg).not.toBeNull();
    expect(snapshotMsg).toHaveProperty("auto_approve_rules");
    expect(Array.isArray(snapshotMsg!.auto_approve_rules)).toBe(true);
  });

  test("rules from snapshot appear in sidebar", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();

    // Intercept real server snapshot and inject rules into it
    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        auto_approve_rules: [
          {
            id: "test-rule-1",
            invoker_name: "test-invoker",
            invoker_exe: "/usr/bin/test-invoker",
            request_type: "get_secret",
            collection: "default",
            expires_at: expiresAt,
          },
        ],
      }),
    });

    await expect(page.getByText("Auto-Approve Rules")).toBeVisible();
    await expect(page.getByText("test-invoker")).toBeVisible();
    await expect(page.getByText("Secret", { exact: true })).toBeVisible();
    await expect(page.getByText("default")).toBeVisible();
  });

  test("rule_added message via WS adds rule to sidebar", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();

    // Forward real WS but inject a rule_added message after snapshot
    await backend.openWith(page, {
      after: [
        {
          type: "auto_approve_rule_added",
          auto_approve_rule: {
            id: "ws-rule-1",
            invoker_name: "ws-invoker",
            invoker_exe: "/usr/bin/ws-invoker",
            request_type: "search",
            collection: "login",
            expires_at: expiresAt,
          },
        },
      ],
    });

    await expect(page.getByText("Auto-Approve Rules")).toBeVisible();
    await expect(page.getByText("ws-invoker")).toBeVisible();
    await expect(page.getByText("Search")).toBeVisible();
    await expect(page.getByText("login")).toBeVisible();
  });

  test("rule_removed message via WS removes rule from sidebar", async ({
    page,
  }) => {
    const expiresAt = new Date(Date.now() + 120_000).toISOString();

    const sendToPage = await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        auto_approve_rules: [
          {
            id: "rule-to-remove",
            invoker_name: "remove-me",
            invoker_exe: "/usr/bin/remove-me",
            request_type: "search",
            collection: "login",
            expires_at: expiresAt,
          },
        ],
      }),
    });

    // Rule should be visible
    await expect(page.getByText("remove-me")).toBeVisible();

    // Send rule_removed
    sendToPage({
      type: "auto_approve_rule_removed",
      id: "rule-to-remove",
    });

    // Rule should disappear
    await expect(page.getByText("remove-me")).not.toBeVisible();
    await expect(page.getByText("Auto-Approve Rules")).not.toBeVisible();
  });
});

test.describe("Auto-Approve Rule Reset", () => {
  test("rule_added with same ID updates expiry instead of duplicating", async ({
    page,
  }) => {
    const initialExpiry = new Date(Date.now() + 30_000).toISOString();
    const resetExpiry = new Date(Date.now() + 120_000).toISOString();

    const sendToPage = await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        auto_approve_rules: [
          {
            id: "reset-rule",
            invoker_name: "reset-invoker",
            invoker_exe: "/usr/bin/reset-invoker",
            request_type: "get_secret",
            collection: "default",
            expires_at: initialExpiry,
          },
        ],
      }),
    });

    await expect(page.getByText("reset-invoker")).toBeVisible();

    // Should show ~30s remaining
    const timerEl = page.locator(".rule-expiry");
    await expect(timerEl).toHaveText(/^\d+s$/);

    // Simulate timer reset: backend sends rule_added with same ID, new expiry
    sendToPage({
      type: "auto_approve_rule_added",
      auto_approve_rule: {
        id: "reset-rule",
        invoker_name: "reset-invoker",
        invoker_exe: "/usr/bin/reset-invoker",
        request_type: "get_secret",
        collection: "default",
        expires_at: resetExpiry,
      },
    });

    // Should show ~2m remaining now (not ~30s)
    await expect(timerEl).toHaveText(/^\d+m \d+s$/, { timeout: 3000 });

    // Should still be exactly one rule, not two
    const ruleEntries = page.locator(".rule-entry");
    await expect(ruleEntries).toHaveCount(1);
  });

  test("timer continues ticking after reset", async ({ page }) => {
    const initialExpiry = new Date(Date.now() + 10_000).toISOString();
    const resetExpiry = new Date(Date.now() + 65_000).toISOString();

    const sendToPage = await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        auto_approve_rules: [
          {
            id: "tick-rule",
            invoker_name: "tick-invoker",
            invoker_exe: "/usr/bin/tick-invoker",
            request_type: "search",
            expires_at: initialExpiry,
          },
        ],
      }),
    });

    await expect(page.getByText("tick-invoker")).toBeVisible();

    // Reset the timer with a longer expiry
    sendToPage({
      type: "auto_approve_rule_added",
      auto_approve_rule: {
        id: "tick-rule",
        invoker_name: "tick-invoker",
        invoker_exe: "/usr/bin/tick-invoker",
        request_type: "search",
        expires_at: resetExpiry,
      },
    });

    const timerEl = page.locator(".rule-expiry");
    await expect(timerEl).toHaveText(/^\d+m \d+s$/, { timeout: 3000 });

    const textBefore = await timerEl.textContent();

    // Wait for timer to tick
    await page.waitForTimeout(3000);

    const textAfter = await timerEl.textContent();
    expect(textAfter).toMatch(/^\d+m \d+s$/);
    expect(textAfter).not.toBe(textBefore);
  });
});

test.describe("Auto-Approve Rule Timer", () => {
  test("rule expiry timer ticks down", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 65_000).toISOString();

    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        auto_approve_rules: [
          {
            id: "timer-rule",
            invoker_name: "timer-test",
            invoker_exe: "/usr/bin/timer-test",
            request_type: "get_secret",
            expires_at: expiresAt,
          },
        ],
      }),
    });

    await expect(page.getByText("timer-test")).toBeVisible();

    const timerEl = page.locator(".rule-expiry");
    const initialText = await timerEl.textContent();
    expect(initialText).toMatch(/^\d+m \d+s$/);

    // Wait 3 seconds for the timer to tick
    await page.waitForTimeout(3000);

    const updatedText = await timerEl.textContent();
    expect(updatedText).toMatch(/^\d+m \d+s$/);
    expect(updatedText).not.toBe(initialText);
  });

  test("expired rules are cleaned up automatically", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 2_000).toISOString();

    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        auto_approve_rules: [
          {
            id: "expiring-rule",
            invoker_name: "expiring-test",
            invoker_exe: "/usr/bin/expiring-test",
            request_type: "get_secret",
            expires_at: expiresAt,
          },
        ],
      }),
    });

    await expect(page.getByText("expiring-test")).toBeVisible();

    // Wait for expiry cleanup (rule expires in ~2s, cleanup runs every 1s)
    await expect(page.getByText("expiring-test")).not.toBeVisible();
  });
});

test.describe("Approve and auto-approve", () => {
  const localAndRemote = () => [
    pendingRequest({ id: "local-req" }),
    pendingRequest({ id: "remote-req", auto_approve_scope: undefined }),
  ];

  test("is offered only on a request a rule could match", async ({ page }) => {
    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        requests: localAndRemote(),
      }),
    });

    const card = (id: string) =>
      page
        .locator(".card")
        .filter({ has: page.locator(`.request-id[title="${id}"]`) });
    await expect(card("local-req").locator(".btn-approve-auto")).toBeVisible();
    const remote = card("remote-req");
    await expect(remote.locator(".btn-approve")).toBeVisible();
    await expect(remote.locator(".btn-approve-auto")).toHaveCount(0);
  });

  test("of similar requests is offered on history only for a request a rule could match", async ({
    page,
  }) => {
    const cancelled = (request: PendingRequest): HistoryEntry => ({
      request,
      resolution: "cancelled",
      resolved_at: new Date().toISOString(),
    });
    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        history: localAndRemote().map(cancelled),
      }),
    });

    const entry = (id: string) =>
      page
        .locator(".history-entry")
        .filter({ has: page.locator(`.history-request-id[title="${id}"]`) });
    await expect(entry("local-req").locator(".btn-auto-approve")).toBeVisible();
    const remote = entry("remote-req");
    await expect(remote.locator(".history-resolution")).toBeVisible();
    await expect(remote.locator(".btn-auto-approve")).toHaveCount(0);
  });

  test("of similar requests offers a timer reset when a rule has the request's scope", async ({
    page,
  }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();
    const scope = (exe: string) => ({
      invoker_exe: exe,
      request_type: "get_secret",
      collection: "default",
    });
    const cancelled = (id: string, exe: string): HistoryEntry => ({
      request: pendingRequest({ id, auto_approve_scope: scope(exe) }),
      resolution: "cancelled",
      resolved_at: new Date().toISOString(),
    });
    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        history: [
          cancelled("same-exe", "/usr/bin/test"),
          cancelled("other-exe", "/usr/bin/spoofed"),
        ],
        // The rule's invoker name differs from the requests' on purpose: rules
        // match on the executable only.
        auto_approve_rules: [
          {
            id: "rule",
            invoker_name: "renamed",
            ...scope("/usr/bin/test"),
            expires_at: expiresAt,
          },
        ],
      }),
    });

    const button = (id: string) =>
      page
        .locator(".history-entry")
        .filter({ has: page.locator(`.history-request-id[title="${id}"]`) })
        .locator(".btn-auto-approve");
    await expect(button("same-exe")).toHaveText("Reset auto-approve timer");
    await expect(button("other-exe")).toHaveText("Auto-approve similar");
  });
});
