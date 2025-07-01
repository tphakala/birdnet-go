# BirdNET-Go Development Notes

## Project Overview
BirdNET-Go is a Go implementation of BirdNET for real-time bird sound identification with telemetry and observability features.

## API Development Guidelines
- **IMPORTANT: Do not expand HTTP API v1 anymore**
  - All new API endpoints must be created in `internal/api/v2/`
  - API v1 is deprecated and should not receive new functionality
  - Follow the existing patterns in v2 for consistency

## Git Branch Management Guidelines

### Creating New Branches
- **ALWAYS create new branches from the latest main branch** to avoid missing recent changes:
  ```bash
  git checkout main
  git pull origin main
  git checkout -b feat/your-new-feature
  ```
- **Check for related PRs before starting work**:
  - Review open PRs that might touch similar code areas
  - Look at recently merged PRs that might not be in your base branch
  - Use GitHub's PR search: `is:pr is:merged` to see recent merges
- **Understand PR merge timing**:
  - Multiple PRs can be in flight simultaneously
  - PR A might be created before PR B but merged after it
  - Creating a branch from a recently merged PR might miss other concurrent changes
- **Rebase regularly during development**:
  ```bash
  git fetch origin
  git rebase origin/main
  ```
  This helps catch conflicts early and ensures you have the latest changes
- **Before creating a PR**:
  - Fetch and rebase on latest main one more time
  - Run linter and tests to catch any integration issues
  - Check if any new PRs were merged while you were working

### Why This Matters
When multiple PRs are being developed in parallel:
- PR #1 (created Monday, merged Wednesday) adds feature A
- PR #2 (created Tuesday, merged Thursday) adds feature B
- If you branch from PR #2's merge commit, you might miss PR #1's changes if they touched different files
- This can lead to "surprise" conflicts when merging to main later

## Go Code Quality Guidelines

### Development Commands
- Linting: `golangci-lint run -v`
- **ALWAYS run linter "golangci-lint run -v" to validate code before EVERY git commit**
- Format edited .go source files with gofmt to avoid linter errors
- Always run golangci-lint for full project instead of targeting it for work in progress file
- If linter finds issues, fix them BEFORE committing - no exceptions

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
- **Use `t.Parallel()` carefully** to enable concurrent test execution:
  - Add `t.Parallel()` as the first line in test functions that don't share state
  - Add `t.Parallel()` inside `t.Run()` subtests only when they're truly independent
  - **IMPORTANT: Do NOT use `t.Parallel()` when**:
    - Tests access shared singleton instances
    - Tests modify global state or package-level variables
    - Subtests access the same resource that could be modified
    - Tests rely on specific execution order
  - Example of when NOT to use parallel:
    ```go
    // BAD - subtests access shared singleton
    func TestSingleton(t *testing.T) {
        manager := GetSingletonManager()
        
        t.Run("Test1", func(t *testing.T) {
            t.Parallel() // WRONG! This will cause race conditions
            manager.Modify()
        })
    }
    
    // GOOD - subtests don't share state
    func TestIndependent(t *testing.T) {
        t.Run("Test1", func(t *testing.T) {
            t.Parallel() // OK - creates its own instance
            obj := NewObject()
            obj.Test()
        })
    }
- **Avoid time-dependent tests** that rely on `time.Sleep()` or real-time delays
  - Use channels, mocks, or other deterministic approaches instead
  - Replace `time.Sleep` with channel-based synchronization:
    ```go
    // Bad: time.Sleep(30 * time.Millisecond)
    // Good: Use channels for synchronization
    ready := make(chan struct{})
    go func() {
        <-ready
        // perform action
    }()
    close(ready) // signal readiness
    ```
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
- **Clean up impossible conditions**:
  - Remove variables that are never assigned (e.g., `var err error` only used inside sync.Once)
  - The linter detects impossible nil checks - fix them

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

### Defensive Programming Patterns
- **Nil checks and fallbacks**:
  - Always check for nil before dereferencing pointers
  - Provide fallback values for uninitialized globals
  - Use defensive initialization patterns
- **Atomic operations best practices**:
  - Use `atomic.Value` instead of `atomic.Pointer[Interface]` for storing interfaces
  - Always use safe type assertions when loading from atomic.Value
  - Store concrete types or interfaces directly, not pointers to them
- **Interface handling**:
  - Use `any` instead of `interface{}` for better readability (Go 1.18+)
  - Always use type assertion with `ok` check for safety
- **Context handling**:
  - Always check context cancellation in long-running operations
  - Respect context timeouts in HTTP handlers
  - Pass context as first parameter to functions that may be cancelled

### Initialization and Circular Dependencies (Lessons from PR #839)
- **Avoid circular dependencies in initialization code**:
  - Initialization code (e.g., `init_manager.go`) must use `fmt.Errorf` instead of internal errors package
  - This prevents circular dependencies: `telemetry → errors → telemetry`
  - Document this choice with comments: `// Using fmt instead of errors package to avoid circular dependencies`
- **Defensive initialization patterns**:
  - Check for nil global variables before use
  - Use sync.Once for singleton initialization
  - Provide fallback mechanisms when dependencies might not be initialized
  - Example:
    ```go
    var coordinator *InitCoordinator
    if globalInitCoordinator != nil {
        coordinator = globalInitCoordinator
    }
    ```
- **Clean variable scoping in sync.Once**:
  - Don't declare variables outside sync.Once if they're only used inside
  - This avoids impossible nil checks and cleaner code

### HTTP Handler Best Practices
- **Always handle JSON encoding errors**:
  - Even when HTTP headers are already set
  - Log errors for debugging but don't change response status
  - Example:
    ```go
    if err := json.NewEncoder(w).Encode(response); err != nil {
        logger.Error("failed to encode response", "error", err)
    }
    ```
- **Use request context for timeout handling**:
  - Check context cancellation early in handlers
  - Respect client timeout/cancellation signals
  - Example:
    ```go
    ctx := r.Context()
    select {
    case <-ctx.Done():
        w.WriteHeader(http.StatusRequestTimeout)
        return
    default:
    }
    ```

### Safe Type Assertions
- **Always use safe type assertions with atomic.Value**:
  - Check the `ok` value to prevent panics
  - Example:
    ```go
    if v := atomicVar.Load(); v != nil {
        if err, ok := v.(error); ok {
            // use err safely
        }
    }
    ```
- **Apply same pattern to all interface{}/any type assertions**

### Code Review Best Practices (Lessons from PR #834, #836, and #839)
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

### Code Review Process
- **Address all review comments systematically**:
  - Read each comment carefully, including inline comments
  - Consider the broader implications of suggested changes
  - Document decisions when not implementing suggestions (e.g., circular dependency prevention)
- **Run comprehensive checks before pushing**:
  - `golangci-lint run -v` for the entire module
  - `go test -race -v` to detect race conditions
  - Format markdown files with prettier for consistency
- **Modern Go patterns in reviews**:
  - Update `interface{}` to `any` when touching code
  - Use modern range syntax (`for range n` instead of `for i := 0; i < n; i++`)
  - Apply consistent patterns across the codebase