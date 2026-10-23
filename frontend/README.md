# BirdNET-Go Frontend

The web UI of BirdNET-Go: a Svelte 5 single page application (runes, TypeScript, Tailwind CSS v4) built with Vite. It is a plain Svelte SPA, not SvelteKit: routing is done client-side in `src/App.svelte` and the navigation store (see [ROUTES.md](ROUTES.md)). The production build in `frontend/dist` is embedded into the Go binary (`embed.go`) and served by the Go server under `/ui/`, with assets under `/ui/assets/`. The UI talks to the backend through the `/api/v2/` endpoints; startup configuration such as the CSRF token and the security state comes from `GET /api/v2/app/config`.

The UI targets desktop and tablet only. Phone layouts are out of scope and are not tested or fixed.

## Development

Run the commands from the repository root with [Task](https://taskfile.dev). Task installs the npm dependencies on demand (`task frontend-install`).

| Command               | Purpose                                                                                |
| --------------------- | -------------------------------------------------------------------------------------- |
| `task frontend-watch` | Rebuild `frontend/dist` on every change (`vite build --watch`)                         |
| `task dev_server`     | Run the Go backend with hot reload through `air` (see `.air.toml`), with Go files only |
| `task frontend-dev`   | Start the Vite dev server with HMR on port 5173                                        |
| `task frontend-build` | Type-check and build the production bundle                                             |
| `task dev`            | Print the development workflow options                                                 |

There are two ways to work on the UI:

1. **Watch mode, served by Go.** Run `task frontend-watch` in one terminal and `task dev_server` (or `./bin/birdnet-go realtime`) in another, then open `http://localhost:8080`. When `frontend/dist` exists on disk next to the working directory, the Go server serves the UI from disk instead of the embedded copy, so a rebuild is picked up without recompiling Go. `air` ignores the `frontend` directory.
2. **Vite dev server.** Run the backend as above, then `task frontend-dev` and open `http://localhost:5173`. Vite proxies `/api` to `http://localhost:8080`.

Without Task, the same scripts run from `frontend/`: `npm run dev`, `npm run build`, `npm run build -- --watch`.

## Tests and checks

Run these from `frontend/` (the Task names are in brackets).

| Command                                         | Purpose                                                        |
| ----------------------------------------------- | -------------------------------------------------------------- |
| `npm test` [`task frontend-test`]               | Vitest unit and component tests                                |
| `npm run check:all` [`task frontend-lint`]      | Prettier, ESLint, Stylelint, `svelte-check` and ast-grep rules |
| `npm run typecheck` [`task frontend-typecheck`] | TypeScript and Svelte checks only                              |
| `npm run test:a11y`                             | Accessibility tests                                            |
| `npm run test:e2e`                              | Playwright end-to-end tests (see `tests/README.md`)            |
| `task frontend-quality`                         | Auto-fix, then checks, tests and build                         |

Translation checks are `npm run i18n:validate` and `npm run generate:i18n-types:check`.

## Where things live

| Path                                                                 | Contents                                                                                                                                                                                  |
| -------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `src/App.svelte`, `src/main.js`                                      | Entry points, client-side route table and page loading                                                                                                                                    |
| `src/lib/desktop/`                                                   | The tablet and desktop UI: `layouts/`, `views/` (top level pages), `features/` (analytics, dashboard, detections, import-export, live-stream, settings, system, wizard) and `components/` |
| `src/lib/components/`                                                | Shared primitives                                                                                                                                                                         |
| `src/lib/api/`, `src/lib/auth/`, `src/lib/stores/`, `src/lib/utils/` | API helpers, auth, Svelte stores and utilities                                                                                                                                            |
| `src/lib/i18n/`                                                      | Translation runtime and generated key types                                                                                                                                               |
| `src/lib/telemetry/`                                                 | Frontend error reporting                                                                                                                                                                  |
| `static/messages/`                                                   | Translation files, one JSON file per locale                                                                                                                                               |
| `tests/`                                                             | Playwright end-to-end tests                                                                                                                                                               |
| `tools/`                                                             | Screenshot tooling                                                                                                                                                                        |
| `rules/`                                                             | ast-grep rule sets used by `npm run ast:all`                                                                                                                                              |

## Guidelines

The rules for working in this directory are in [AGENTS.md](AGENTS.md). Nested guides cover the areas that have their own conventions:

- [src/lib/desktop/AGENTS.md](src/lib/desktop/AGENTS.md): the desktop and tablet UI
- [src/lib/desktop/components/AGENTS.md](src/lib/desktop/components/AGENTS.md): UI components
- [src/lib/desktop/components/ui/AGENTS.md](src/lib/desktop/components/ui/AGENTS.md): generic UI primitives
- [src/lib/desktop/features/settings/AGENTS.md](src/lib/desktop/features/settings/AGENTS.md): settings pages
- [static/messages/AGENTS.md](static/messages/AGENTS.md): translations
- [tools/AGENTS.md](tools/AGENTS.md): screenshot tooling
