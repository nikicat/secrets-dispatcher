import { Buffer } from "node:buffer";
import { type ChildProcess, spawn } from "node:child_process";
import { once } from "node:events";
import process from "node:process";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { createInterface } from "node:readline";
import { createHmac, randomBytes } from "node:crypto";
import type { AddressInfo } from "node:net";
import { preview, type PreviewServer } from "vite";
import type { Page, WebSocketRoute } from "@playwright/test";
import type {
  PendingRequest,
  SenderInfo,
  WSMessage,
  WSSnapshotMessage,
} from "../../src/lib/types.ts";

// The suite runs with cwd web/. The root is not derived from `import.meta.url`
// or `__dirname`, because Playwright may load this file in either module format.
export const PROJECT_ROOT = resolve("..");
if (!existsSync(join(PROJECT_ROOT, "go.mod"))) {
  throw new Error(
    `expected the repo root at ${PROJECT_ROOT} (cwd must be web/); ` +
      "launch the E2E suite via `pnpm test:e2e`",
  );
}
const BINARY_PATH = join(PROJECT_ROOT, ".build", "secrets-dispatcher");

export interface TestBackend {
  url: string;
  cleanup: () => Promise<void>;
  generateLoginURL: () => Promise<string>;
  getAuthToken: () => Promise<string>;
  restart: (options?: BackendOptions) => Promise<void>;
}

/** Settings that may change on each start of the backend, restarts included. */
export interface BackendOptions {
  /** Overrides the build version the backend reports. */
  version?: string;
}

/**
 * Generate a JWT token for testing.
 * Matches the format expected by the backend. Exported so specs never
 * hand-roll their own (a drifted copy is how the missing-jti breakage
 * slipped past the suite).
 */
export function generateJWT(secret: string): string {
  const header = Buffer.from(
    JSON.stringify({ alg: "HS256", typ: "JWT" }),
  ).toString("base64url");

  const now = Math.floor(Date.now() / 1000);
  const claims = Buffer.from(
    JSON.stringify({
      iat: now,
      exp: now + 300, // 5 minutes
      // Login JWTs are single-use, keyed by jti: without a fresh nonce the
      // backend rejects every login after the first as a replay.
      jti: randomBytes(16).toString("hex"),
    }),
  ).toString("base64url");

  const signingInput = `${header}.${claims}`;
  const signature = createHmac("sha256", secret)
    .update(signingInput)
    .digest("base64url");

  return `${signingInput}.${signature}`;
}

/**
 * Build a raw git commit object from display fields, the way a real thin
 * client feeds one to gpg. The daemon re-derives author/committer/message/
 * parent from these bytes (WYSIWYS) and discards the client's display fields,
 * so tests must supply a commit_object consistent with their expectations.
 */
export function buildCommitObject(info: Record<string, unknown>): string {
  const lines = ["tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904"];
  if (typeof info.parent_hash === "string" && info.parent_hash !== "") {
    lines.push(`parent ${info.parent_hash}`);
  }
  lines.push(`author ${info.author ?? ""}`);
  lines.push(`committer ${info.committer ?? ""}`);
  const msg = typeof info.commit_msg === "string" ? info.commit_msg : "";
  return `${lines.join("\n")}\n\n${msg}\n`;
}

/** Fields of a pending request that override the test defaults. */
export type RequestFields = Omit<Partial<PendingRequest>, "sender_info"> & {
  sender_info?: Partial<SenderInfo>;
};

/**
 * Returns a pending request with test defaults, overridden by the given fields.
 * A partial `sender_info` is merged over the default sender.
 */
export function pendingRequest({
  sender_info,
  ...fields
}: RequestFields): PendingRequest {
  return {
    id: "test-request",
    client: "local",
    type: "get_secret",
    items: [
      {
        path: "/org/freedesktop/secrets/collection/default/1",
        label: "Test Secret",
        attributes: {},
      },
    ],
    session: "/org/freedesktop/secrets/session/s1",
    sender_info: {
      sender: ":1.100",
      pid: 1000,
      uid: 1000,
      user_name: "testuser",
      invoker_name: "test.service",
      ...sender_info,
    },
    created_at: new Date().toISOString(),
    expires_at: new Date(Date.now() + 300_000).toISOString(),
    ...fields,
  };
}

