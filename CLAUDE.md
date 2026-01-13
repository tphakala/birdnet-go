# BirdNET-Go Development Guidelines

## Project Overview

BirdNET-Go: Go implementation of BirdNET for real-time bird sound identification aimed for non serious birders and home users. Open source project for fun.

## Quick Navigation

- **Frontend**: See `frontend/CLAUDE.md` for Svelte 5, TypeScript, UI
- **Backend**: See `internal/CLAUDE.md` for Go standards, testing
- **API v2**: See `internal/api/v2/CLAUDE.md` for endpoints
- **Testing**: See `TESTING.md` for test patterns, testify usage, shared helpers

**IMPORTANT**: Always read the relevant CLAUDE.md file before working on code:

- Working on Go code? Read `internal/CLAUDE.md` first
- Working on frontend? Read `frontend/CLAUDE.md` first
- Working on API v2? Read `internal/api/v2/CLAUDE.md` first
- Writing tests? Read `TESTING.md` first - all tests MUST use testify

## Universal Rules

### Critical Constraints

- **NEVER expand API v1** - All new endpoints in `internal/api/v2/`
- **Always lint before commit**: `golangci-lint run -v` (Go), `npm run check:all` (Frontend)
- **Branch from updated main**: `git pull origin main && git checkout -b feature-name`
- **No magic numbers/strings** - Use named constants with descriptive names

### Project Structure

| Path         | Purpose            |
| ------------ | ------------------ |
| `/cmd/`      | Viper CLI commands |
| `/internal/` | Private packages   |
| `/pkg/`      | Public packages    |
| `/frontend/` | Svelte 5 UI        |

## Code Search & Refactoring

**Use ast-grep instead of grep/sed for code operations** - it's more reliable and syntax-aware.

### Search Operations

```bash
# ❌ Avoid grep for code patterns
grep -r "function.*async" src/

# ✅ Use ast-grep - understands syntax
ast-grep --pattern "async function $NAME($$$) { $$$ }" src/

# ❌ Complex grep with regex
grep -r "console\.\(log\|warn\|error\)" src/

# ✅ Structural pattern matching
ast-grep --pattern "console.$METHOD($$$)" src/
```

### Refactoring Operations

```bash
# ❌ Avoid sed for code changes
sed 's/let \([a-zA-Z]*\) =/const \1 =/g' file.js

# ✅ Use ast-grep rewrite - syntax-safe
ast-grep --pattern "let $VAR = $VALUE" --rewrite "const $VAR = $VALUE" src/

# ✅ Complex refactoring example
ast-grep --pattern "export let $PROP" --rewrite "let { $PROP } = \$props()" --lang svelte src/
```

### Why ast-grep?

- **Syntax-aware**: Won't break code structure
- **Language-specific**: Supports TypeScript, Svelte, Go, etc.
- **Reliable**: Matches semantic patterns, not just text
- **Safe refactoring**: Preserves code meaning

**Frontend**: See `frontend/doc/AST-GREP-SETUP.md` for complete ast-grep integration guide.

## Build Commands

| Command               | Purpose                             |
| --------------------- | ----------------------------------- |
| `task`                | Default build (auto-detects target) |
| `task dev_server`     | Development with hot reload         |
| `task frontend-build` | Frontend only                       |
| `task clean`          | Clean artifacts                     |
| `task linux_amd64`    | Cross-platform builds               |

## Pre-Commit Checklist

1. Run linters: `golangci-lint run -v` / `npm run check:all`
2. Run tests: `go test -race -v` / `npm test`
3. Check open PRs to avoid conflicts
4. Format markdown with prettier
5. Document all exports

## PR Review Workflow

After pushing updates to a PR, request automated reviews:

```bash
# Request Gemini review
gh pr comment <PR_NUMBER> --body "/gemini review"

# Or from current branch
gh pr comment $(gh pr view --json number -q .number) --body "/gemini review"
```

This triggers automated code review that checks for bugs, security issues, and best practices.

### Handling PR Review Comments

When fetching and addressing code review comments from a PR, use the receiving-code-review skill:

```text
/superpowers:receiving-code-review
```

This skill ensures:

- Technical verification before implementing suggestions
- Appropriate pushback on incorrect feedback
- No performative agreement - just fix and move on
- Clarification of unclear items before partial implementation
