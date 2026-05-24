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

## Project Context

- Tech stack: Go 1.24+, Svelte 5, TypeScript, Tailwind v4.1
- Build system: Task (taskfile.dev)
- Linting: golangci-lint (Go), npm run check:all (Frontend)
- Testing: go test -race (Go), npm test (Frontend)
- All PRs receive automated CodeRabbit and Gemini reviews
- API v1 is frozen; all new endpoints go in `internal/api/v2/`

For detailed guidelines, see `CLAUDE.md` and the `CLAUDE.md` files in
subdirectories (`internal/`, `frontend/`, `internal/api/v2/`).
