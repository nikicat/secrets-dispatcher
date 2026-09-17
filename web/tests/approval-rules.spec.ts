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

function approvalRule(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    name: "Browser token",
    enabled: true,
    request_types: ["get_secret"],
    process: { exe: "/usr/bin/browser", direct: true },
    secret: { collection: "login", attributes: { service: "example" } },
    created_at: now,
    updated_at: now,
    ...overrides,
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

function pendingRequest() {
  return {
    id: "pending-save-source",
    client: "local",
    items: [{
      path: "/org/freedesktop/secrets/collection/login/item1",
      label: "Pending token",
      attributes: { service: "example" },
    }],
    session: "/org/freedesktop/secrets/session/1",
    created_at: now,
    expires_at: new Date(Date.now() + 300_000).toISOString(),
    type: "get_secret",
    sender_info: {
      sender: ":1.10",
      pid: 1010,
      uid: 1000,
      user_name: "testuser",
      invoker_name: "browser",
      process_chain: [{
        name: "browser",
        pid: 1010,
        exe: "/usr/bin/browser",
      }],
    },
  };
}

test("pending Save rule does not resolve the request", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [pendingRequest()];
    snapshot.approval_rules = [];
    snapshot.auto_approve_rules = [];
  });

  let requestBody: unknown;
  await page.route("**/api/v1/approval-rules/from-request", async (route) => {
    requestBody = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(approvalRule("saved-from-pending")),
    });
  });

  await page.goto(await backend.generateLoginURL());
  const card = page.locator(".card--get_secret");
  await card.getByRole("button", { name: "Save rule" }).click();

  const confirmation = card.locator(".save-rule-confirmation");
  await expect(confirmation.getByText("Save approval rule?")).toBeVisible();
  await expect(confirmation.getByText("/usr/bin/browser", { exact: true }))
    .toBeVisible();
  await expect(confirmation.getByText("get_secret", { exact: true }))
    .toBeVisible();
  await expect(confirmation.getByText("login", { exact: true })).toBeVisible();
  expect(requestBody).toBeUndefined();
  await card.getByRole("button", { name: "Create saved rule" }).click();

  expect(requestBody).toEqual({ request_id: "pending-save-source" });
  await expect(card.locator(".item-summary")).toHaveText("Pending token");
  await expect(card.getByRole("button", { name: "Approve once" }))
    .toBeVisible();
  await expect(page.getByText("Saved Approvals (1)")).toBeVisible();
});

test("focused Admin save previews scope and leaves the request pending", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [pendingRequest()];
    snapshot.approval_rules = [];
    snapshot.auto_approve_rules = [];
  });
  let posts = 0;
  await page.route("**/api/v1/approval-rules/from-request", async (route) => {
    posts++;
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(approvalRule("focused-saved")),
    });
  });

  const loginURL = await backend.generateLoginURL();
  await page.goto(`${loginURL}&request=pending-save-source`);
  const card = page.locator(".card--get_secret");
  await card.getByRole("button", { name: "Save rule" }).click();
  await expect(card.getByText("Direct request process only")).toBeVisible();
  await expect(card.getByText("Every item in a request must match"))
    .toBeVisible();
  await card.getByRole("button", { name: "Create saved rule" }).click();

  expect(posts).toBe(1);
  await expect(card.locator(".item-summary")).toHaveText("Pending token");
  await expect(page.locator(".sidebar")).not.toBeAttached();
});

test("focused Admin disables saving when direct process scope is unavailable", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    const request = pendingRequest();
    request.sender_info.process_chain = [];
    snapshot.requests = [request];
  });

  const loginURL = await backend.generateLoginURL();
  await page.goto(`${loginURL}&request=pending-save-source`);
  await expect(page.getByRole("button", { name: "Save rule" })).toBeDisabled();
  await expect(page.getByText("Save approval rule?")).not.toBeVisible();
});

