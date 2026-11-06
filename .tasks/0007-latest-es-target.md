---
status: todo
tags: [web, build]
---
# Build the web UI for the latest ECMAScript

## Goal

Write and ship the web UI as modern JavaScript: ES2025 syntax and APIs in the
source, and output that browsers run without down-levelling. Pick the browsers
we support on purpose, instead of inheriting Vite's default.

## State (2026-11-06)

After the Deno → pnpm switch (ADR 0012):

- TypeScript 5.9.3 supports targets up to ES2024. `web/tsconfig.json` uses
  `target: ES2020`, only because esbuild reads it when compiling TypeScript and
  ES2020 kept the bundle identical to the Deno build.
- Vite 6.4.1 builds with esbuild and Rollup. `build.target` isn't set, so Vite
  uses its `modules` default: es2020, chrome87, firefox78, safari14. That's why
  the bundle contains helpers for class fields and private fields.
- No browser floor is documented for the web UI anywhere.

## Decisions to make

- **Browser floor.** The UI is served on `127.0.0.1` to the user's own Linux
  desktop browser, and e2e covers Chromium and Firefox. Proposal: Firefox ESR
  (currently 140) and Chrome/Chromium from the last ~2 years. That is at least
  as new as Vite 8's `baseline-widely-available` default (chrome111, edge111,
  firefox114, safari16.4). Debian stable ships `firefox-esr`, so ESR is the real
  lower bound.
- **`lib` vs floor.** `lib: ES2025` only adds *types*. Some ES2025 APIs reach
  browsers late (Set methods: Firefox 127; Iterator helpers: Firefox 131), so
  allow ES2025 APIs only if every browser on the floor has them. Otherwise
  stop `lib` at the newest edition the floor fully supports.

## Plan

1. **Upgrade the toolchain** in its own PR, after the pnpm switch merges:
   - Vite 6 → 8: Rolldown + Oxc replace Rollup + esbuild. `vite.config.ts`
     is minimal, so expect little to change.
   - `@sveltejs/vite-plugin-svelte` 5 → 7 (needs Vite 8 and Svelte ≥ 5.46.4).
   - Svelte 5.50 → 5.57, svelte-check 4.3 → 4.7.
   - TypeScript 5.9 → 6.0, which adds `target`/`lib` ES2025.
   - **Not TypeScript 7:** svelte-check's peer range is `^5 || ^6`.
   - Check the esbuild `allowBuilds: false` entry in `pnpm-workspace.yaml`.
     Drop it if Vite 8 no longer pulls esbuild in.
2. **Set the target:**
   - `tsconfig.json`: `target: ES2025`, and `lib` per the decision above.
   - `vite.config.ts`: an explicit `build.target` list for the floor (e.g.
     `["firefox140", "chrome120"]`) or `"es2025"`, so the output isn't lowered
     beyond what the floor needs.
   - Check whether Oxc still reads `target` and `useDefineForClassFields` from
     the tsconfig the way esbuild did.
3. **Expect the bundle to change.** Rebuild `internal/api/web/dist` and review
   the diff: the class-field and private-field helpers should go away and the
   bundle should get smaller. Record the size before and after.
4. **Verify:**
   - `make check` and the e2e suite in Chromium and Firefox.
   - The Tier-2 GNOME VM run. The web UI opens from a notification in
     Ubuntu's Firefox snap.
   - One manual load of the UI on the oldest browser on the floor (e.g. a
     Firefox ESR container).
5. **Document it:**
   - A new ADR for the browser floor and target, with the reasons for both.
   - A web UI browsers row in `docs/COMPATIBILITY.md`.
   - Update ADR 0012's note about why `target` is ES2020.

## Ruled out

- **Raising `target` alone, with no toolchain upgrade.** TS 5.9 stops at
  ES2024, and Vite 6 would still lower the output to its old browser defaults.
- **TypeScript 7 (native compiler).** svelte-check doesn't support it yet.
