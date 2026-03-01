import { test, expect } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

// These tests verify auto-approve rule WebSocket notifications and timer display.

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
    let snapshotMsg: Record<string, unknown> | null = null;

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              snapshotMsg = parsed;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);
    await expect(page.getByText("No pending requests")).toBeVisible();

    expect(snapshotMsg).not.toBeNull();
    expect(snapshotMsg).toHaveProperty("auto_approve_rules");
    expect(Array.isArray(snapshotMsg!.auto_approve_rules)).toBe(true);
  });

  test("rules from snapshot appear in sidebar", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();

    // Intercept real server snapshot and inject rules into it
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.auto_approve_rules = [
                {
                  id: "test-rule-1",
                  invoker_name: "test-invoker",
                  request_type: "get_secret",
                  collection: "default",
                  expires_at: expiresAt,
                },
              ];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("Auto-Approve Rules")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("test-invoker")).toBeVisible();
    await expect(page.getByText("Secret", { exact: true })).toBeVisible();
    await expect(page.getByText("default")).toBeVisible();
  });

  test("rule_added message via WS adds rule to sidebar", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();

    // Forward real WS but inject a rule_added message after snapshot
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        ws.send(message);
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              // Send rule_added synchronously after snapshot
              ws.send(
                JSON.stringify({
                  type: "auto_approve_rule_added",
                  auto_approve_rule: {
                    id: "ws-rule-1",
                    invoker_name: "ws-invoker",
                    request_type: "search",
                    collection: "login",
                    expires_at: expiresAt,
                  },
                }),
              );
            }
          } catch { /* not JSON */ }
        }
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("Auto-Approve Rules")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("ws-invoker")).toBeVisible();
    await expect(page.getByText("Search")).toBeVisible();
    await expect(page.getByText("login")).toBeVisible();
  });

  test("rule_removed message via WS removes rule from sidebar", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 120_000).toISOString();

    let sendToPage: ((msg: string) => void) | null = null;

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      sendToPage = (msg) => ws.send(msg);
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.auto_approve_rules = [
                {
                  id: "rule-to-remove",
                  invoker_name: "remove-me",
                  request_type: "search",
                  collection: "login",
                  expires_at: expiresAt,
                },
              ];
              parsed.trusted_signers = [];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    // Rule should be visible
    await expect(page.getByText("remove-me")).toBeVisible({ timeout: 10000 });

    // Send rule_removed
    sendToPage!(JSON.stringify({ type: "auto_approve_rule_removed", id: "rule-to-remove" }));

    // Rule should disappear
    await expect(page.getByText("remove-me")).not.toBeVisible();
    await expect(page.getByText("Auto-Approve Rules")).not.toBeVisible();
  });
});

test.describe("Auto-Approve Rule Reset", () => {
  test("rule_added with same ID updates expiry instead of duplicating", async ({ page }) => {
    const initialExpiry = new Date(Date.now() + 30_000).toISOString();
    const resetExpiry = new Date(Date.now() + 120_000).toISOString();

    let sendToPage: ((msg: string) => void) | null = null;

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      sendToPage = (msg) => ws.send(msg);
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.auto_approve_rules = [
                {
                  id: "reset-rule",
                  invoker_name: "reset-invoker",
                  request_type: "get_secret",
                  collection: "default",
                  expires_at: initialExpiry,
                },
              ];
              parsed.trusted_signers = [];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("reset-invoker")).toBeVisible({ timeout: 10000 });

    // Should show ~30s remaining
    const timerEl = page.locator(".rule-expiry");
    await expect(timerEl).toHaveText(/^\d+s$/);

    // Simulate timer reset: backend sends rule_added with same ID, new expiry
    sendToPage!(JSON.stringify({
      type: "auto_approve_rule_added",
      auto_approve_rule: {
        id: "reset-rule",
        invoker_name: "reset-invoker",
        request_type: "get_secret",
        collection: "default",
        expires_at: resetExpiry,
      },
    }));

    // Should show ~2m remaining now (not ~30s)
    await expect(timerEl).toHaveText(/^\d+m \d+s$/, { timeout: 3000 });

    // Should still be exactly one rule, not two
    const ruleEntries = page.locator(".rule-entry");
    await expect(ruleEntries).toHaveCount(1);
  });

  test("timer continues ticking after reset", async ({ page }) => {
    const initialExpiry = new Date(Date.now() + 10_000).toISOString();
    const resetExpiry = new Date(Date.now() + 65_000).toISOString();

    let sendToPage: ((msg: string) => void) | null = null;

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      sendToPage = (msg) => ws.send(msg);
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.auto_approve_rules = [
                {
                  id: "tick-rule",
                  invoker_name: "tick-invoker",
                  request_type: "search",
                  collection: "",
                  expires_at: initialExpiry,
                },
              ];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("tick-invoker")).toBeVisible({ timeout: 10000 });

    // Reset the timer with a longer expiry
    sendToPage!(JSON.stringify({
      type: "auto_approve_rule_added",
      auto_approve_rule: {
        id: "tick-rule",
        invoker_name: "tick-invoker",
        request_type: "search",
        collection: "",
        expires_at: resetExpiry,
      },
    }));

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

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.auto_approve_rules = [
                {
                  id: "timer-rule",
                  invoker_name: "timer-test",
                  request_type: "get_secret",
                  collection: "",
                  expires_at: expiresAt,
                },
              ];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("timer-test")).toBeVisible({ timeout: 10000 });

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

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.auto_approve_rules = [
                {
                  id: "expiring-rule",
                  invoker_name: "expiring-test",
                  request_type: "get_secret",
                  collection: "",
                  expires_at: expiresAt,
                },
              ];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    const loginURL = await backend.generateLoginURL();
    await page.goto(loginURL);

    await expect(page.getByText("expiring-test")).toBeVisible({ timeout: 10000 });

    // Wait for expiry cleanup (rule expires in ~2s, cleanup runs every 1s)
    await expect(page.getByText("expiring-test")).not.toBeVisible({ timeout: 5000 });
  });
});
