# ADR 0013: Web UI targets ES2025 on Firefox ESR 140 and Chrome 136

Date: 2026-11-07 · Status: accepted

## Context

ADR 0012 set the TypeScript `target` to ES2020, only to keep the bundle
identical to the Deno build. Vite's default target then lowered the output for
browsers from 2020, so the bundle emulated private class fields with `WeakMap`s.
No browser floor for the web UI was written down anywhere.

The UI is served on `127.0.0.1` to the user's own Linux desktop browser, and the
E2E suite covers Chromium and Firefox. Debian stable ships `firefox-esr`, which
makes Firefox ESR the oldest browser a user is likely to run.

## Decision

- **The floor is Firefox ESR 140 and Chrome 136.** `build.target` in
  `web/vite.config.ts` names both, so Vite lowers the output for these browsers
  and no further. Chrome 136 is the first Chrome with every ES2025 API. Firefox
  140 already has all of them.
- **The source may use all of ES2025.** `web/tsconfig.json` sets `target` and
  `lib` to ES2025, which TypeScript 6 supports. `lib` only adds types, so it
  must not go past what the floor runs.
- **The toolchain is Vite 8 and TypeScript 6.** Vite 8 builds with Rolldown and
  Oxc instead of Rollup and esbuild. It no longer installs esbuild, so
  `pnpm-workspace.yaml`, which only denied esbuild's build script, is gone. pnpm
  still denies build scripts by default. TypeScript 7 waits, because
  svelte-check and typescript-eslint support only TypeScript 5 and 6.

## Consequences

- The bundle shrank from 83.5 KB to 78.0 KB, and private fields now ship
  natively.
- Raising the floor means changing `build.target` and, if a newer edition is
  wanted, `target` and `lib` together. Lowering it means checking that every
  API the source uses exists in the new floor.