test("temporary rules can be persisted and removed", async ({ page }) => {
  const temporaryRule = {
    id: "temporary-1",
    invoker_name: "browser",
    invoker_exe: "/usr/bin/browser",
    request_type: "get_secret",
    collection: "login",
    attributes: { service: "example" },
    expires_at: new Date(Date.now() + 120_000).toISOString(),
  };
  const removableRule = {
    ...temporaryRule,
    id: "temporary-2",
    invoker_name: "terminal",
    invoker_exe: "/usr/bin/terminal",
    attributes: { service: "shell" },
  };
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [];
    snapshot.approval_rules = [];
    snapshot.auto_approve_rules = [temporaryRule, removableRule];
  });

  let persisted = false;
  let removed = false;
  await page.route(
    "**/api/v1/auto-approve/temporary-1/persist",
    async (route) => {
      persisted = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(approvalRule("permanent-1")),
      });
    },
  );
  await page.route("**/api/v1/auto-approve/temporary-2", async (route) => {
    removed = true;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "removed" }),
    });
  });

  await page.goto(await backend.generateLoginURL());
  const temporary = page.locator(".rules-subsection").filter({
    has: page.getByRole("heading", { name: "Temporary Rules" }),
  });
  const persistRow = temporary.locator(".rule-entry").filter({
    hasText: "browser",
  });
  const removeRow = temporary.locator(".rule-entry").filter({
    hasText: "terminal",
  });
  await persistRow.getByRole("button", { name: "Save" }).click();
  expect(persisted).toBe(true);
  await expect(page.getByText("Browser token")).toBeVisible();
  await expect(persistRow).not.toBeVisible();

  await removeRow.getByRole("button", {
    name: "Remove temporary rule temporary-2",
  }).click();
  expect(removed).toBe(true);
  await expect(removeRow).not.toBeVisible();
});

test("temporary GPG rules cannot be persisted", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.auto_approve_rules = [{
      id: "temporary-gpg",
      invoker_name: "git",
      invoker_exe: "/usr/bin/git",
      request_type: "gpg_sign",
      collection: "",
      attributes: {},
      expires_at: new Date(Date.now() + 120_000).toISOString(),
    }];
    snapshot.approval_rules = [];
  });

  await page.goto(await backend.generateLoginURL());
  const temporary = page.locator(".rules-subsection").filter({
    has: page.getByRole("heading", { name: "Temporary Rules" }),
  });
  await expect(temporary).toContainText("git");
  await expect(temporary.getByRole("button", { name: "Save", exact: true }))
    .toHaveCount(0);
});

test("temporary persist ignores double submission while in flight", async ({ page }) => {
  const temporaryRule = {
    id: "temporary-delayed",
    invoker_name: "browser",
    invoker_exe: "/opt/browser/browser",
    request_type: "get_secret",
    collection: "login",
    attributes: { service: "example" },
    expires_at: new Date(Date.now() + 120_000).toISOString(),
  };
  await injectSnapshot(page, (snapshot) => {
    snapshot.auto_approve_rules = [temporaryRule];
    snapshot.approval_rules = [];
  });

  let posts = 0;
  let release!: () => void;
  const delayed = new Promise<void>((resolve) => release = resolve);
  await page.route(
    "**/api/v1/auto-approve/temporary-delayed/persist",
    async (route) => {
      posts++;
      await delayed;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(approvalRule("delayed-rule")),
      });
    },
  );

  await page.goto(await backend.generateLoginURL());
  const save = page.getByRole("button", { name: "Save", exact: true });
  await save.evaluate((button: HTMLButtonElement) => {
    button.click();
    button.click();
  });
  await expect(page.getByRole("button", { name: "Saving..." })).toBeDisabled();
  expect(posts).toBe(1);
  release();
  await expect(page.getByText("Browser token")).toBeVisible();
});

test("only eligible history resolutions offer saved rules", async ({ page }) => {
  const history = [
    "approved",
    "cancelled",
    "auto_approved",
    "denied",
    "expired",
    "ignored",
  ].map((resolution, index) => ({
    request: { ...pendingRequest(), id: `history-${index}` },
    resolution,
    resolved_at: now,
  }));
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [];
    snapshot.history = history;
    snapshot.approval_rules = [];
    snapshot.auto_approve_rules = [];
  });

  await page.goto(await backend.generateLoginURL());
  await expect(
    page.getByRole("button", { name: "Always approve exact access" }),
  ).toHaveCount(3);
});

test("a disabled permanent rule can be created from validated JSON", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [];
    snapshot.auto_approve_rules = [];
    snapshot.approval_rules = [];
  });

  let requestBody: Record<string, unknown> | undefined;
  await page.route("**/api/v1/approval-rules", async (route) => {
    requestBody = route.request().postDataJSON() as Record<string, unknown>;
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(approvalRule("created-1", requestBody)),
    });
  });

  await page.goto(await backend.generateLoginURL());
  await page.getByRole("button", { name: "New rule" }).click();
  const editor = page.getByRole("dialog", { name: "Create approval rule" });
  const textarea = editor.getByRole("textbox", { name: "Approval rule JSON" });
  const draft = JSON.parse(await textarea.inputValue());
  draft.name = "Created from JSON";
  draft.enabled = false;
  await textarea.fill(JSON.stringify(draft, null, 2));
  await expect(editor.getByText("Disabled (will not approve until enabled)"))
    .toBeVisible();
  await editor.getByRole("button", { name: "Create rule" }).click();

  expect(requestBody?.name).toBe("Created from JSON");
  expect(requestBody?.enabled).toBe(false);
  await expect(page.getByText("Created from JSON")).toBeVisible();
});

