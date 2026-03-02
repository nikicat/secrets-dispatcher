import { expect, test } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

// These tests verify trust rules (config-defined persistent rules) display in the WebUI.

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Trust Rules WebSocket", () => {
  test("snapshot includes trust_rules field", async ({ page }) => {
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
    expect(snapshotMsg).toHaveProperty("trust_rules");
    expect(Array.isArray(snapshotMsg!.trust_rules)).toBe(true);
  });

  test("trust rules from snapshot appear in sidebar", async ({ page }) => {
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.trust_rules = [
                {
                  name: "Allow GitHub CLI",
                  action: "approve",
                  request_types: ["search", "get_secret"],
                  process: { name: "gh" },
                  secret: { collection: "default" },
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

    await expect(page.getByText("Trust Rules")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("Allow GitHub CLI")).toBeVisible();
    await expect(page.getByText("config")).toBeVisible();
  });

  test("ignore action trust rule displays correctly", async ({ page }) => {
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.trust_rules = [
                {
                  name: "Ignore Chrome dummy",
                  action: "ignore",
                  request_types: ["write"],
                  secret: {
                    attributes: {
                      "xdg:schema": "_chrome_dummy_schema_for_unlocking",
                    },
                  },
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

    await expect(page.getByText("Trust Rules")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("Ignore Chrome dummy")).toBeVisible();
    // Should show "Ignore Write" for ignore action with write request type
    await expect(page.getByText("Ignore Write")).toBeVisible();
  });

  test("multiple trust rules render in order", async ({ page }) => {
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.trust_rules = [
                {
                  name: "Rule One",
                  action: "approve",
                  request_types: ["search"],
                  process: { name: "gh" },
                },
                {
                  name: "Rule Two",
                  action: "ignore",
                  request_types: ["write"],
                  process: { exe: "/opt/google/chrome/chrome" },
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

    await expect(page.getByText("Trust Rules")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("Rule One")).toBeVisible();
    await expect(page.getByText("Rule Two")).toBeVisible();
  });

  test("trust rules section hidden when no rules", async ({ page }) => {
    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.trust_rules = [];
              parsed.auto_approve_rules = [];
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

    await expect(page.getByText("No pending requests")).toBeVisible();
    await expect(page.getByText("Trust Rules")).not.toBeVisible();
  });

  test("trust rules coexist with auto-approve rules in sidebar", async ({ page }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();

    await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
      const server = ws.connectToServer();
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.trust_rules = [
                {
                  name: "Config rule",
                  action: "approve",
                  request_types: ["search"],
                  process: { name: "gh" },
                },
              ];
              parsed.auto_approve_rules = [
                {
                  id: "temp-rule-1",
                  invoker_name: "temp-invoker",
                  request_type: "get_secret",
                  collection: "default",
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

    // Both sections should be visible
    await expect(page.getByText("Trust Rules")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText("Auto-Approve Rules")).toBeVisible();

    // Both rule contents visible
    await expect(page.getByText("Config rule")).toBeVisible();
    await expect(page.getByText("temp-invoker")).toBeVisible();
  });
});
