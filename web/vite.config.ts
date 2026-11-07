import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

export default defineConfig({
  plugins: [svelte()],
  build: {
    // The oldest browsers the UI supports: Firefox ESR 140, and Chrome 136,
    // the first with every ES2025 API that tsconfig's lib allows.
    target: ["firefox140", "chrome136"],
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8484",
        changeOrigin: true,
      },
    },
  },
});
