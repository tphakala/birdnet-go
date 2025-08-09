# BirdNET-Go Development Guidelines

## Project Overview

BirdNET-Go: Go implementation of BirdNET for real-time bird sound identification aimed for non serious birders and home users. Open source project for fun.

## Quick Navigation

- **Frontend**: See `frontend/CLAUDE.md` for Svelte 5, TypeScript, UI
- **Backend**: See `internal/CLAUDE.md` for Go standards, testing
- **API v2**: See `internal/api/v2/CLAUDE.md` for endpoints

## Universal Rules

### Critical Constraints

- **NEVER expand API v1** - All new endpoints in `internal/api/v2/`
- **Always lint before commit**: `golangci-lint run -v` (Go), `npm run check:all` (Frontend)
- **Branch from updated main**: `git pull origin main && git checkout -b feature-name`

### Project Structure

| Path         | Purpose            |
| ------------ | ------------------ |
| `/cmd/`      | Viper CLI commands |
| `/internal/` | Private packages   |
| `/pkg/`      | Public packages    |
| `/frontend/` | Svelte 5 UI        |

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
