# ADR 0012: Frontend toolchain on Node, pnpm, Prettier and ESLint

Date: 2026-11-06 · Status: accepted

## Context

The Svelte web UI was built, checked and tested with Deno: `deno task` ran
Vite, svelte-check and Playwright through `npm:` specifiers, and `deno fmt` and
`deno lint` checked style. Nothing that ships depends on it, since the Go binary
embeds the built `dist`.

- **Deno's future is uncertain.** Cloudflare acquired Deno Land in October 2026,
  and the runtime is reported to get only fixes for about a year while the team
  moves to Workers.
- **Deno ran our tools through a Node-compatibility layer, and that layer kept
  breaking.** Deno 2.6 broke Playwright's transform layer, and Deno 2.9 hung
  its browser install. Each time CI pinned Deno, in the workflows and later in
  `.dvmrc`, until a fix landed. Vite, svelte-check and Playwright are all
  built for Node.
- **`deno fmt` and `deno lint` excluded `web/src/`,** so the Svelte components
  were never formatted or linted. Only svelte-check looked at them.

## Decision

- **Node runs the tools, and pnpm manages dependencies.** `web/package.json`
  pins exact versions, `pnpm-lock.yaml` locks the tree, `packageManager` pins
  pnpm, and `.nvmrc` pins Node for CI and releases. Build scripts are denied by
  default (`allowBuilds` in `pnpm-workspace.yaml`). esbuild's script is denied
  too, since it only verifies the binary that its platform package already
  installed.
- **`web/tsconfig.json` holds the TypeScript settings** that Deno used to
  supply itself: `strict`, bundler module resolution, and the `node`, `svelte`
  and `vite/client` types. `@types/node` supplies only the `node` types. The
  other two ship with the `svelte` and `vite` packages. The editor extension
  and svelte-check both read it, so they agree. It covers the e2e tests too, which
  were never type-checked before. `target` is ES2020 because esbuild reads it
  when compiling TypeScript, and ES2020 keeps the bundle identical.
- **Prettier with `prettier-plugin-svelte` formats all of `web/`,** including
  `.svelte` templates. It is the only mature formatter for the whole Svelte file.
- **ESLint, with `typescript-eslint` and `eslint-plugin-svelte`, lints all of
  `web/`.** It adds Svelte rules (runes, reactivity, `{@html}`) that
  svelte-check does not have. svelte-check stays as the type checker.
- **Rejected:** Bun, which has the same single-vendor risk. Plain npm, because
  pnpm's strict `node_modules` and default-deny build scripts suit a
  security tool. Biome, because it cannot yet format Svelte templates without an
  experimental flag, which would leave `src/` unformatted.

## Consequences

- Contributors need Node and pnpm instead of Deno. CI uses `pnpm/action-setup`
  and `actions/setup-node`, and the AUR package's `makedepends` lists `nodejs`
  and `pnpm`.
- The same versions resolve as under Deno, so the JS and CSS assets in
  `internal/api/web/dist` rebuilt byte-identical. Only `dist/index.html`
  changed, because Prettier reformatted `web/index.html`.
- Turning Prettier on for `src/` and `tests/` caused a one-time reformat,
  listed in `.git-blame-ignore-revs`.
- So that the switch changes no runtime behavior, `web/eslint.config.js` turns
  off one `eslint-plugin-svelte` recommended rule, `svelte/require-each-key`,
  with the reason next to it. `web/src/App.svelte` disables
  `svelte/prefer-svelte-reactivity` on three lines, each with its own reason.
