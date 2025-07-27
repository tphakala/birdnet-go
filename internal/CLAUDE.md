# Go Coding Standards

## Import Rules
- **Use** `"github.com/tphakala/birdnet-go/internal/errors"` (never standard `"errors"`)
- **Use** `internal/logging` for structured logging
- Specify `.Component()` and `.Category()` for telemetry
- Register new components in error package's `init()`

## Error Handling
- Wrap errors: `fmt.Errorf("operation failed: %w", err)`
- Use sentinel errors: `var ErrNotFound = errors.New("not found")`
- Log but continue on batch operation failures
- Provide detailed context in messages

## Testing
- `t.Parallel()` only for independent tests
- No `time.Sleep()` - use channels/sync
- Use `t.TempDir()` for temp files
- Test with `go test -race`
- Table-driven tests with `t.Run()`
- `b.ResetTimer()` after benchmark setup

## Modern Go (1.22+)
- `any` not `interface{}`
- `for i := range n` for loops
- Pre-compile regex at package level
- Store interfaces in `atomic.Value` directly

## Standard Library First
- URLs: `url.Parse()`
- IPs: `net.ParseIP()`, `ip.IsPrivate()`
- Paths: `filepath.Join()`, `filepath.Clean()`
- Never manual string parsing for these

## Common Patterns
- Safe type assertions: `if v, ok := x.(Type); ok { }`
- Avoid circular dependencies in init
- Accept interfaces, return concrete types
- Copy data under read lock (RWMutex)
- Chain contexts properly
- Use dependency injection
- Document all exports: `// TypeName does...`

## Security
- Validate all user input
- Check path traversal, injection attacks
- Validate UUIDs properly

## Checklist
- Remove unused variables
- Run gofmt before commit
- Check nil before dereferencing
- No magic numbers - use consts
- Handle errors even after headers set
- Mark test-only methods in comments