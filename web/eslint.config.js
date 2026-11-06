import js from "@eslint/js";
import { defineConfig, includeIgnoreFile } from "eslint/config";
import svelte from "eslint-plugin-svelte";
import globals from "globals";
import ts from "typescript-eslint";
import { fileURLToPath } from "node:url";
import svelteConfig from "./svelte.config.js";

export default defineConfig(
  includeIgnoreFile(fileURLToPath(new URL(".gitignore", import.meta.url))),
  js.configs.recommended,
  ts.configs.recommended,
  svelte.configs.recommended,
  {
    files: ["src/**"],
    languageOptions: { globals: globals.browser },
  },
  {
    files: ["*.js", "*.ts", "tests/**"],
    languageOptions: { globals: globals.node },
  },
  {
    files: ["**/*.svelte", "**/*.svelte.ts"],
    languageOptions: {
      parserOptions: {
        parser: ts.parser,
        extraFileExtensions: [".svelte"],
        svelteConfig,
      },
    },
  },
  {
    rules: {
      // A `_` prefix marks a parameter that is deliberately unused.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_" },
      ],
      // Our {#each} lists are display-only and rebuilt wholesale; a key adds
      // nothing, and Svelte throws on a duplicate one.
      "svelte/require-each-key": "off",
    },
  },
);
