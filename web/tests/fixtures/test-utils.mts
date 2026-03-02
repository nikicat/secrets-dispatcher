import { Buffer } from "node:buffer";
import { type ChildProcess, spawn } from "node:child_process";
import process from "node:process";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createReadStream, existsSync } from "node:fs";
import {
  createServer,
  type IncomingMessage,
  request as httpRequest,
  type Server,
  type ServerResponse,
} from "node:http";
import { dirname, extname, join } from "node:path";
import { tmpdir } from "node:os";
import { createHmac, randomBytes } from "node:crypto";
import { fileURLToPath } from "node:url";
import type { Socket } from "node:net";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = join(__dirname, "..", "..", "..");
const BINARY_PATH = join(PROJECT_ROOT, "secrets-dispatcher");
const FRONTEND_DIR = join(PROJECT_ROOT, "web", "dist");

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

// MIME types for static file serving
const MIME_TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "application/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
};

/**
 * Create a reverse proxy that serves the frontend from web/dist/ and proxies
 * /api/ requests (including WebSocket) to the Go backend.
 */
function createTestProxy(
  backendPort: number,
): Promise<{ server: Server; port: number }> {
  return new Promise((resolve, reject) => {
    const server = createServer((req, res) => {
      if (req.url?.startsWith("/api/")) {
        proxyRequest(req, res, backendPort);
      } else {
        serveStatic(req, res);
      }
    });

    server.on("upgrade", (req, socket, head) => {
      proxyWebSocket(req, socket as Socket, head, backendPort);
    });

    server.listen(0, "127.0.0.1", () => {
      const addr = server.address() as { port: number };
      resolve({ server, port: addr.port });
    });

    server.on("error", reject);
  });
}

function proxyRequest(
  req: IncomingMessage,
  res: ServerResponse,
  backendPort: number,
): void {
  const proxyReq = httpRequest(
    {
      hostname: "127.0.0.1",
      port: backendPort,
      path: req.url,
      method: req.method,
      headers: req.headers,
    },
    (proxyRes) => {
      res.writeHead(proxyRes.statusCode!, proxyRes.headers);
      proxyRes.pipe(res);
    },
  );
  proxyReq.on("error", () => {
    res.writeHead(502);
    res.end("Backend unavailable");
  });
  req.pipe(proxyReq);
}

function proxyWebSocket(
  req: IncomingMessage,
  socket: Socket,
  head: Buffer,
  backendPort: number,
): void {
  const proxyReq = httpRequest({
    hostname: "127.0.0.1",
    port: backendPort,
    path: req.url,
    method: "GET",
    headers: req.headers,
  });

  proxyReq.on("upgrade", (_proxyRes, proxySocket, proxyHead) => {
    // Forward the raw HTTP upgrade response from the backend to the client.
    // We need to reconstruct the response line + headers from what the backend sent.
    // The simplest way: just pipe everything. The backend already sent the 101 response
    // on proxySocket's internal buffer before the 'upgrade' event fires.
    // Actually, Node.js strips the response and gives us the socket post-handshake.
    // We need to write the 101 ourselves based on _proxyRes.

    let response = `HTTP/1.1 101 Switching Protocols\r\n`;
    for (let i = 0; i < _proxyRes.rawHeaders.length; i += 2) {
      response += `${_proxyRes.rawHeaders[i]}: ${
        _proxyRes.rawHeaders[i + 1]
      }\r\n`;
    }
    response += "\r\n";
    socket.write(response);
    if (proxyHead.length) socket.write(proxyHead);
    if (head.length) proxySocket.write(head);

    proxySocket.pipe(socket);
    socket.pipe(proxySocket);

    proxySocket.on("error", () => socket.destroy());
    socket.on("error", () => proxySocket.destroy());
  });

  proxyReq.on("error", () => socket.destroy());
  proxyReq.end();
}

