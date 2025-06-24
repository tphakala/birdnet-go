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