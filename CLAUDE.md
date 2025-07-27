# BirdNET-Go Development Guidelines

## Project Overview
BirdNET-Go: Go implementation of BirdNET for real-time bird sound identification with telemetry/observability.

## Critical Rules

### Git Workflow
- **Always start from an updated base branch**:
  ```bash
  git checkout <base-branch>  # main or feature branch
  git pull origin <base-branch>
  git checkout -b <new-branch-name>
  ```
- Check open/merged PRs before starting work to avoid conflicts
- **Run `golangci-lint run -v` before EVERY commit**
- Rebase regularly against your base branch

### API Development
- **NEVER expand API v1** - All new endpoints go in `internal/api/v2/`
- API v1 is deprecated - no new functionality

### Project Structure
- `/cmd/` - Viper managed CLI commands
- `/internal/` - Private packages (not importable externally)
- `/pkg/` - Public reusable packages
- `/internal/api/v2/` - New API endpoints
- Follow Go's internal package conventions

### Build & Review Process
1. Run `golangci-lint run -v` for entire module
2. Run `go test -race -v`
3. Format markdown with prettier
4. Address ALL review comments systematically

## Quick Reference
- New APIs in v2 only
- Branch from latest main
- Run linter before commit
- Test with race detection
- Document all exports

## Development Workflow
- When running linter, run for full project: `golangci-lint run -v`

## Go-Specific Guidelines
See `internal/CLAUDE.md` for detailed Go coding standards and patterns.