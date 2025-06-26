# BirdNET-Go Development Notes

## Project Overview
BirdNET-Go is a Go implementation of BirdNET for real-time bird sound identification with telemetry and observability features.

## Go Code Quality Guidelines

### Common Linter Issues to Avoid

Based on golangci-lint analysis, avoid these patterns:

1. **errorlint**: Always use `errors.As()` for error type checking
   ```go
   // Bad
   if validationErr, ok := err.(ValidationError); ok {
   
   // Good
   var validationErr ValidationError
   if errors.As(err, &validationErr) {
   ```

2. **gocritic paramTypeCombine**: Combine parameters of same type
   ```go
   // Bad
   func StartSpan(ctx context.Context, operation string, description string)
   
   // Good
   func StartSpan(ctx context.Context, operation, description string)
   ```

3. **gocritic ifElseChain**: Use switch statements for multiple conditions
   ```go
   // Bad
   if condition1 {
   } else if condition2 {
   } else {
   }
   
   // Good
   switch {
   case condition1:
   case condition2:
   default:
   }
   ```

4. **gocritic emptyStringTest**: Use direct string comparison
   ```go
   // Bad
   return len(s) > 0
   
   // Good
   return s != ""
   ```

5. **gocritic regexpSimplify**: Simplify regex patterns
   ```go
   // Bad
   regexp.MustCompile(`[^\s]+`)
   
   // Good
   regexp.MustCompile(`\S+`)
   ```

6. **gocritic octalLiteral**: Use new octal literal syntax
   ```go
   // Bad
   os.MkdirAll(dir, 0755)
   
   // Good
   os.MkdirAll(dir, 0o755)
   ```

7. **gosimple**: Use append with variadic syntax when possible
   ```go
   // Bad
   for _, item := range items {
       result = append(result, item)
   }
   
   // Good
   result = append(result, items...)
   ```

8. **ineffassign**: Avoid unused variable assignments
   ```go
   // Bad
   span, ctx := StartSpan(ctx, "op", "desc") // ctx reassigned but not used
   
   // Good
   span, _ := StartSpan(ctx, "op", "desc")
   ```

### Development Commands
- Linting: `golangci-lint run -v`
- Always run linter before committing code

### Custom Errors Package Usage

**MANDATORY**: Always use the enhanced error handling from `internal/errors/` package for all error returns in the codebase. This ensures consistent telemetry integration and meaningful error reporting in Sentry.

When importing and using the custom errors package:

```go
import (
    "github.com/tphakala/birdnet-go/internal/errors"
)
```

**Important**: 
- Always import as `errors` (not as an alias like `customerrors`) to maintain consistency with the existing codebase patterns
- Do NOT import the standard `errors` package alongside the custom one - the custom package provides all standard error functions (`errors.Is()`, `errors.As()`, etc.)
- The custom errors package provides enhanced error handling with automatic telemetry integration and categorization

Use the fluent builder pattern for creating enhanced errors:
```go
// For wrapping existing errors
return errors.New(err).
    Component("component_name").
    Category(errors.CategoryNetwork).
    Context("key", "value").
    Build()

// For creating new errors with descriptive messages
return errors.Newf("component: failed to perform operation").
    Component("component_name").
    Category(errors.CategoryValidation).
    Context("operation", "specific_operation").
    Build()
```

The package automatically reports errors to Sentry when configured, with privacy-safe context data.

**Never use plain error returns** like:
- `return fmt.Errorf("error: %w", err)` 
- `return errors.New("some error")` (standard library)
- `return err` (without enhancement)

Instead, always enhance errors with component, category, and context information for better observability.

### Telemetry Error Message Guidelines

For better telemetry identification and debugging, always use descriptive error messages that clearly identify the component and operation:

#### Best Practices for Error Messages

