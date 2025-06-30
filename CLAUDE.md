# BirdNET-Go Development Notes

## Project Overview
BirdNET-Go is a Go implementation of BirdNET for real-time bird sound identification with telemetry and observability features.

## Go Code Quality Guidelines

### Development Commands
- Linting: `golangci-lint run -v`
- Always run linter before committing code
- **Always run linter "golangci-lint run" to validate code before git commit**
- Format edited .go source files with gofmt to avoid linter errors
- Always run golangci-lint for full project instead of targeting it for work in progress file

### Security Guidelines
- **Always validate user input** to prevent security vulnerabilities such as:
  - Path traversal attacks (validate file paths don't contain ".." or escape intended directories)
  - Command injection (sanitize inputs used in system commands)
  - SQL injection (use parameterized queries)
  - UUID validation for identifiers (use proper UUID parsing to validate format)
- Never trust user-supplied data without proper validation and sanitization

### Error Handling Guidelines
- **Use the enhanced error handling system** for better observability and debugging
- See `internal/errors/README.md` for comprehensive error handling documentation
- Key points:
  - Import only `"github.com/tphakala/birdnet-go/internal/errors"` (never import standard `"errors"` package)
  - Always specify `.Component()` and `.Category()` for proper telemetry tagging
  - Register new components in the error package's `init()` function to avoid incorrect tagging
  - Use descriptive error messages and add context for better debugging
  - Example:
    ```go
    err := errors.New(originalErr).
        Component("birdweather").
        Category(errors.CategoryNetwork).
        Context("operation", "upload_soundscape").
        Build()
    ```

### Testing Best Practices
- **Always use `t.Parallel()`** in test functions and subtests to enable concurrent execution
  - Add `t.Parallel()` as the first line in test functions
  - Also add `t.Parallel()` inside each `t.Run()` subtest
  - This improves test suite performance by running tests concurrently
- **Avoid time-dependent tests** that rely on `time.Sleep()` or real-time delays
  - Use channels, mocks, or other deterministic approaches instead
  - Time-based tests can be flaky in CI environments
- **Use descriptive test names** that accurately reflect what is being tested
  - Avoid misleading names that don't match the test implementation
- **Benchmark setup considerations**:
  - Always call `b.ResetTimer()` after any setup code and before the benchmark loop
  - Pre-population or setup code should be done before `b.ReportAllocs()` and `b.ResetTimer()`
  - This ensures benchmarks only measure the intended code execution time

### Common Linter Issues to Avoid
- **Remove unused variables** even if kept for "backward compatibility"
- **Format code with gofmt** - linter will fail on improperly formatted files
- **Modern Go patterns**:
  - Replace `interface{}` with `any` (Go 1.18+)
  - Use modern loop patterns (see below)

### Modern Go Loop Patterns (Go 1.22+)
- **Use modern range syntax** instead of traditional for loops where applicable:
  - **Benchmarks**: Use `for b.Loop()` instead of `for i := 0; i < b.N; i++` (Go 1.24+)
  - **Integer ranges**: Use `for i := range n` instead of `for i := 0; i < n; i++` (Go 1.22+)
  - **Function iteration**: Use `for range iteratorFunc` for custom iterators (Go 1.23+)

### Go 1.24 Benchmark Pattern with b.Loop()
- **Key differences from traditional benchmarks**:
  - Use `for b.Loop() { }` instead of `for range b.Loop()` or `for i := 0; i < b.N; i++`
  - Setup code runs exactly once, not multiple times
  - Automatic timer management - only loop body is timed
  - Prevents unwanted compiler optimizations
  - After loop completes, `b.N` contains total iterations
- **Benchmark examples**:
  ```go
  // Preferred (Go 1.24+) - Using b.Loop()
  func BenchmarkExample(b *testing.B) {
      // Setup code here (expensive operations not timed)
      big := NewBig()
      
      b.ReportAllocs()
      // No need for b.ResetTimer() with b.Loop()
      for b.Loop() {
          // Only this code is timed
          big.Len()
      }
      // b.N now contains total iterations for computing averages
  }
  
  // Avoid - traditional pattern with b.N
  func BenchmarkExample(b *testing.B) {
      b.ReportAllocs()
      b.ResetTimer()
      for i := 0; i < b.N; i++ {
          // Old pattern - more error-prone
      }
  }
  ```
- **Always use `b.ReportAllocs()`** in benchmarks to track memory allocations
- **Benefits**: More robust, efficient, automatic timer management, prevents compiler optimizations

### Code Defensive Patterns
- Use defensive patterns, check for nils etc
- **Avoid pointer-to-interface anti-pattern**: Use `atomic.Value` instead of `atomic.Pointer[Interface]` for storing interfaces
- **Interface type parameters**: Use `any` instead of `interface{}` for better readability (Go 1.18+)

### Code Review Best Practices (Lessons from PR #834 and #836)
- **Fix typos immediately**: Even in comments/method names - they can cause compilation errors
- **Testing improvements**:
  - Always add `t.Parallel()` to test functions and subtests for concurrent execution
  - Replace `time.Sleep` with deterministic synchronization (channels, wait groups, polling helpers)
  - Use table-driven tests with subtests for better organization and parallel execution
  - Create helper functions like `waitForProcessed()` to avoid timing-dependent test failures
  - For time-dependent operations (like circuit breakers), use polling with deadline instead of fixed sleeps
- **Atomic operations**: When using `atomic.Value` to store interfaces:
  - Store the interface directly, not a pointer to it
  - Use type assertions when loading: `value.(InterfaceType)`
  - Simplify nil checks - one check is sufficient after loading
- **Performance considerations**:
  - Consider using efficient LRU implementations (e.g., `github.com/hashicorp/golang-lru/v2`) for caches
  - Batch operations when updating indices to avoid O(n) complexity in loops
  - Always utilize pre-compiled resources (templates, regexes) instead of wasting compilation effort
  - Implement proper batch processing with aggregation, not just iterating over items
- **Error handling patterns**: Maintain consistency in error category comparisons across the codebase
- **Code efficiency**: 
  - If you pre-compile templates or patterns, actually use them - don't leave them unused
  - Implement true batch processing with error aggregation and notification deduplication
  - Group similar errors to reduce notification spam and improve efficiency