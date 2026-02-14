# Go Coding Standards

## Project Requirements

- **Go Version**: 1.26
- **Release Notes**: [https://go.dev/doc/go1.26](https://go.dev/doc/go1.26)

## Quick Reference

- Use `internal/errors` package (never standard `errors`)
- Structured logging with `internal/logging`
- Test with `-race` flag always
- No magic numbers - use constants
- Document all exports
- **Zero linter tolerance** - fix all issues before commit

## Go 1.26 Features

### Enhanced new() Function

Use `new()` with expressions for pointer initialization:

```go
// ✅ Go 1.26 - new() accepts expressions
existingFirstSeen: new(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC))

// ❌ Old pattern - wrapper functions unnecessary
func ptr[T any](v T) *T { return &v }
existingFirstSeen: ptr(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC))
```

### Green Tea GC (Default)

- **10-40% reduction in GC overhead** (enabled by default)
- Additional 10% improvement on newer CPUs (Intel Ice Lake, AMD Zen 4+)
- Opt-out: `GOEXPERIMENT=nogreenteagc` (will be removed in Go 1.27)

### Performance Improvements

- **30% faster cgo calls** - reduced baseline overhead
- **Faster io.ReadAll()** - ~2x improvement with better allocation strategy
- **JPEG decoder/encoder** - new, faster, more accurate implementation
- **Reduced fmt allocations** for unformatted strings

### Modern Standard Library

- **strings.Cut()** - replaces `strings.Index` + slicing patterns:

  ```go
  // ✅ Go 1.26 - cleaner and more efficient
  if host, port, found := strings.Cut(rawURL, ":"); found {
      return host
  }

  // ❌ Old pattern
  if colonIdx := strings.Index(rawURL, ":"); colonIdx != -1 {
      return rawURL[:colonIdx]
  }
  ```

- **errors.AsType()** - type-safe version of `errors.As()`:

  ```go
  // ✅ Go 1.26 - type-safe
  if pathErr := errors.AsType[*fs.PathError](err); pathErr != nil {
      // Use pathErr
  }

  // ❌ Old pattern
  var pathErr *fs.PathError
  if errors.As(err, &pathErr) {
      // Use pathErr
  }
  ```

- **bytes.Buffer.Peek()** - read without advancing position

### Preallocation Patterns

Preallocate slice capacity when size is known:

```go
// ✅ Preallocate capacity
fields := make([]logger.Field, 0, 5+len(extraFields))
fields = append(fields, baseFields...)
fields = append(fields, extraFields...)

// ❌ Multiple reallocations
fields := []logger.Field{}
fields = append(fields, baseFields...)
fields = append(fields, extraFields...)
```

### Security & Cryptography

- **Post-quantum ML-KEM** enabled by default in crypto/tls
- **crypto/hpke** - Hybrid Public Key Encryption (RFC 9180)
- **Secure randomness** - Random parameter in crypto functions now ignored (always secure)

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

- **Prefer `testing/synctest` over `time.Sleep()`** (Go 1.25)
- `t.Parallel()` only for independent tests
- Use `t.TempDir()` for temp files
- Test with `go test -race`
- Table-driven tests with `t.Run()`
- `b.ResetTimer()` after benchmark setup
- Use `t.Attr()` for test metadata (Go 1.25)

### Test Cleanup Best Practices

- **Use `t.Cleanup()` instead of `defer`** for test resource cleanup
- `t.Cleanup()` runs after all defers, providing more predictable cleanup order
- Particularly important for tests that restore global state
- Example:

  ```go
  func TestWithGlobalState(t *testing.T) {
      // ❌ Wrong - defer may run at unpredictable times
      originalValue := GetGlobalValue()
      defer SetGlobalValue(originalValue)

      // ✅ Correct - cleanup runs after all test defers
      originalValue := GetGlobalValue()
      t.Cleanup(func() {
          SetGlobalValue(originalValue)
      })
  }
  ```

### Test Parallelization Guidelines

- Add `t.Parallel()` to **test functions** and **subtests** for speed
- **NEVER parallelize tests that**:
  - Mutate global state (e.g., `conf.SetTestSettings()`)
  - Share mutable data structures without synchronization
  - Use shared map references without cloning
- **Always clone shared test data** in subtests:

  ```go
  import "maps"

  customSeasons := map[string]Season{...}
  for _, tt := range tests {
      t.Run(tt.name, func(t *testing.T) {
          t.Parallel()
          // Clone to avoid aliasing - prevents false positives
          settings.Seasons = maps.Clone(customSeasons)
      })
  }
  ```

### Test Helper File Naming

- Name test-only helper files with `_test.go` suffix
- **Wrong**: `internal/conf/test_helpers.go` (included in production builds)
- **Correct**: `internal/conf/test_helpers_test.go` (test-only)
- This ensures helpers with `*testing.T` parameters don't bloat binaries

### Benchmark Best Practices

- Always call `b.ReportAllocs()` before `b.ResetTimer()` to track allocations
- Use `b.Loop()` (Go 1.24+) for cleaner benchmark loops (optional)
- Benchmark example:

  ```go
  func BenchmarkValidation(b *testing.B) {
      cfg := &Config{...}
      b.ReportAllocs()
      b.ResetTimer()
      for i := 0; i < b.N; i++ {
          _ = Validate(cfg)
      }
  }
  ```

### Mock Generation with Mockery

**IMPORTANT**: Never manually write mocks. Use mockery for automated mock generation.

**Quick Start:**

```bash
# Generate mocks for all interfaces
go generate ./internal/datastore

# Or use mockery directly
mockery --config .mockery.yaml
```

**Using Generated Mocks in Tests:**

```go
import (
    "testing"
    "github.com/stretchr/testify/mock"
    "github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

func TestMyFunction(t *testing.T) {
    // Create mock
    mockDS := mocks.NewMockInterface(t)

    // Set expectations using .EXPECT() pattern
    mockDS.EXPECT().
        Save(mock.Anything, mock.Anything).
        Return(nil).
        Once()

    // Use the mock
    err := myFunction(mockDS)

    // Assertions happen automatically
}
```

**Critical Rules:**

- **Conditional Mock Calls**: Use `.Maybe()` for methods called conditionally

```go
// Method only called when NotificationSuppressionHours > 0
mockDS.EXPECT().
    GetActiveNotificationHistory(mock.AnythingOfType("time.Time")).
    Return([]datastore.NotificationHistory{}, nil).
    Maybe()  // Won't fail if not called
```

- **Async Operations**: Use `.Maybe()` for methods called in goroutines

```go
// Called asynchronously in RecordNotificationSent
mockDS.EXPECT().
    SaveNotificationHistory(mock.AnythingOfType("*datastore.NotificationHistory")).
    Return(nil).
    Maybe()  // Non-blocking operation
```

- **Test Helpers**: Always use `t.Helper()` in setup functions

```go
func createTestTracker(t *testing.T) *Tracker {
    t.Helper()  // Stack traces point to caller, not this function
    // ... setup
}
```

**Common Patterns:**

```go
// Match any argument type
mockDS.EXPECT().Get(mock.Anything).Return(note, nil)

// Match specific type
mockDS.EXPECT().Save(mock.AnythingOfType("*datastore.Note")).Return(nil)

// Multiple calls
mockDS.EXPECT().Get(mock.Anything).Return(note, nil).Times(3)

// Return different values on subsequent calls
mockDS.EXPECT().Get("123").Return(note1, nil).Once()
mockDS.EXPECT().Get("123").Return(note2, nil).Once()
```

**When Interface Changes:**

1. Update the interface in `internal/datastore/interfaces.go`
2. Run `go generate ./internal/datastore`
3. Mocks automatically regenerate with all methods
4. **Never** manually edit files in `internal/datastore/mocks/`

**Documentation:**

- Complete guide: `internal/datastore/mocks/README.md`
- Configuration: `.mockery.yaml`
- Migration guide and examples in README

## Go 1.25 Testing Features

See [Go 1.25 Release Notes](https://go.dev/doc/go1.25) for complete changelog.

### testing/synctest - Deterministic Concurrent Testing

Replace flaky sleep-based tests with deterministic scheduling:

```go
// ❌ Old pattern - unreliable timing
time.Sleep(100 * time.Millisecond)

// ✅ New pattern - deterministic
import "testing/synctest"

func TestConcurrent(t *testing.T) {
    synctest.Test(t, func() {
        // Time moves instantly when all goroutines are blocked
        // Perfect for testing timeouts, retries, rate limiting
    })
}
```

### sync.WaitGroup.Go() - Cleaner Goroutines

```go
// ❌ Old pattern
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    // work
}()

// ✅ New pattern - automatic Add/Done
var wg sync.WaitGroup
wg.Go(func() {
    // work
})
```

### Test Output & Attributes

```go
func TestAPI(t *testing.T) {
    // Add test metadata
    t.Attr("component", "api")
    t.Attr("version", "v2")

    // Structured output
    output := t.Output()
    fmt.Fprintf(output, "Request: %v\n", req)
}
```

### runtime/trace.FlightRecorder - Production Diagnostics

Capture lightweight traces only when needed:

```go
import "runtime/trace"

recorder := trace.NewFlightRecorder()
defer recorder.Stop()

// Process audio/data
if err != nil {
    // Save trace only on error
    recorder.WriteTo(errorLog)
}
```

### encoding/json/v2 (Experimental)

For performance-critical JSON operations:

```go
import jsonv2 "encoding/json/v2"

// ~2x faster for API responses
data, err := jsonv2.Marshal(response)
```

## Benchmarks (Go 1.25)

- Use `b.Loop()` instead of manual `for i := 0; i < b.N; i++`
- Use `b.TempDir()` instead of `os.MkdirTemp()`
- Call `b.ReportAllocs()` to track memory allocations
- Container-aware GOMAXPROCS adjusts CPU automatically

## Modern Go (1.25+)

- `any` not `interface{}`
- `for i := range n` for loops
- Pre-compile regex at package level
- Store interfaces in `atomic.Value` directly
- Use `os.Root` for filesystem sandboxing (<https://go.dev/blog/osroot>)
- Use `sync.WaitGroup.Go()` for goroutines (<https://pkg.go.dev/sync#WaitGroup.Go>)
- Use `testing/synctest` for concurrent tests (<https://go.dev/blog/synctest>)

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

## Dependency Injection for Testability

- **Pass dependencies as interfaces** through constructors or struct fields
- **Avoid global state** - inject configuration, loggers, and clients
- **Define minimal interfaces** close to where they're used
- **Constructor pattern**: `NewService(deps...) *Service`
- **Identify untestable code** - if you see direct instantiation of external dependencies, flag it
- **Example pattern**:

  ```go
  type Store interface {
      Get(id string) (*Item, error)
  }

  type Service struct {
      store Store  // inject interface, not concrete type
  }

  func NewService(store Store) *Service {
      return &Service{store: store}
  }
  ```

- **Common injection targets**: databases, HTTP clients, file systems, time providers
- **If you encounter code that would benefit from DI**, communicate it rather than leaving it untestable

## Security

- Validate all user input
- Check path traversal, injection attacks
- Validate UUIDs properly

## Goroutine Leak Detection

Add to tests that create services/goroutines:

```go
defer goleak.VerifyNone(t,
    goleak.IgnoreTopFunction("testing.(*T).Run"),
    goleak.IgnoreTopFunction("runtime.gopark"),
    goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
)
```

- Always `defer service.Stop()` after creating services
- Use local service instances, not global singletons
- Use 500ms+ timeouts for async operations (CI reliability)

## Linter Compliance (Zero Tolerance)

### Active Linters & Common Fixes

| Linter           | Purpose                 | Common Fixes                                 |
| ---------------- | ----------------------- | -------------------------------------------- |
| **errorlint**    | Error handling          | Use `errors.Is()`, `errors.As()` not `==`    |
| **errname**      | Error naming            | Prefix errors with `Err`: `var ErrNotFound`  |
| **nilerr**       | Nil error returns       | Don't return nil error with non-nil value    |
| **nilnil**       | Nil returns             | Avoid `return nil, nil` - return zero value  |
| **bodyclose**    | HTTP bodies             | Always `defer resp.Body.Close()`             |
| **ineffassign**  | Unused assignments      | Remove or use assigned values                |
| **staticcheck**  | Static analysis         | Fix all SA\* warnings                        |
| **gocritic**     | Style/performance       | Follow suggestions (rangeValCopy, etc.)      |
| **gocognit**     | Complexity              | Split functions >50 complexity               |
| **gocyclo**      | Cyclomatic complexity   | Refactor complex functions                   |
| **dupl**         | Duplication             | Extract common code                          |
| **misspell**     | Spelling                | Fix typos in comments/strings                |
| **unconvert**    | Unnecessary conversions | Remove redundant type conversions            |
| **wastedassign** | Wasted assignments      | Remove unused assignments                    |
| **prealloc**     | Slice preallocation     | Use `make([]T, 0, cap)` when size known      |
| **exhaustive**   | Switch exhaustiveness   | Handle all enum cases or add default         |
| **testifylint**  | Testify usage           | Use `assert.Equal` not `assert.True(a == b)` |
| **thelper**      | Test helpers            | Add `t.Helper()` to test functions           |
| **fatcontext**   | Context usage           | Don't store context in structs               |
| **iface**        | Interface pollution     | Accept interfaces, return structs            |

### Common Fixes by Category

#### Error Handling

```go
// ❌ Wrong
if err == io.EOF { }

// ✅ Correct
if errors.Is(err, io.EOF) { }

// ❌ Wrong - nilerr
if err != nil {
    return nil, nil
}

// ✅ Correct
if err != nil {
    return nil, err
}
```

#### Resource Management

```go
// ❌ Wrong - bodyclose
resp, _ := http.Get(url)

// ✅ Correct
resp, err := http.Get(url)
if err != nil {
    return err
}
defer resp.Body.Close()
```

#### Test Helpers

```go
// ❌ Wrong - thelper
func assertSomething(t *testing.T, val int) {
    if val != 42 {
        t.Errorf("expected 42")
    }
}

// ✅ Correct
func assertSomething(t *testing.T, val int) {
    t.Helper() // Add this
    if val != 42 {
        t.Errorf("expected 42")
    }
}
```

#### Performance

```go
// ❌ Wrong - prealloc
var results []string
for _, item := range items {
    results = append(results, item)
}

// ✅ Correct
results := make([]string, 0, len(items))
for _, item := range items {
    results = append(results, item)
}
```

## Pre-Commit Checklist

- [ ] Run `golangci-lint run -v` - **MUST have zero errors**
  - **Always run on full project** - never single files/packages (incomplete results)
  - **Primary compilation validation** - don't run `go build` separately
- [ ] Run `go test -race -v`
- [ ] Check all linter categories above
- [ ] No disabled linters with `//nolint` without justification
- [ ] Document all exports
- [ ] Handle all errors properly

## Linter Configuration Notes

- Config: `.golangci.yaml` (v2 format)
- Complexity threshold: 50 (gocognit)
- Disabled checks: commentFormatting, commentedOutCode (gocritic)
- Exhaustive switches: `default` case marks as exhaustive
- gosec disabled but configured for future use