1. **Use Component Prefixes**: Always prefix error messages with the component name for clear identification in telemetry:
   ```go
   // Good
   errors.New(fmt.Errorf("birdweather: failed to upload soundscape: %w", err))
   errors.New(fmt.Errorf("diskmanager: failed to get disk usage statistics: %w", err))
   errors.New(fmt.Errorf("imageprovider: failed to parse Wikipedia API response: %w", err))
   
   // Bad
   errors.New(err) // Generic error without context
   ```

2. **Be Operation-Specific**: Include specific operation details in error messages:
   ```go
   // Good
   "BirdNET: failed to initialize analysis model"
   "diskmanager: failed to parse audio filename format"
   "imageprovider: failed to fetch Wikipedia pages"
   
   // Bad
   "initialization failed"
   "parsing failed"
   "fetch failed"
   ```

3. **Use Consistent Naming**: Use lowercase component names separated by colons:
   ```go
   "component: specific operation description: %w"
   ```

4. **Always Wrap Original Errors**: Use `%w` to preserve error chains:
   ```go
   errors.New(fmt.Errorf("component: operation failed: %w", originalErr))
   ```

#### Error Category Usage

Combine descriptive messages with appropriate error categories:
```go
return errors.New(fmt.Errorf("diskmanager: failed to walk directory for audio files: %w", err)).
    Component("diskmanager").
    Category(errors.CategoryFileIO).
    Context("operation", "walk_directory").
    Context("base_dir", baseDir).
    Build()
```

#### Component Naming Standards

- **birdweather**: BirdWeather API client operations
- **diskmanager**: Disk management and file operations  
- **imageprovider**: Image fetching and caching operations
- **birdnet**: BirdNET model operations
- **myaudio**: Audio processing operations

These guidelines ensure that telemetry errors in Sentry have clear, searchable titles that immediately identify the failing component and operation.

## Additional Cursor Rules Integration

### Go Best Practices (from .cursor/rules/go.mdc)

#### Code Style Standards
- Functions should be focused and concise (typically under 50 lines)
- Keep cognitive complexity low (aim for under 50)
- Use switch statements instead of long if-else chains
- Name return values in function signatures for better documentation and to avoid gocritic unnamedResult errors
- Order struct fields to minimize padding
- Use consistent naming conventions (MixedCaps, consistent acronym casing)

#### Error Handling Best Practices
- Always use `errors.Is()` and `errors.As()` for error comparison instead of `==` or `!=` operators
- Use error wrapping with `fmt.Errorf("... %w", err)` to preserve error types
- Return early for error conditions
- Check errors from all I/O operations, especially in defer statements

#### Performance Guidelines
- Pre-allocate slices when size is known
- Use `strings.Builder` instead of string concatenation
- Pass large structs by pointer to avoid copying
- Use `sync.Pool` for frequently allocated objects
- Combine multiple append operations into single call when possible

#### Security & Resource Management
- Use `os.Root` (Go 1.24+) for sandboxed filesystem access to prevent path traversal
- Always pass `context.Context` as first parameter for cancellable operations
- Implement proper resource cleanup in defer statements
- Monitor goroutine leaks and implement proper cancellation

#### Modern Go Features (Go 1.21+)
- Use built-in `min`, `max`, and `clear` functions
- Leverage `slices` and `maps` packages for operations
- Use `log/slog` for structured logging
- Range over integers directly: `for i := range 10 { ... }`
- Use `cmp` package for comparisons

#### Testing Standards
- Write table-driven tests with subtests (`t.Run`)
- Use `t.Cleanup()`, `t.TempDir()`, `t.Setenv()` for test setup
- Use `t.Parallel()` when tests can run concurrently
- Implement benchmark tests with `b.Loop()` (Go 1.24)

#### Linter Configuration
Essential linters to run:
- `durationcheck`: Detect incorrect time.Duration operations
- `errcheck`: Ensure error returns are checked
- `errorlint`: Enforce errors.Is/errors.As usage
- `gocognit`: Keep cognitive complexity manageable
- `gocritic`: Detect code improvement opportunities
- `gosimple`: Simplify code
- `ineffassign`: Detect ineffectual assignments
- `staticcheck`: Wide range of code improvements