test("JSON editor rejects unknown top-level and nested matcher fields", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.approval_rules = [];
  });
  let posts = 0;
  await page.route("**/api/v1/approval-rules", async (route) => {
    posts++;
    await route.abort();
  });

  await page.goto(await backend.generateLoginURL());
  await page.getByRole("button", { name: "New rule" }).click();
  const editor = page.getByRole("dialog", { name: "Create approval rule" });
  const textarea = editor.getByRole("textbox", { name: "Approval rule JSON" });
  const draft = JSON.parse(await textarea.inputValue());
  draft.requst_types = draft.request_types;
  delete draft.request_types;
  await textarea.fill(JSON.stringify(draft));
  await expect(editor.getByText("Unknown rule field: requst_types"))
    .toBeVisible();

  delete draft.requst_types;
  draft.request_types = ["get_secret"];
  draft.process = { executable: "/usr/bin/example", direct: true };
  await textarea.fill(JSON.stringify(draft));
  await expect(editor.getByText("Unknown process field: executable"))
    .toBeVisible();
  await editor.getByRole("button", { name: "Create rule" }).click();
  await expect(editor.getByRole("alert")).toHaveText(
    "Unknown process field: executable",
  );
  expect(posts).toBe(0);
});

test("JSON creation ignores double submission while in flight", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.approval_rules = [];
  });
  let posts = 0;
  let release!: () => void;
  const delayed = new Promise<void>((resolve) => release = resolve);
  await page.route("**/api/v1/approval-rules", async (route) => {
    posts++;
    await delayed;
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(approvalRule("delayed-create")),
    });
  });

  await page.goto(await backend.generateLoginURL());
  await page.getByRole("button", { name: "New rule" }).click();
  const create = page.getByRole("button", { name: "Create rule" });
  await create.evaluate((button: HTMLButtonElement) => {
    button.click();
    button.click();
  });
  await expect(page.getByRole("button", { name: "Saving..." })).toBeDisabled();
  expect(posts).toBe(1);
  release();
  await expect(page.getByText("Browser token")).toBeVisible();
});

test("disabled matching rules do not produce a misleading duplicate claim", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [];
    snapshot.history = [{
      request: { ...pendingRequest(), id: "disabled-match-history" },
      resolution: "approved",
      resolved_at: now,
    }];
    snapshot.approval_rules = [approvalRule("disabled-match", {
      enabled: false,
    })];
  });

  await page.goto(await backend.generateLoginURL());
  await expect(
    page.getByRole("button", { name: "Always approve exact access" }),
  )
    .toBeEnabled();
  await expect(page.getByText("Exact approval saved")).not.toBeVisible();
  await expect(page.getByText("Saved rule active")).not.toBeVisible();
});

test("permanent rules can be disabled, enabled, edited, and deleted", async ({ page }) => {
  await injectSnapshot(page, (snapshot) => {
    snapshot.requests = [];
    snapshot.auto_approve_rules = [];
    snapshot.approval_rules = [approvalRule("managed-1")];
  });

  const methods: string[] = [];
  await page.route("**/api/v1/approval-rules/managed-1", async (route) => {
    methods.push(route.request().method());
    if (route.request().method() === "DELETE") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "removed" }),
      });
      return;
    }
    const body = route.request().postDataJSON() as Record<string, unknown>;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(approvalRule("managed-1", body)),
    });
  });

  await page.goto(await backend.generateLoginURL());
  const row = page.locator(".rule-entry").filter({ hasText: "Browser token" });

  await row.getByRole("button", { name: "Disable" }).click();
  await expect(row.getByText("disabled")).toBeVisible();
  await row.getByRole("button", { name: "Enable" }).click();
  await expect(row.getByText("saved")).toBeVisible();

  await row.getByRole("button", { name: "Edit" }).click();
  const editor = page.getByRole("dialog", { name: "Edit approval rule" });
  const textarea = editor.getByRole("textbox", { name: "Approval rule JSON" });
  const edited = JSON.parse(await textarea.inputValue());
  edited.name = "Edited browser token";
  await textarea.fill(JSON.stringify(edited, null, 2));
  await editor.getByRole("button", { name: "Save changes" }).click();
  await expect(row.getByText("Edited browser token")).toBeVisible();

  await row.getByTitle("Delete saved rule").click();
  await expect(row).not.toBeVisible();
  expect(methods).toEqual(["PUT", "PUT", "PUT", "DELETE"]);
});