function serveStatic(req: IncomingMessage, res: ServerResponse): void {
  let urlPath = (req.url?.split("?")[0]) || "/";
  if (urlPath === "/") urlPath = "/index.html";

  const filePath = join(FRONTEND_DIR, urlPath);

  // Prevent directory traversal
  if (!filePath.startsWith(FRONTEND_DIR)) {
    res.writeHead(403);
    res.end();
    return;
  }

  if (existsSync(filePath)) {
    const ext = extname(filePath);
    const contentType = MIME_TYPES[ext] || "application/octet-stream";
    res.writeHead(200, { "Content-Type": contentType });
    createReadStream(filePath).pipe(res);
  } else {
    // SPA fallback — serve index.html for client-side routes
    const indexPath = join(FRONTEND_DIR, "index.html");
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    createReadStream(indexPath).pipe(res);
  }
}

/**
 * Start a test instance of the secrets-dispatcher backend.
 * Creates an isolated config directory and starts the server in API-only mode.
 * A lightweight reverse proxy serves the frontend from web/dist/ and proxies
 * API/WebSocket traffic to the Go backend (no embed or copy needed).
 */
export async function startTestBackend(
  options?: { version?: string; extraArgs?: string[] },
): Promise<TestBackend> {
  // Create temp directory for test
  const testId = randomBytes(8).toString("hex");
  const stateDir = join(tmpdir(), `secrets-dispatcher-test-${testId}`);
  await mkdir(stateDir, { recursive: true });

  // Write minimal config to isolate tests from user's real config.
  // The session_bus downstream creates a staticProvider so the WS snapshot
  // includes a "client connected" entry that many tests expect.
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

  // Build environment with optional version override
  const env: Record<string, string> = { ...process.env };
  if (options?.version) {
    env.TEST_BUILD_VERSION = options.version;
  }

  const extraArgs = options?.extraArgs ?? [];

  const spawnBackend = (
    listenAddr: string,
    spawnEnv: Record<string, string>,
  ): ChildProcess => {
    return spawn(
      BINARY_PATH,
      [
        "serve",
        "--api-only",
        "--notifications=false",
        "--config",
        configPath,
        "--state-dir",
        stateDir,
        "--listen",
        listenAddr,
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
  const waitForServer = (p: ChildProcess): Promise<number> => {
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

  const stopProcess = (p: ChildProcess): Promise<void> => {
    if (!p.pid || p.exitCode !== null) return Promise.resolve();

    return new Promise<void>((resolve) => {
      let killTimer: ReturnType<typeof setTimeout> | null = null;

      p.on("exit", () => {
        if (killTimer) clearTimeout(killTimer);
        resolve();
      });

      p.kill("SIGTERM");

      killTimer = setTimeout(() => {
        try {
          p.kill("SIGKILL");
        } catch { /* already dead */ }
      }, 2000);
    });
  };

  // Start with port 0 — let the OS assign an available port
  let proc = spawnBackend("127.0.0.1:0", env);
  const backendPort = await waitForServer(proc);

  // Start reverse proxy (serves frontend from disk, proxies API to backend)
  const proxy = await createTestProxy(backendPort);
  const proxyPort = proxy.port;

  const url = `http://localhost:${proxyPort}`;
  const wsUrl = `ws://localhost:${proxyPort}/api/v1/ws`;

  const getAuthToken = async (): Promise<string> => {
    const cookiePath = join(stateDir, ".cookie");
    return (await readFile(cookiePath, "utf-8")).trim();
  };

  const generateLoginURL = async (): Promise<string> => {
    const token = await getAuthToken();
    const jwt = generateJWT(token);
    return `${url}/?token=${jwt}`;
  };

  const restart = async (
    restartOptions?: { version?: string },
  ): Promise<void> => {
    await stopProcess(proc);

    const restartEnv: Record<string, string> = { ...process.env };
    if (restartOptions?.version) {
      restartEnv.TEST_BUILD_VERSION = restartOptions.version;
    }

    // Reuse the same backend port so the proxy can reconnect
    proc = spawnBackend(`127.0.0.1:${backendPort}`, restartEnv);
    await waitForServer(proc);
  };

  const cleanup = async (): Promise<void> => {
    proxy.server.close();
    await stopProcess(proc);
    await rm(stateDir, { recursive: true, force: true });
  };

  return {
    url,
    wsUrl,
    port: proxyPort,
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
