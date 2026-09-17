import { expect, type Page, test } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

const now = new Date().toISOString();

function historyEntry(
  id: string,
  resolution = "approved",
  overrides: Record<string, unknown> = {},
) {
  return {
    request: {
      id,
      client: "local",
      items: [{
        path: "/org/freedesktop/secrets/collection/login/item1",
        label: "GitHub token",
        attributes: { service: "github.com", account: "developer" },
      }],
      session: "/org/freedesktop/secrets/session/1",
      created_at: now,
      expires_at: now,
      type: "get_secret",
      sender_info: {
        sender: ":1.100",
        pid: 1234,
        uid: 1000,
        user_name: "testuser",
        invoker_name: "gh",
        process_chain: [{
          name: "gh",
          pid: 1234,
          exe: "/usr/bin/gh",
          args: ["gh"],
          cwd: "/home/test/project",
        }],
      },
      ...overrides,
    },
    resolution,
    resolved_at: now,
  };
}

function savedRule(id: string, name: string) {
  return {
    id,
    created_at: now,
    name,
    action: "approve",
    request_types: ["get_secret"],
    process: { exe: "/usr/bin/gh", direct: true },
    secret: {
      collection: "login",
      attributes: { service: "github.com", account: "developer" },
    },
  };
}

async function injectSnapshot(
  page: Page,
  update: (snapshot: Record<string, unknown>) => void,
): Promise<void> {
  await page.routeWebSocket("**/api/v1/ws", (ws) => {
    const server = ws.connectToServer();
    server.onMessage((message) => {
      if (typeof message === "string") {
        try {
          const parsed = JSON.parse(message);
          if (parsed.type === "snapshot") {
            update(parsed);
            ws.send(JSON.stringify(parsed));
            return;
          }
        } catch { /* not JSON */ }
      }
      ws.send(message);
    });
  });
}

test.describe("Saved approvals", () => {
  test("snapshot and WebSocket changes update the saved approval list", async ({ page }) => {
    let sendToPage: ((message: string) => void) | undefined;
    await page.routeWebSocket("**/api/v1/ws", (ws) => {
      const server = ws.connectToServer();
      sendToPage = (message) => ws.send(message);
      server.onMessage((message) => {
        if (typeof message === "string") {
          try {
            const parsed = JSON.parse(message);
            if (parsed.type === "snapshot") {
              parsed.approval_rules = [
                savedRule("saved-1", "gh: GitHub token"),
              ];
              ws.send(JSON.stringify(parsed));
              return;
            }
          } catch { /* not JSON */ }
        }
        ws.send(message);
      });
    });

    await page.goto(await backend.generateLoginURL());
    await expect(page.getByText("Saved Approvals (1)")).toBeVisible();
    await expect(page.getByText("gh: GitHub token")).toBeVisible();

    sendToPage!(JSON.stringify({
      type: "approval_rule_added",
      approval_rule: savedRule("saved-2", "gh: Work token"),
    }));
    await expect(page.getByText("Saved Approvals (2)")).toBeVisible();
    await expect(page.getByText("gh: Work token")).toBeVisible();

    sendToPage!(JSON.stringify({
      type: "approval_rule_updated",
      approval_rule: {
        ...savedRule("saved-2", "gh: Updated work token"),
        enabled: false,
      },
    }));
    await expect(page.getByText("gh: Updated work token")).toBeVisible();
    await expect(page.getByText("disabled")).toBeVisible();

    sendToPage!(
      JSON.stringify({ type: "approval_rule_removed", id: "saved-1" }),
    );
    await expect(page.getByText("gh: GitHub token")).not.toBeVisible();
    await expect(page.getByText("Saved Approvals (1)")).toBeVisible();
  });

  test("creates only from an eligible manual approval after exact-scope confirmation", async ({ page }) => {
    await injectSnapshot(page, (snapshot) => {
      snapshot.approval_rules = [];
      snapshot.history = [
        historyEntry("eligible-1"),
        historyEntry("denied-1", "denied"),
        historyEntry("search-1", "approved", { type: "search", items: [] }),
      ];
    });

    let requestBody: unknown;
    let revokedRuleId: string | undefined;
    await page.route("**/api/v1/approval-rules/from-request", async (route) => {
      requestBody = route.request().postDataJSON();
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(savedRule("created-1", "gh: GitHub token")),
      });
    });
    await page.route("**/api/v1/approval-rules/created-1", async (route) => {
      revokedRuleId = route.request().url().split("/").at(-1);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "deleted" }),
      });
    });

    await page.goto(await backend.generateLoginURL());
    await expect(
      page.getByRole("button", { name: "Always approve exact access" }),
    ).toHaveCount(1);
    await page.getByRole("button", { name: "Always approve exact access" })
      .click();

    await expect(page.getByText("Always approve this exact access?"))
      .toBeVisible();
    const scope = page.locator(".approval-confirmation");
    await expect(scope.getByText("/usr/bin/gh", { exact: true })).toBeVisible();
    await expect(scope.getByText("login", { exact: true })).toBeVisible();
    await expect(scope.getByText("github.com", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Save exact approval" }).click();
    expect(requestBody).toEqual({ request_id: "eligible-1" });
    await expect(page.getByText("Saved Approvals (1)")).toBeVisible();
    const revokeButton = page.getByRole("button", {
      name: "Revoke saved rule",
    });
    await expect(revokeButton).toBeVisible();

    await revokeButton.click();
    expect(revokedRuleId).toBe("created-1");
    await expect(page.getByText("Saved Approvals (0)")).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Always approve exact access" }),
    ).toBeEnabled();
  });

  test("warns when an interpreter rule relies on argv", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    const entry = historyEntry("python-1");
    entry.request.sender_info.process_chain = [{
      name: "python3",
      pid: 1234,
      exe: "/usr/bin/python3",
      args: ["python3", "scripts/read-token.py"],
      cwd: "/home/test/project",
    }];
    await injectSnapshot(page, (snapshot) => {
      snapshot.approval_rules = [];
      snapshot.history = [entry];
    });

    await page.goto(await backend.generateLoginURL());
    await page.getByRole("button", { name: "Always approve exact access" })
      .click();

    const scope = page.locator(".approval-confirmation");
    await expect(scope.getByText("scripts/read-token.py", { exact: true }))
      .toBeVisible();
    await expect(scope.getByText("/home/test/project", { exact: true }))
      .toBeVisible();
    await expect(page.getByText(/argv is advisory and can be rewritten/))
      .toBeVisible();
  });

  test("shows managed attribution and keeps a rule visible when deletion fails", async ({ page }) => {
    await injectSnapshot(page, (snapshot) => {
      snapshot.approval_rules = [savedRule("saved-1", "gh: GitHub token")];
      snapshot.history = [historyEntry("automatic-1", "auto_approved", {
        attribution: {
          source: "managed_rule",
          rule_id: "saved-1",
          rule_name: "gh: GitHub token",
          action: "approve",
        },
      })];
    });
    await page.route("**/api/v1/approval-rules/saved-1", async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "storage unavailable" }),
      });
    });

    await page.goto(await backend.generateLoginURL());
    await expect(page.getByText("Saved approval: gh: GitHub token"))
      .toBeVisible();
    await page.getByRole("button", {
      name: "Remove saved approval gh: GitHub token",
    }).click();

    await expect(page.getByRole("alert")).toHaveText("storage unavailable");
    await expect(page.getByText("gh: GitHub token").first()).toBeVisible();
    await expect(page.getByText("Saved Approvals (1)")).toBeVisible();
  });
});
