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

### Testing Guidelines
- **Use modern Go testing patterns** for better test reliability and performance
- **Always use t.Parallel()** in tests when they can run concurrently:
  - Call `t.Parallel()` at the beginning of test functions that don't mutate global state
  - For table-driven tests, call `t.Parallel()` within subtests if test cases are independent
  - Avoid `t.Parallel()` when tests mutate global state or shared resources
  - Example:
    ```go
    func TestSomething(t *testing.T) {
        t.Parallel() // Enable parallel execution at top level
        
        testCases := []struct{
            name string
            // test case fields
        }{
            // test cases
        }
        
        for _, tc := range testCases {
            t.Run(tc.name, func(t *testing.T) {
                t.Parallel() // Enable parallel execution for subtests
                // test logic
            })
        }
    }
    ```
- **Use table-driven tests** with subtests (`t.Run()`) for comprehensive test coverage
- **Use modern testing utilities**:
  - `t.Cleanup()` instead of manual defer cleanup for resource management
  - `t.TempDir()` for temporary directories in tests
  - `t.Setenv()` for setting environment variables in tests
  - `b.Loop()` for Go 1.24+ benchmark tests instead of manual loops
- **Use testify for assertions**:
  - Import `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/require`
  - Use `assert` for readable assertions that continue on failure
  - Use `require` for critical assertions that should abort the test on failure
  - Use `testify/mock` for dependency mocking
- **Follow Go test naming conventions**:
  - Unit tests: `func TestXxx(t *testing.T)`
  - Benchmark tests: `func BenchmarkXxx(b *testing.B)`
  - Fuzz tests: `func FuzzXxx(f *testing.F)`
- **Use proper error handling in tests**:
  - Use `t.Fatal`, `t.Error`, `t.Fatalf`, or `t.Errorf` for reporting failures
  - Avoid panics in test code
  - Format error messages clearly: `t.Errorf("expected %v, got %v", want, got)`
- **Use test-specific logging**:
  - Use `t.Log()` and `t.Logf()` for test-local logging
  - Avoid `fmt.Println()` as it's not tied to test output
- **Run tests with race detection**: Use `go test -race` for concurrent test suites
- **Keep tests organized**: Place test files in the same package as the code they test (`*_test.go` files)


### Common Linter Issues to Avoid