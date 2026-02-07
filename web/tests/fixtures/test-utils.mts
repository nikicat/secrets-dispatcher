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
}

/**
 * Find an available port by letting the OS assign one.
 */
function getRandomPort(): number {
  return 18000 + Math.floor(Math.random() * 1000);
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
export async function startTestBackend(): Promise<TestBackend> {
  // Create temp directory for test
  const testId = randomBytes(8).toString("hex");
  const stateDir = join(tmpdir(), `secrets-dispatcher-test-${testId}`);
  await mkdir(stateDir, { recursive: true });

  const port = getRandomPort();
  const url = `http://localhost:${port}`;
  const wsUrl = `ws://localhost:${port}/api/v1/ws`;

  // Start the backend in API-only mode with isolated state
  const proc = spawn(
    BINARY_PATH,
    [
      "--api-only",
      "--state-dir",
      stateDir,
      "--listen",
      `127.0.0.1:${port}`,
      "--client",
      "test-client",
    ],
    {
      stdio: ["ignore", "pipe", "pipe"],
      detached: false,
    },
  );

  // Wait for the server to start
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      reject(new Error("Timeout waiting for backend to start"));
    }, 5000);

    let output = "";
    proc.stderr?.on("data", (data: Buffer) => {
      output += data.toString();
      if (output.includes("API server started")) {
        clearTimeout(timeout);
        resolve();
      }
    });

    proc.on("error", (err) => {
      clearTimeout(timeout);
      reject(err);
    });

    proc.on("exit", (code) => {
      if (code !== 0 && code !== null) {
        clearTimeout(timeout);
        reject(new Error(`Backend exited with code ${code}: ${output}`));
      }
    });
  });

  const getAuthToken = async (): Promise<string> => {
    const cookiePath = join(stateDir, ".cookie");
    return (await readFile(cookiePath, "utf-8")).trim();
  };

  const generateLoginURL = async (): Promise<string> => {
    const token = await getAuthToken();
    const jwt = generateJWT(token);
    return `${url}/?token=${jwt}`;
  };

  const cleanup = async (): Promise<void> => {
    // Kill the process
    if (proc.pid) {
      proc.kill("SIGTERM");
      // Wait for process to exit
      await new Promise<void>((resolve) => {
        proc.on("exit", () => resolve());
        setTimeout(resolve, 1000); // Force resolve after 1s
      });
    }
    // Clean up temp directory
    await rm(stateDir, { recursive: true, force: true });
  };

  return {
    url,
    wsUrl,
    port,
    stateDir,
    process: proc,
    cleanup,
    generateLoginURL,
    getAuthToken,
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
