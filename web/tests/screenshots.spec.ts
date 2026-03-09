import { expect, test } from "@playwright/test";
import { startTestBackend, type TestBackend } from "./fixtures/test-utils.mts";
import { join } from "node:path";
import { mkdir } from "node:fs/promises";

// Screenshot generation test.
// Run: make screenshots
// Output: docs/screenshots/

const SCREENSHOT_DIR = join(__dirname, "..", "..", "docs", "screenshots");

let backend: TestBackend;

test.beforeAll(async () => {
  backend = await startTestBackend();
  await mkdir(SCREENSHOT_DIR, { recursive: true });
});

test.afterAll(async () => {
  await backend.cleanup();
});

// -- Fixture data --

const now = new Date().toISOString();
const later = new Date(Date.now() + 300_000).toISOString();

/** A pending secret access request from an AI agent with process chain. */
const secretRequest = {
  type: "request_created",
  request: {
    id: "req-screenshot-secret-001",
    client: "local",
    items: [
      {
        path: "/org/freedesktop/secrets/collection/login/github-token",
        label: "GitHub Personal Access Token",
        attributes: {
          "xdg:schema": "org.freedesktop.Secret.Generic",
          service: "github.com",
          username: "developer",
        },
      },
    ],
    session: "/org/freedesktop/secrets/session/s1",
    created_at: now,
    expires_at: later,
    type: "get_secret",
    sender_info: {
      sender: ":1.142",
      pid: 48210,
      uid: 1000,
      user_name: "dev",
      unit_name: "app-org.freedesktop.secrets.slice",
      process_chain: [
        {
          name: "secret-tool",
          pid: 48210,
          exe: "/usr/bin/secret-tool",
          args: ["secret-tool", "lookup", "service", "github.com"],
          cwd: "/home/dev/src/my-project",
        },
        {
          name: "node",
          pid: 48200,
          exe: "/usr/bin/node",
          args: ["node", "/home/dev/.local/lib/claude-code/main.js"],
          cwd: "/home/dev/src/my-project",
        },
        {
          name: "claude",
          pid: 48100,
          exe: "/usr/local/bin/claude",
          args: ["claude"],
          cwd: "/home/dev/src/my-project",
        },
      ],
    },
  },
};

/** A pending GPG commit signing request. */
const gpgSignRequest = {
  type: "request_created",
  request: {
    id: "req-screenshot-gpg-001",
    client: "gpg-sign",
    items: [],
    session: "",
    created_at: now,
    expires_at: later,
    type: "gpg_sign",
    sender_info: {
      sender: "local",
      pid: 49300,
      uid: 1000,
      user_name: "dev",
      unit_name: "",
      process_chain: [
        {
          name: "secrets-dispatcher-gpg",
          pid: 49300,
          exe: "/home/dev/.local/bin/secrets-dispatcher-gpg",
          args: [
            "secrets-dispatcher-gpg",
            "--status-fd=2",
            "-bsau",
            "A1B2C3D4",
          ],
          cwd: "/home/dev/src/my-project",
        },
        {
          name: "git",
          pid: 49290,
          exe: "/usr/bin/git",
          args: ["git", "commit", "-S", "-m", "feat: add OAuth2 login flow"],
          cwd: "/home/dev/src/my-project",
        },
      ],
    },
    gpg_sign_info: {
      repo_name: "my-project",
      commit_msg:
        "feat: add OAuth2 login flow\n\nImplement authorization code grant with PKCE.\nAdd refresh token rotation and session management.",
      author: "Alice Developer <alice@example.com> 1741500000 +0100",
      committer: "Alice Developer <alice@example.com> 1741500000 +0100",
      key_id: "A1B2C3D4",
      fingerprint: "8F3A 2B1C 4D5E 6F7A 8B9C  0D1E 2F3A 4B5C A1B2 C3D4",
      changed_files: [
        "internal/auth/oauth2.go",
        "internal/auth/oauth2_test.go",
        "internal/auth/session.go",
        "internal/config/config.go",
        "go.mod",
        "go.sum",
      ],
      parent_hash: "f7ca80c",
    },
  },
};

