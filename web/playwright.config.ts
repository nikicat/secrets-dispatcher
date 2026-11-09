import { defineConfig } from "@playwright/test";
import process from "node:process";

const allBrowsers = process.env.ALL_BROWSERS === "1";
// screenshots.spec.ts rewrites the committed docs/screenshots/, so only `make screenshots` runs it
const screenshots = process.env.SCREENSHOTS === "1";

export default defineConfig({
  testDir: "./tests",
  testIgnore: screenshots ? [] : ["**/screenshots.spec.ts"],
  projects: [
    { name: "chromium", use: { browserName: "chromium" } },
    ...(allBrowsers
      ? [{ name: "firefox", use: { browserName: "firefox" as const } }]
      : []),
  ],
  fullyParallel: true,
  use: {},
  // Don't start web server - tests start the Go binary directly
});
