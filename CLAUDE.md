# BirdNET-Go Development Guidelines

## Project Overview
BirdNET-Go: Go implementation of BirdNET for real-time bird sound identification with telemetry/observability.

## Critical Rules

### API Development
- **NEVER expand API v1** - All new endpoints go in `internal/api/v2/`
- API v1 is deprecated - no new functionality

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
- Do not reuse old merged branches
- Do not open PRs against old already merged branches, always check for this

### Code Quality

#### Imports & Dependencies
- **Only import** `"github.com/tphakala/birdnet-go/internal/errors"` (never standard `"errors"`)
- Always specify `.Component()` and `.Category()` for telemetry
- Register new components in error package's `init()`

#### Error Handling
- **Wrap errors with context**: `fmt.Errorf("operation failed: %w", err)`
- **Log but continue** on individual failures in batch operations
- **Provide detailed context** in error messages
- **Use structured logging** for errors with metadata

#### Security
- Validate all user input (path traversal, command injection, SQL injection)
- Use `net.ParseIP()` and `url.Parse()` - never manual string parsing
- Validate UUIDs properly

#### Testing
- Use `t.Parallel()` only when tests are truly independent
- Avoid `time.Sleep()` - use channels/synchronization instead
- Call `b.ResetTimer()` after benchmark setup
- Use `for b.Loop()` pattern (Go 1.24+) for benchmarks
- **Use `t.TempDir()`** for automatic temp directory cleanup (not manual)
- **Prefer table-driven tests** with `t.Run()` for comprehensive coverage
- **Structure tests clearly**: setup → execution → assertion
- **Use fuzzing** for complex input validation functions
- **Keep benchmarks minimal** - remove unnecessary setup/teardown

#### Modern Go Patterns
- Use `any` instead of `interface{}`
- Use `for i := range n` instead of traditional loops (Go 1.22+)
- Pre-compile regex at package level to avoid memory leaks
- Store interfaces directly in `atomic.Value`, not pointers

### Standard Library First
- URL parsing: `url.Parse()`
- IP operations: `net.ParseIP()`, `ip.IsPrivate()`
- Path operations: `filepath.Join()`, `filepath.Clean()`
- Time operations: `time` package functions
- Never use string manipulation for these operations
- For error handling use `internal/errors`
- For logging use `internal/logging`

### Common Pitfalls
- Remove unused variables even if for "compatibility"
- Format with gofmt before committing
- Check for nil before dereferencing
- Handle JSON encoding errors even after headers set
- Use safe type assertions: `if v, ok := x.(Type); ok { }`
- Avoid circular dependencies in init code (use `fmt.Errorf` not internal errors)
- **Avoid recursive implementations** - use standard library equivalents
- **Document test-only methods** clearly to prevent production use
- **Use explicit conversions** over clever arithmetic (e.g., `fmt.Sprintf` vs rune math)

### Project Structure
- `/cmd/` - Viper managed cli commands
- `/internal/` - Private packages (not importable externally)
- `/pkg/` - Public reusable packages
- `/internal/api/v2/` - New API endpoints
- Follow Go's internal package conventions

### Documentation Standards
- **Document all exported types, functions, and packages**
- **Use standard Go doc format**: `// TypeName does...`
- **Include examples** for complex functionality
- **Mark test-only methods** clearly in comments

### Review Process
1. Run `golangci-lint run -v` for entire module
2. Run `go test -race -v`
3. Format markdown with prettier
4. Address ALL review comments systematically

### Code Design Patterns
- **Use adapter pattern** for interface compatibility
- **Implement dependency injection** for better testability
- **Copy under read lock** to prevent deadlocks with RWMutex
- **Continue processing on errors** - don't fail entire chains
- **Provide fallback mechanisms** (e.g., default loggers)
- **Chain contexts properly** - preserve parent cancellation

## Quick Reference
- Always validate inputs
- Prefer standard library
- Run linter before commit
- New APIs in v2 only
- Branch from latest main
- Use modern Go patterns
- Test without time dependencies
- Use `t.TempDir()` not manual cleanup
- Document all exports