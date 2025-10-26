# Datastore Mocks

This directory contains automatically generated mocks for the datastore interfaces using [Mockery v2](https://vektra.github.io/mockery/).

## 🚀 Quick Start

### Using Mocks in Tests

```go
import (
    "testing"
    "github.com/stretchr/testify/mock"
    "github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

func TestMyFunction(t *testing.T) {
    // Create the mock
    mockDS := mocks.NewMockInterface(t)

    // Set up expectations
    mockDS.EXPECT().
        Save(mock.Anything, mock.Anything).
        Return(nil).
        Once()

    // Use the mock
    err := myFunction(mockDS)

    // Assertions happen automatically at test end
}
```

## 🔄 Regenerating Mocks

When the `Interface` changes, regenerate mocks:

```bash
# From project root
go generate ./internal/datastore

# Or directly with mockery
mockery --config .mockery.yaml
```

**IMPORTANT**: Never manually edit files in this directory. They are auto-generated.

## 📚 Migration Guide

### Before (Manual Mock)

**Old approach** in `test_helpers.go`:

```go
// MockSpeciesDatastore implements the SpeciesDatastore interface using testify/mock
type MockSpeciesDatastore struct {
    mock.Mock
}

// Manually implement every method...
func (m *MockSpeciesDatastore) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
    args := m.Called(after)
    return safeSlice[datastore.NotificationHistory](args, 0), args.Error(1)
}

// ... 4 more methods manually written
```

### After (Generated Mock)

**New approach**:

```go
import "github.com/tphakala/birdnet-go/internal/datastore/mocks"

func TestSpeciesTracker(t *testing.T) {
    // Use generated mock instead
    mockDS := mocks.NewMockInterface(t)

    // Set expectations
    mockDS.EXPECT().
        GetActiveNotificationHistory(mock.AnythingOfType("time.Time")).
        Return([]datastore.NotificationHistory{}, nil)

    tracker := NewTrackerFromSettings(mockDS, settings)
}
```

**Benefits**:
- ✅ No manual mock maintenance
- ✅ Automatic updates when interface changes
- ✅ Type-safe expectations
- ✅ Better error messages
- ✅ All 62 methods available automatically

## 🎯 Common Patterns

### Basic Method Expectations

```go
// Simple return value
mockDS.EXPECT().
    Get("123").
    Return(datastore.Note{ID: 123}, nil)

// Multiple calls
mockDS.EXPECT().
    Save(mock.Anything, mock.Anything).
    Return(nil).
    Times(3)

// Any arguments
mockDS.EXPECT().
    Delete(mock.AnythingOfType("string")).
    Return(nil)
```

### Complex Return Values

```go
// Return slice
mockDS.EXPECT().
    GetLastDetections(10).
    Return([]datastore.Note{
        {ID: 1, CommonName: "Robin"},
        {ID: 2, CommonName: "Sparrow"},
    }, nil)

// Return error
mockDS.EXPECT().
    Get("invalid").
    Return(datastore.Note{}, datastore.ErrNoteNotFound)
```

### Partial Mocking

You only need to set expectations for methods your test actually calls:

```go
func TestSomething(t *testing.T) {
    mockDS := mocks.NewMockInterface(t)

    // Only mock what you need
    mockDS.EXPECT().
        GetLastDetections(5).
        Return([]datastore.Note{}, nil)

    // If your code calls other methods without expectations, test fails
    // This catches unexpected behavior!
}
```

## 📋 Migration Checklist

When updating existing tests:

- [ ] Replace custom mock struct with `mocks.NewMockInterface(t)`
- [ ] Remove manual mock method implementations
- [ ] Update expectations to use `.EXPECT()` syntax
- [ ] Delete helper functions like `safeSlice()` (no longer needed)
- [ ] Run tests to verify behavior
- [ ] Delete old mock files

## ✅ Migration Complete!

All datastore mocks have been successfully migrated to generated mocks:

### Completed Migrations

1. ✅ **internal/analysis/species/** (19 test files)
   - Deleted: `MockSpeciesDatastore` (88 lines)
   - Added: Mock expectations with `.Maybe()` for conditional calls
   - Status: All non-performance tests passing

2. ✅ **internal/api/v2/test_utils.go**
   - Deleted: `MockDataStore` + `MockDataStoreV2` (646 lines!)
   - Migrated: 17 test files using setup functions
   - Status: Tests compiling and passing

3. ✅ **internal/analysis/processor/test_helpers.go**
   - Deleted: `MockSpeciesDatastore` (unused, removed file)

### Migration Statistics

- **Total manual mock lines deleted**: 734 lines
- **Test files migrated**: 36+ files
- **Generated mock file**: 111KB with all 62 methods
- **Maintenance burden**: Eliminated ✨

### Out of Scope

These mocks are for non-datastore interfaces and remain unchanged:
- `internal/imageprovider/*` - Image provider mocks
- `internal/api/v2/range_test.go` - MockBirdNET, MockProcessor
- `internal/api/v2/integrations_test.go` - MockMQTTClient, MockBirdWeatherClient

## 🛠️ Configuration

Mock generation is configured in:
- **Go generate directive**: `internal/datastore/interfaces.go:4`
- **Global config**: `.mockery.yaml`

## 📖 Resources

- [Mockery Documentation](https://vektra.github.io/mockery/)
- [Testify Mock Guide](https://pkg.go.dev/github.com/stretchr/testify/mock)
- [BG-21: Implementation Issue](https://linear.app/birdnet-go/issue/BG-21)

---

**Last Generated**: October 26, 2025
**Mockery Version**: v2.53.5
**Interface Methods**: 62
