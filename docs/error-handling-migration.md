# Error Handling Migration Guide

This document explains the error handling improvements made to address `nilerr` and `nilnil` linter issues while maintaining API compatibility.

## Summary of Changes

### Breaking Changes Avoided

Several functions that previously returned `nil, nil` were identified as having callers that depend on this behavior. To maintain API compatibility, these functions were NOT changed:

1. **`GetNoteReview`** - Callers check for `nil` result
2. **`GetNoteLock`** - Callers check for `nil` result  
3. **`GetImageCache`** - Caller in `imageprovider.go:439` checks `if cachedImage == nil`
4. **`Get` (notification)** - Caller in `notification/service.go` checks `if notification == nil`
5. **`createFilter`** - Caller checks `if filter != nil`
6. **`ProcessAudioData`** - Tests expect `nil, nil` for no data
7. **`FetchWeather`** - 304 Not Modified is not an error condition
8. **`Initialize` (eventbus)** - Callers expect `nil, nil` when disabled

### Safe Changes Made

The following changes were made because they don't break existing APIs:

1. **`generateSpectrogram`** in `media.go`
   - Uses `singleflight.Do()` which ignores the result value
   - Changed string literals to constants for better maintainability

2. **`collectJournalLogs`** in `support/collector.go`
   - Changed to return `ErrJournalNotAvailable` with error logging
   - Caller only checks error, not the result
   - Added test coverage

3. **`getSunEvents`** in `weather.go`
   - Used `//nolint:nilerr` directive for intentional fallback behavior
   - This preserves the original behavior while satisfying the linter

## Future Considerations

For functions that currently return `nil, nil`, consider these approaches in future major versions:

1. **Option Types**: Use a Result type that explicitly indicates "not found" vs error
2. **Sentinel Values**: Return empty structs instead of nil for "not found"
3. **Error Wrapping**: Use sentinel errors that callers can check with `errors.Is()`

## Testing

When changing error handling:

1. Identify all callers using grep/search tools
2. Check how callers handle the `nil, nil` case
3. Add tests for new error conditions
4. Run integration tests to ensure compatibility

## Example Safe Pattern

```go
// Old pattern that breaks callers:
func GetSomething() (*Thing, error) {
    // ...
    if notFound {
        return nil, nil // Callers check: if thing == nil
    }
}

// Safe pattern for new code:
var ErrThingNotFound = errors.NewStd("thing not found")

func GetSomething() (*Thing, error) {
    // ...
    if notFound {
        return nil, ErrThingNotFound // Callers check: errors.Is(err, ErrThingNotFound)
    }
}
```