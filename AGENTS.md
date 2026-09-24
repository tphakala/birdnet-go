# AI Agent Instructions for BirdNET-Go

These instructions apply to every AI coding agent working on this repository
(Claude Code, Codex, Cursor, Antigravity, Gemini CLI, Windsurf, Copilot, and others)
and to human contributors. They are the single source of project guidance.

BirdNET-Go is a Go implementation of BirdNET for real-time bird sound
identification, aimed at hobby birders and home users. It is an open source
project run for fun by volunteers, so avoidable rework and support load matter.

## Module Guides

Guidance is split by area. Tools that support nested `AGENTS.md` files load the
matching file when you work in that directory; if yours does not, read it
yourself before touching code in that area.

| Working on                                  | Read first                                                                                   |
| ------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Any Go code, including `cmd/` and `main.go` | `internal/AGENTS.md` (nested loading does not reach it from `cmd/` or the root)              |
| API v2 endpoints (`internal/api/v2/`)       | `internal/api/v2/AGENTS.md`, which sends you to its `README.md` endpoint catalog             |
| Error handling (`internal/errors/`)         | `internal/errors/AGENTS.md`                                                                  |
| Frontend (Svelte 5, TypeScript)             | `frontend/AGENTS.md`                                                                         |
| Desktop UI (tablet and desktop only)        | `frontend/src/lib/desktop/AGENTS.md`                                                         |
| Desktop UI components                       | `frontend/src/lib/desktop/components/AGENTS.md`                                              |
| Generic UI primitives                       | `frontend/src/lib/desktop/components/ui/AGENTS.md`                                           |
| Settings pages                              | `frontend/src/lib/desktop/features/settings/AGENTS.md`                                       |
| Translations (`frontend/static/messages/`)  | `frontend/static/messages/AGENTS.md`                                                         |
| Screenshot tooling                          | `frontend/tools/AGENTS.md`                                                                   |
| Writing or changing Go tests                | `TESTING.md` and the Testing section of `internal/AGENTS.md` (all Go tests MUST use testify) |
| Writing or changing frontend tests          | the Testing section of `frontend/AGENTS.md`                                                  |

## Project Context

