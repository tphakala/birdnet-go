# AI Agent Instructions for BirdNET-Go

These instructions apply to all AI coding agents working on this repository,
regardless of tool (Claude Code, Codex, Cursor, Gemini, Windsurf, Copilot, etc.).

## PR Scope Rule

Each pull request must contain exactly ONE of:
- One feature
- One bug fix
- One refactor

PRs that batch multiple features or multiple fixes WILL NOT be merged.
This is non-negotiable. If your task involves multiple independent changes,
split them into separate branches and separate PRs.

If you are uncertain whether changes constitute one concern or multiple,
ask the user before proceeding.

Why: batched PRs cannot be properly reviewed, cannot be safely reverted
if one change causes a regression, and create merge conflicts for other
contributors.

## Mandatory: Pre-Push Quality Gate

Before pushing code or creating a pull request, you MUST execute the
preflight quality gate defined in `.agents/skills/preflight/SKILL.md`.

Read that file and follow its complete process (all phases).
Do not skip this step. Do not push without running it first.

If your platform supports native skill invocation (e.g., Claude Code's
`/preflight` or Codex's skill system), use that. Otherwise, read the
SKILL.md file directly and execute the review process.

## PR Creation Rules

When creating a pull request, you MUST:

1. Verify this PR addresses exactly ONE feature, fix, or refactor
2. Include a "Preflight Status" section in the PR description showing
   what was found and fixed during preflight
3. Verify all linters pass (`golangci-lint run -v`, `npm run check:all`)
4. Verify all tests pass (`go test -race ./...`, `npm test`)
5. Confirm the diff contains ONLY changes relevant to the stated goal
6. Confirm scope is complete (no TODO/FIXME for core functionality)
7. Confirm no secrets, credentials, or PII in the diff
8. Document any breaking changes to API, config, or behavior

You MUST actually execute verification commands (linters, tests) and
observe passing output before claiming they pass. Do not check boxes
based on assumption or prior knowledge.

PRs missing the preflight certification will require multiple review
rounds. The gate catches the same issues reviewers find; running it
locally saves a day of back-and-forth.

## Interpreting CI Failures

The `golangci-test` workflow runs Go tests through `gotestsum` with one
automatic rerun of any failed test, then publishes a consolidated result in the
`test-report` job. Before assuming a red run means your code is broken:

1. Read the `test-report` job summary. It states one verdict:
   - `REGRESSION` - real failures that persisted after a rerun. Fix these.
   - `PASS (with flakes)` - tests that failed once then passed on rerun. These
     are flaky/infra (a reaped container, a registry blip), NOT a code
     regression. Do not "fix" them; re-run or report instead.
   - `PASS` - all green.
2. For machine-readable detail, download the `ci-failures` artifact:
   - `ci-failures.json` - array of real regressions (`{pkg, test, output}`).
   - `ci-flaky.json` - tests that passed on rerun (informational).
   Prefer reading these small files over scrolling the raw multi-thousand-line
   logs.
3. If a testcontainer job failed, the job summary includes a "Testcontainer
   diagnostics" block (docker state, memory, OOM kills) to distinguish an
   infra flake from a logic bug.

Do not spend time debugging a failure classified as flaky/infra. If a test is
persistently flaky, raise it rather than patching around it.

## Project Context

- Tech stack: Go 1.24+, Svelte 5, TypeScript, Tailwind v4.1
- Build system: Task (taskfile.dev)
- Linting: golangci-lint (Go), npm run check:all (Frontend)
- Testing: go test -race (Go), npm test (Frontend)
- All PRs receive automated CodeRabbit reviews
- API v1 is frozen; all new endpoints go in `internal/api/v2/`

For detailed guidelines, see `CLAUDE.md` and the `CLAUDE.md` files in
subdirectories (`internal/`, `frontend/`, `internal/api/v2/`).