/** History entries showing a mix of resolutions. */
function makeHistory() {
  const entries = [];
  const resolutions = [
    {
      id: "hist-1",
      resolution: "approved",
      label: "Database Password (production)",
      unit: "deploy.service",
      collection: "deploy",
      type: "get_secret",
    },
    {
      id: "hist-2",
      resolution: "auto_approved",
      label: "GitHub Token",
      unit: "firefox.desktop",
      collection: "login",
      type: "get_secret",
    },
    {
      id: "hist-3",
      resolution: "auto_approved",
      label: "GitHub Token",
      unit: "firefox.desktop",
      collection: "login",
      type: "get_secret",
    },
    {
      id: "hist-4",
      resolution: "auto_approved",
      label: "GitHub Token",
      unit: "firefox.desktop",
      collection: "login",
      type: "get_secret",
    },
    {
      id: "hist-5",
      resolution: "denied",
      label: "AWS Secret Key",
      unit: "unknown-script.service",
      collection: "cloud",
      type: "get_secret",
    },
    {
      id: "hist-6",
      resolution: "ignored",
      label: "Chrome Safe Storage",
      unit: "chrome.desktop",
      collection: "default",
      type: "write",
    },
  ];

  for (const r of resolutions) {
    entries.push({
      request: {
        id: r.id,
        client: "local",
        items: [
          {
            path: `/org/freedesktop/secrets/collection/${r.collection}/1`,
            label: r.label,
            attributes: {},
          },
        ],
        session: "/org/freedesktop/secrets/session/1",
        created_at: now,
        expires_at: later,
        type: r.type,
        sender_info: {
          sender: ":1.100",
          pid: 10000,
          uid: 1000,
          user_name: "dev",
          unit_name: r.unit,
        },
      },
      resolution: r.resolution,
      resolved_at: now,
    });
  }
  return entries;
}

/** Trust rules to show in sidebar. */
const trustRules = [
  {
    name: "Firefox",
    action: "approve",
    request_types: ["get_secret"],
    process: { exe: "/usr/lib/firefox/firefox" },
  },
  {
    name: "Chrome probe",
    action: "ignore",
    request_types: ["write"],
    process: { exe: "*chrome*" },
  },
  {
    name: "Deploy scripts",
    action: "approve",
    request_types: ["get_secret"],
    process: { exe: "/usr/bin/ansible-playbook" },
    secret: { collection: "deploy" },
  },
];

// -- Screenshot tests --

test.describe("Screenshots", () => {
  test(
    "webui-overview: pending requests with history and trust rules",
    async ({ page }) => {
      // Intercept WebSocket to inject rich state.
      await page.routeWebSocket(`**/api/v1/ws`, (ws) => {
        const server = ws.connectToServer();
        server.onMessage((message) => {
          if (typeof message === "string") {
            try {
              const parsed = JSON.parse(message);
              if (parsed.type === "snapshot") {
                // Inject history and trust rules into snapshot.
                parsed.history = makeHistory();
                parsed.trust_rules = trustRules;
                ws.send(JSON.stringify(parsed));

                // Then inject pending requests.
                setTimeout(() => {
                  ws.send(JSON.stringify(secretRequest));
                }, 200);
                setTimeout(() => {
                  ws.send(JSON.stringify(gpgSignRequest));
                }, 400);
                return;
              }
            } catch {
              /* not JSON */
            }
          }
          ws.send(message);
        });
      });

      await page.setViewportSize({ width: 1280, height: 900 });

      const loginURL = await backend.generateLoginURL();
      await page.goto(loginURL);

      // Wait for both pending cards to appear.
      await expect(page.locator(".card")).toHaveCount(2, { timeout: 10_000 });

      // Wait for history to render.
      await expect(page.getByText("Recent Activity")).toBeVisible();

      // Expand the GPG sign commit body for a richer screenshot.
      const gpgCard = page.locator(".card--gpg_sign");
      const bodyToggle = gpgCard.locator(".commit-body-toggle summary");
      if (await bodyToggle.isVisible()) {
        await bodyToggle.click();
      }

      // Small pause for animations to settle.
      await page.waitForTimeout(500);

      await page.screenshot({
        path: join(SCREENSHOT_DIR, "webui-overview.png"),
        fullPage: false,
      });
    },
  );
});
