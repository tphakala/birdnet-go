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

### Common Linter Issues to Avoid