- **Backend**: Go 1.27 (see `go.mod`), Echo HTTP server, GORM datastore
- **Frontend**: Svelte 5 (runes), TypeScript, Tailwind CSS v4, Vite, Vitest
- **Build system**: [Task](https://taskfile.dev) (`Taskfile.yml`)
- **Linting**: `golangci-lint` (Go), `npm run check:all` (frontend)
- **Review**: every PR receives an automated CodeRabbit review

| Path         | Purpose                        |
| ------------ | ------------------------------ |
| `/cmd/`      | CLI commands (Cobra/Viper)     |
| `/internal/` | Private Go packages            |
| `/rules/`    | Custom go-ruleguard lint rules |
| `/frontend/` | Svelte 5 web UI                |

## Critical Constraints

- **API v1 is frozen.** Never add or extend v1 endpoints; all new endpoints go
  in `internal/api/v2/`.
- **Settings must hot-reload.** Every setting changed through the UI must take
  effect immediately, without a server restart. Read settings per request or
  per operation (for example dynamic middleware or an atomic settings snapshot);
  never branch once on a value captured at startup.
- **No magic numbers or strings.** Use named constants with descriptive names.
- **Use `internal/errors`, not the standard `errors` package**, in Go code (see
  `internal/errors/AGENTS.md` for the exceptions).
- **Never log secrets or PII.** Use the typed sensitive-field helpers in
  `internal/logger` (a `forbidigo` lint rule enforces this for credential and
  token field names).
- **Document all exported symbols.**
- **Branch from an up-to-date `main`**:
  `git switch main && git pull origin main && git switch -c <branch>`, and
  check open PRs first so you do not duplicate or conflict with work in flight.
- **Format the Markdown you change with Prettier**: from `frontend/`, run
  `npx prettier --write ../<path to each changed .md file>`. Skip files under
  `.agents/skills/`, which are not Prettier-formatted. Do not run
  `task format-md` in a feature PR: it reformats every Markdown file in the
  repository.

## PR Scope Rule

Each pull request must contain exactly ONE of:

- One feature
- One bug fix
- One refactor

PRs that batch multiple features or multiple fixes WILL NOT be merged. If your
task involves multiple independent changes, split them into separate branches
and separate PRs. If you are unsure whether changes are one concern or several,
ask before proceeding.

Why: batched PRs cannot be properly reviewed, cannot be safely reverted if one
change causes a regression, and create merge conflicts for other contributors.

## Mandatory: Pre-Push Quality Gate

Before pushing code or creating a pull request, you MUST run the preflight
quality gate defined in `.agents/skills/preflight/SKILL.md`. Read that file and
follow its complete process (all phases). Do not push without running it.

If your tool supports native skill invocation, use that: in Claude Code, run
`/preflight` (`.claude/skills/preflight` is a symlink to
`.agents/skills/preflight`).
Otherwise read the SKILL.md file and execute the process manually.

The minimum verification, always run and observed before claiming success:

```bash
task lint                     # Go: golangci-lint over the whole module, zero issues
task test                     # Go tests with -race
cd frontend && npm run check:all && npm test   # frontend
```

Run `task setup-dev` once first: it installs the TensorFlow Lite headers and
C library that every Go build, lint and test needs (the models are committed).
`task lint` and `task test` add the build tags and CGO flags for you. To run a
step by hand instead, print what Task would run with `task --dry lint` or
`task --dry test` instead of copying a command from a document. Task prints
the commands on stderr, each prefixed with `task: [lint]` or `task: [test]`;
copy them without that prefix and run them in `sh` or `bash`.

These checks cover only the default build tags and your own OS:

- For files behind other build tags, add them with
  `task lint BASE_BUILD_TAGS=<tags>` (CI's lint tag set is in
  `.github/workflows/golangci-lint.yml`; for `openvino`, run
  `task check-openvino OPENVINO=true` first, since without `OPENVINO=true`
  the task reports itself up to date and installs nothing).
- Other operating systems cannot be checked locally without a cross toolchain.
  On a pull request, the `cross-platform-build` workflow compiles and vets
  windows/amd64 and linux/arm64, and builds and vets the `openvino` tag on
  linux/amd64 and linux/arm64, whenever Go files change; it is not advisory,
  so treat a failure there as a real break. The native Windows and
  macOS test jobs run only when a maintainer adds the `full-ci` label, and they
  are advisory with known failures, so a green check there proves nothing
  unless you read the job output. When you change OS-specific files
  (`_windows.go`, `_darwin.go`), say so in the PR and ask for `full-ci`.

## PR Creation Rules

When creating a pull request, you MUST:

1. Verify the PR addresses exactly ONE feature, fix, or refactor
2. Include a "Preflight Status" section in the description showing what the
   gate found and fixed
3. Verify all linters pass (`task lint`, `npm run check:all`)
4. Verify all tests pass (`task test`, `npm test`)
5. Confirm the diff contains ONLY changes relevant to the stated goal
6. Confirm scope is complete (no TODO/FIXME left for core functionality)
7. Confirm no secrets, credentials, or PII are in the diff
8. Document any breaking changes to API, config, or behavior

Actually execute the verification commands and observe passing output before
claiming they pass. Do not tick boxes from assumption or prior knowledge. PRs
without the preflight certification take multiple review rounds; the gate
catches the same issues reviewers find.

## Interpreting CI Failures

The `golangci-test` workflow runs Go tests through `gotestsum` with one
automatic rerun of any failed test, then publishes a consolidated result in the
`test-report` job. Before assuming a red run means your code is broken:

1. Read the `test-report` job summary. It states one verdict:
   - `REGRESSION`: real failures that persisted after a rerun. Fix these.
   - `PASS (with flakes)`: tests that failed once then passed on rerun. These
     are flaky/infra (a reaped container, a registry blip), NOT a code
     regression. Do not "fix" them; re-run or report instead.
   - `PASS`: all green.
2. For machine-readable detail, download the `ci-failures` artifact:
   - `ci-failures.json`: array of real regressions (`{pkg, test, output}`).
   - `ci-flaky.json`: tests that passed on rerun (informational).

   Prefer these small files over scrolling multi-thousand-line raw logs.

3. If a testcontainer job failed, the job summary includes a "Testcontainer
   diagnostics" block (docker state, memory, OOM kills) to tell an infra flake
   from a logic bug.

Do not spend time debugging a failure classified as flaky/infra. If a test is
persistently flaky, raise it rather than patching around it.

## Build Commands

| Command                   | Purpose                                                                    |
| ------------------------- | -------------------------------------------------------------------------- |
| `task`                    | Default build (auto-detects the target)                                    |
| `task dev_server`         | Backend development server with reload                                     |
| `task frontend-build`     | Frontend production build                                                  |
| `task test` / `task lint` | Go tests / Go lint                                                         |
| `task frontend-quality`   | Frontend auto-fix (format, lint, ast-grep), then checks, tests, and build  |
| `task clean`              | Remove build artifacts                                                     |
| `task linux_amd64`        | Cross-platform build (also `linux_arm64`, `windows_amd64`, `darwin_arm64`) |

Run `task --list` for everything else (setup, integration tests, native library downloads).

## Code Search and Refactoring

Prefer syntax-aware tools over plain text search for code operations. Use
`ast-grep` (`sg`) for structural searches and rewrites instead of `grep`/`sed`
regexes, which break on formatting and match inside strings and comments:

```bash
# Structural search
ast-grep --pattern 'console.$METHOD($$$)' frontend/src/

# Syntax-safe rewrite: prints the diff only; add --update-all (-U) to apply it
ast-grep --pattern 'let $VAR = $VALUE' --rewrite 'const $VAR = $VALUE' frontend/src/
```

If your tool offers language-server navigation (find references, go to
definition), use it for "who calls this" questions before changing a signature.
See `frontend/doc/AST-GREP-SETUP.md` for the frontend ast-grep rules.

## Handling Review Feedback

Automated review (CodeRabbit plus the repository's configured checks) runs on
every PR. When addressing review comments:

- Verify each suggestion technically before implementing it; reviewers,
  human or bot, can be wrong.
- Push back with a reason when a suggestion is incorrect or out of scope.
- Ask for clarification before partially implementing an unclear item.
- After pushing fixes, a fresh CodeRabbit pass can be requested with a PR
  comment: `@coderabbitai review`.

## About These Instruction Files

- Keep guidance in `AGENTS.md` files: this root file for cross-cutting rules and
  one per module for area-specific rules. Keep each file focused and current;
  delete rules that no longer match the code instead of letting them rot.
- Do NOT add `CLAUDE.md`, `CLAUDE.local.md`, or `GEMINI.md` files. Claude Code
  (2.1.277 and later) reads `AGENTS.md` in a project that has no `CLAUDE.md`
  (the "Project instructions" setting in `/config` controls this). Tested with
  Claude Code 2.1.281: a root `CLAUDE.md` or `CLAUDE.local.md` turns that off
  for the root and the nested `AGENTS.md` files alike.
- Tool caveats: Claude Code's native `AGENTS.md` support is not yet available on
  Bedrock, Vertex, or Foundry, and older versions ignore it. Gemini CLI reads
  `GEMINI.md` by default until its context file setting points at `AGENTS.md`.
  With those tools, read this file and the matching module guide yourself.
- For personal, machine-local instructions with Claude Code, use a gitignored
  file under `.claude/rules/` named `*.local.md`.
