import { spawn, type ChildProcess } from "node:child_process";
import { readFile, rm, mkdir } from "node:fs/promises";
import { join, dirname } from "node:path";
import { tmpdir } from "node:os";
import { randomBytes, createHmac } from "node:crypto";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = join(__dirname, "..", "..", "..");
const BINARY_PATH = join(PROJECT_ROOT, "secrets-dispatcher");

export interface TestBackend {
  url: string;
  wsUrl: string;
  port: number;
  stateDir: string;
  process: ChildProcess;
  cleanup: () => Promise<void>;
  generateLoginURL: () => Promise<string>;
  getAuthToken: () => Promise<string>;
  restart: (options?: { version?: string }) => Promise<void>;
}

/**
 * Generate a JWT token for testing.
 * Matches the format expected by the backend.
 */
function generateJWT(secret: string): string {
  const header = Buffer.from(
    JSON.stringify({ alg: "HS256", typ: "JWT" }),
  ).toString("base64url");

  const now = Math.floor(Date.now() / 1000);
  const claims = Buffer.from(
    JSON.stringify({
      iat: now,
      exp: now + 300, // 5 minutes
    }),
  ).toString("base64url");

  const signingInput = `${header}.${claims}`;
  const signature = createHmac("sha256", secret)
    .update(signingInput)
    .digest("base64url");

  return `${signingInput}.${signature}`;
}

/**
 * Start a test instance of the secrets-dispatcher backend.
 * Creates an isolated config directory and starts the server in API-only mode.
 */
export async function startTestBackend(options?: { version?: string; extraArgs?: string[] }): Promise<TestBackend> {
  // Create temp directory for test
  const testId = randomBytes(8).toString("hex");
  const stateDir = join(tmpdir(), `secrets-dispatcher-test-${testId}`);
  await mkdir(stateDir, { recursive: true });

  // Build environment with optional version override
  const env: Record<string, string> = { ...process.env };
  if (options?.version) {
    env.TEST_BUILD_VERSION = options.version;
  }

  const extraArgs = options?.extraArgs ?? [];

  const spawnBackend = (listenAddr: string, spawnEnv: Record<string, string>): ChildProcess => {
    return spawn(
      BINARY_PATH,
      [
        "serve",
        "--api-only",
        "--notifications=false",
        "--state-dir",
        stateDir,
        "--listen",
        listenAddr,
        "--client",
        "test-client",
        ...extraArgs,
      ],
      {
        stdio: ["ignore", "pipe", "pipe"],
        env: spawnEnv,
        detached: false,
      },
    );
  };

  // Wait for the server to start and parse its actual listen port from log output.
  const waitForServer = async (p: ChildProcess): Promise<number> => {
    return new Promise<number>((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error("Timeout waiting for backend to start"));
      }, 5000);

      let output = "";
      p.stderr?.on("data", (data: Buffer) => {
        output += data.toString();
        if (output.includes("API server started")) {
          const match = output.match(/http:\/\/127\.0\.0\.1:(\d+)/);
          if (match) {
            clearTimeout(timeout);
            resolve(parseInt(match[1], 10));
          }
        }
      });

      p.on("error", (err) => {
        clearTimeout(timeout);
        reject(err);
      });

      p.on("exit", (code) => {
        if (code !== 0 && code !== null) {
          clearTimeout(timeout);
          reject(new Error(`Backend exited with code ${code}: ${output}`));
        }
      });
    });
  };

  const stopProcess = async (p: ChildProcess): Promise<void> => {
    if (!p.pid || p.exitCode !== null) return;

    return new Promise<void>((resolve) => {
      let killTimer: ReturnType<typeof setTimeout> | null = null;

      p.on("exit", () => {
        if (killTimer) clearTimeout(killTimer);
        resolve();
      });

      p.kill("SIGTERM");

      killTimer = setTimeout(() => {
        try { p.kill("SIGKILL"); } catch { /* already dead */ }
      }, 2000);
    });
  };

  // Start with port 0 — let the OS assign an available port
  let proc = spawnBackend("127.0.0.1:0", env);
  const actualPort = await waitForServer(proc);

  const url = `http://localhost:${actualPort}`;
  const wsUrl = `ws://localhost:${actualPort}/api/v1/ws`;

  const getAuthToken = async (): Promise<string> => {
    const cookiePath = join(stateDir, ".cookie");
    return (await readFile(cookiePath, "utf-8")).trim();
  };

  const generateLoginURL = async (): Promise<string> => {
    const token = await getAuthToken();
    const jwt = generateJWT(token);
    return `${url}/?token=${jwt}`;
  };

  const restart = async (restartOptions?: { version?: string }): Promise<void> => {
    await stopProcess(proc);

    const restartEnv: Record<string, string> = { ...process.env };
    if (restartOptions?.version) {
      restartEnv.TEST_BUILD_VERSION = restartOptions.version;
    }

    // Reuse the same port so the browser page can reconnect
    proc = spawnBackend(`127.0.0.1:${actualPort}`, restartEnv);
    await waitForServer(proc);
  };

  const cleanup = async (): Promise<void> => {
    await stopProcess(proc);
    await rm(stateDir, { recursive: true, force: true });
  };

  return {
    url,
    wsUrl,
    port: actualPort,
    stateDir,
    process: proc,
    cleanup,
    generateLoginURL,
    getAuthToken,
    restart,
  };
}

/**
 * Wait for a condition to be true, with timeout.
 */
export async function waitFor(
  condition: () => Promise<boolean>,
  timeoutMs: number = 5000,
  intervalMs: number = 100,
): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (await condition()) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`Timeout waiting for condition after ${timeoutMs}ms`);
}
