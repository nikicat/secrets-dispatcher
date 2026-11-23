import { expect, type Page, test } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";
import type { WSSnapshotMessage } from "../src/lib/types.ts";

// These tests verify trust rules (config-defined persistent rules) display in the WebUI.

let backend: TestBackend;

/**
 * Opens the app logged in, with the given fields merged into its snapshot, and
 * expands the Trust Rules section, which starts collapsed.
 */
async function openTrustRules(
  page: Page,
  fields: Partial<WSSnapshotMessage>,
): Promise<void> {
  await backend.openWith(page, {
    rewrite: (snapshot) => ({ ...snapshot, ...fields }),
  });
  await page.getByText("Trust Rules").click();
}

test.beforeAll(async () => {
  backend = await startTestBackend();
});

test.afterAll(async () => {
  await backend.cleanup();
});

test.describe("Trust Rules WebSocket", () => {
  test("snapshot includes trust_rules field", async ({ page }) => {
    let snapshotMsg: WSSnapshotMessage | null = null;

    await backend.openWith(page, {
      rewrite: (s) => {
        snapshotMsg = s;
        return s;
      },
    });
    await expect(page.getByText("No pending requests")).toBeVisible();

    expect(snapshotMsg).not.toBeNull();
    expect(snapshotMsg).toHaveProperty("trust_rules");
    expect(Array.isArray(snapshotMsg!.trust_rules)).toBe(true);
  });

  test("trust rules from snapshot appear in main content", async ({ page }) => {
    await openTrustRules(page, {
      trust_rules: [
        {
          name: "Allow GitHub CLI",
          action: "approve",
          request_types: ["search", "get_secret"],
          process: { name: "gh" },
          client: "laptop",
          secret: { collection: "default" },
        },
      ],
    });

    await expect(page.getByText("Allow GitHub CLI")).toBeVisible();
    await expect(page.getByText("config")).toBeVisible();
    const props = page.locator(".rule-entry .props-table");
    await expect(
      props.getByRole("row", { name: "client laptop" }),
    ).toBeVisible();
  });

  test("ignore action trust rule displays correctly", async ({ page }) => {
    await openTrustRules(page, {
      trust_rules: [
        {
          name: "Ignore Chrome dummy",
          action: "ignore",
          client: "local",
          request_types: ["write"],
          secret: {
            attributes: {
              "xdg:schema": "_chrome_dummy_schema_for_unlocking",
            },
          },
        },
      ],
    });

    await expect(page.getByText("Ignore Chrome dummy")).toBeVisible();
    // Should show "Ignore Write" for ignore action with write request type
    await expect(page.getByText("Ignore Write")).toBeVisible();
  });

  test("each trust rule shows its own action", async ({ page }) => {
    await openTrustRules(page, {
      trust_rules: [
        {
          name: "Approve gh",
          action: "approve",
          client: "local",
          request_types: ["search"],
        },
        {
          name: "Ignore Chrome",
          action: "ignore",
          client: "local",
          request_types: ["write"],
        },
        {
          name: "Deny everyone",
          action: "deny",
          client: "*",
          request_types: ["delete"],
        },
      ],
    });

    const badge = (name: string) =>
      page.locator(".rule-entry", { hasText: name }).locator(".history-type");
    await expect(badge("Approve gh")).toHaveText("Approve Search");
    await expect(badge("Ignore Chrome")).toHaveText("Ignore Write");
    await expect(badge("Deny everyone")).toHaveText("Deny Delete");
    await expect(badge("Deny everyone")).toHaveClass(/rule-action--deny/);
  });

  test("multiple trust rules render in order", async ({ page }) => {
    await openTrustRules(page, {
      trust_rules: [
        {
          name: "Rule One",
          action: "approve",
          client: "local",
          request_types: ["search"],
          process: { name: "gh" },
        },
        {
          name: "Rule Two",
          action: "ignore",
          client: "local",
          request_types: ["write"],
          process: { exe: "/opt/google/chrome/chrome" },
        },
      ],
    });

    await expect(page.getByText("Rule One")).toBeVisible();
    await expect(page.getByText("Rule Two")).toBeVisible();
  });

  test("trust rules section hidden when no rules", async ({ page }) => {
    await backend.openWith(page, {
      rewrite: (s) => ({
        ...s,
        trust_rules: [],
        auto_approve_rules: [],
        trusted_signers: [],
      }),
    });

    await expect(page.getByText("No pending requests")).toBeVisible();
    await expect(page.getByText("Trust Rules")).not.toBeVisible();
  });

  test("trust rules in main content coexist with auto-approve rules in sidebar", async ({
    page,
  }) => {
    const expiresAt = new Date(Date.now() + 90_000).toISOString();

    await openTrustRules(page, {
      trust_rules: [
        {
          name: "Config rule",
          action: "approve",
          client: "local",
          request_types: ["search"],
          process: { name: "gh" },
        },
      ],
      auto_approve_rules: [
        {
          id: "temp-rule-1",
          invoker_name: "temp-invoker",
          invoker_exe: "/usr/bin/temp-invoker",
          request_type: "get_secret",
          collection: "default",
          expires_at: expiresAt,
        },
      ],
      trusted_signers: [],
    });
    await expect(page.getByText("Config rule")).toBeVisible();

    // Auto-approve rules in sidebar
    await expect(page.getByText("Auto-Approve Rules")).toBeVisible();
    await expect(page.getByText("temp-invoker")).toBeVisible();
  });
});