/** How `routeSnapshot` changes what the backend sends the page. */
export interface SnapshotEdit {
  /** Replaces the backend's snapshot. */
  rewrite?: (snapshot: WSSnapshotMessage) => WSSnapshotMessage;
  /** Messages the page receives right after the snapshot. */
  after?: WSMessage[];
}

/** Sends a message to the page on its latest WebSocket connection. */
export type SendToPage = (message: WSMessage) => void;

/**
 * Connects the page's WebSocket to the backend, with each snapshot rewritten
 * by `rewrite` and followed by `after`. Every other message passes unchanged.
 * Resolves to a function that sends further messages to the page.
 */
export async function routeSnapshot(
  page: Page,
  { rewrite = (snapshot) => snapshot, after = [] }: SnapshotEdit = {},
): Promise<SendToPage> {
  let latest: WebSocketRoute | undefined;
  await page.routeWebSocket("**/api/v1/ws", (ws) => {
    latest = ws;
    const server = ws.connectToServer();
    server.onMessage((message) => {
      const snapshot = parseSnapshot(message);
      if (!snapshot) {
        ws.send(message);
        return;
      }
      ws.send(JSON.stringify(rewrite(snapshot)));
      for (const next of after) ws.send(JSON.stringify(next));
    });
  });
  return (message) => {
    if (!latest) throw new Error("the page has not opened its WebSocket");
    latest.send(JSON.stringify(message));
  };
}

/** Returns `message` as a snapshot, or null for any other frame. */
function parseSnapshot(message: string | Buffer): WSSnapshotMessage | null {
  if (typeof message !== "string") return null;
  try {
    const parsed = JSON.parse(message) as WSMessage;
    return parsed.type === "snapshot" ? parsed : null;
  } catch {
    return null; // not JSON
  }
}

/**
 * Starts Vite's preview server on an OS-assigned port. It serves the frontend
 * from web/dist/ and proxies /api/ requests, WebSocket upgrades included, to
 * the backend at backendUrl.
 */
function startFrontend(backendUrl: URL): Promise<PreviewServer> {
  return preview({
    root: join(PROJECT_ROOT, "web"),
    configFile: false,
    logLevel: "silent",
    preview: {
      host: "127.0.0.1",
      port: 0,
      proxy: {
        "/api": { target: backendUrl.origin, ws: true },
      },
    },
  });
}

/**
 * Writes a minimal config that isolates tests from the user's real config and
 * returns its path. The session_bus downstream creates a staticProvider, so
 * the WS snapshot includes a "client connected" entry that many tests expect.
 */
async function writeTestConfig(stateDir: string): Promise<string> {
  const configPath = join(stateDir, "config.yaml");
  await writeFile(
    configPath,
    [
      "serve:",
      "    upstream:",
      "        type: socket",
      "        path: /dev/null",
      "    downstream:",
      "        - type: session_bus",
      "",
    ].join("\n"),
  );
  return configPath;
}

/** The flags and paths that every launch of one test backend shares. */
interface BackendLaunch {
  configPath: string;
  stateDir: string;
  extraArgs: readonly string[];
}

/**
 * Where a started backend serves and where it keeps its auth cookie, as its
 * startup log reports them.
 */
interface BackendReady {
  url: URL;
  cookieFile: string;
}

const READY_MESSAGE = "API server started";
const READY_TIMEOUT_MS = 5000;
const STOP_GRACE_MS = 2000;

/** Builds the argv that serves the API alone on listenAddr. */
function backendArgs(listenAddr: string, launch: BackendLaunch): string[] {
  return [
    "serve",
    "--api-only",
    "--notifications=false",
    // JSON records parse without the colour codes of the text format.
    "--log-format",
    "json",
    "--config",
    launch.configPath,
    "--state-dir",
    launch.stateDir,
    "--listen",
    listenAddr,
    ...launch.extraArgs,
  ];
}

/**
 * Parses the backend's startup record from one stderr line. Returns undefined
 * for any other line.
 */
function parseReadyLine(line: string): BackendReady | undefined {
  let record: { msg?: unknown; url?: unknown; cookie_file?: unknown };
  try {
    record = JSON.parse(line);
  } catch {
    return undefined;
  }
  if (
    record.msg !== READY_MESSAGE ||
    typeof record.url !== "string" ||
    typeof record.cookie_file !== "string"
  ) {
    return undefined;
  }
  return {
    url: new URL(record.url),
    cookieFile: record.cookie_file,
  };
}

