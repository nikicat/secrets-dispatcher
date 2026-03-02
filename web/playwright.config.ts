import { defineConfig } from "@playwright/test";
import process from "node:process";

const allBrowsers = process.env.ALL_BROWSERS === "1";

export default defineConfig({
  testDir: "./tests",
  projects: [
    { name: "chromium", use: { browserName: "chromium" } },
    ...(allBrowsers
      ? [{ name: "firefox", use: { browserName: "firefox" as const } }]
      : []),
  ],
  use: {},
  // Don't start web server - tests start the Go binary directly
});