/**
 * Resolves with where the backend serves once it logs that it started. Errors
 * when the process fails to spawn, exits first, or is not ready within
 * READY_TIMEOUT_MS.
 */
async function waitForReady(child: ChildProcess): Promise<BackendReady> {
  const signal = AbortSignal.timeout(READY_TIMEOUT_MS);
  const lines = createInterface({ input: child.stderr!, signal });
  let spawnError: Error | undefined;
  child.on("error", (err) => {
    spawnError = err;
    lines.close();
  });
  const output: string[] = [];
  try {
    for await (const line of lines) {
      const ready = parseReadyLine(line);
      if (ready) return ready;
      output.push(line);
    }
  } finally {
    lines.close();
    // Keep draining stderr so the backend never blocks on a full pipe.
    child.stderr!.resume();
  }
  if (spawnError) throw spawnError;
  const reason = signal.aborted
    ? "Timeout waiting for backend to start"
    : "Backend exited before it was ready";
  throw new Error(`${reason}: ${output.join("\n")}`);
}

/** A running backend process that has reported where it serves. */
class BackendProcess {
  private constructor(
    private readonly child: ChildProcess,
    readonly url: URL,
    readonly cookieFile: string,
  ) {}

  /**
   * Starts a backend on listenAddr and resolves once it is ready. Errors as
   * waitForReady does, after killing the process.
   */
  static async start(
    listenAddr: string,
    { version }: BackendOptions,
    launch: BackendLaunch,
  ): Promise<BackendProcess> {
    const child = spawn(BINARY_PATH, backendArgs(listenAddr, launch), {
      stdio: ["ignore", "pipe", "pipe"],
      env: version
        ? { ...process.env, TEST_BUILD_VERSION: version }
        : process.env,
    });
    try {
      const { url, cookieFile } = await waitForReady(child);
      return new BackendProcess(child, url, cookieFile);
    } catch (err) {
      child.kill("SIGKILL");
      throw err;
    }
  }

  /**
   * Sends SIGTERM, then SIGKILL after STOP_GRACE_MS, and resolves once the
   * process exits.
   */
  async stop(): Promise<void> {
    const child = this.child;
    if (!child.pid || child.exitCode !== null || child.signalCode !== null) {
      return;
    }
    const exited = once(child, "exit");
    child.kill("SIGTERM");
    const killTimer = setTimeout(() => child.kill("SIGKILL"), STOP_GRACE_MS);
    try {
      await exited;
    } finally {
      clearTimeout(killTimer);
    }
  }
}

/**
 * Starts an isolated backend in API-only mode behind a Vite preview server.
 * Returns a handle that mints login URLs, restarts the backend and cleans
 * everything up.
 */
export async function startTestBackend(
  options: BackendOptions & Partial<Pick<BackendLaunch, "extraArgs">> = {},
): Promise<TestBackend> {
  const stateDir = join(
    tmpdir(),
    `secrets-dispatcher-test-${randomBytes(8).toString("hex")}`,
  );
  await mkdir(stateDir, { recursive: true });
  const launch: BackendLaunch = {
    configPath: await writeTestConfig(stateDir),
    stateDir,
    extraArgs: options.extraArgs ?? [],
  };

  // Port 0 lets the OS pick; restarts reuse the port so the proxy reconnects.
  let backend = await BackendProcess.start("127.0.0.1:0", options, launch);

  const frontend = await startFrontend(backend.url);
  const url = `http://localhost:${(frontend.httpServer.address() as AddressInfo).port}`;

  const getAuthToken = async (): Promise<string> =>
    (await readFile(backend.cookieFile, "utf-8")).trim();

  return {
    url,
    getAuthToken,
    generateLoginURL: async () =>
      `${url}/?token=${generateJWT(await getAuthToken())}`,
    restart: async (restartOptions = {}) => {
      await backend.stop();
      backend = await BackendProcess.start(
        backend.url.host,
        restartOptions,
        launch,
      );
    },
    cleanup: async () => {
      await frontend.close();
      await backend.stop();
      await rm(stateDir, { recursive: true, force: true });
    },
  };
}
