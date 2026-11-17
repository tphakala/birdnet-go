package migration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// mockWeatherRepo implements repository.WeatherRepository for testing.
type mockWeatherRepo struct {
	mock.Mock
}

func (m *mockWeatherRepo) SaveDailyEvents(ctx context.Context, events *entities.DailyEvents) error {
	args := m.Called(ctx, events)
	return args.Error(0)
}

func (m *mockWeatherRepo) GetDailyEvents(ctx context.Context, date string) (*entities.DailyEvents, error) {
	args := m.Called(ctx, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.DailyEvents), args.Error(1)
}

func (m *mockWeatherRepo) SaveHourlyWeather(ctx context.Context, weather *entities.HourlyWeather) error {
	args := m.Called(ctx, weather)
	return args.Error(0)
}

func (m *mockWeatherRepo) GetHourlyWeather(ctx context.Context, date string) ([]entities.HourlyWeather, error) {
	args := m.Called(ctx, date)
	return args.Get(0).([]entities.HourlyWeather), args.Error(1)
}

func (m *mockWeatherRepo) LatestHourlyWeather(ctx context.Context) (*entities.HourlyWeather, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.HourlyWeather), args.Error(1)
}

func (m *mockWeatherRepo) GetAllDailyEvents(ctx context.Context) ([]entities.DailyEvents, error) {
	args := m.Called(ctx)
	return args.Get(0).([]entities.DailyEvents), args.Error(1)
}

func (m *mockWeatherRepo) SaveAllDailyEvents(ctx context.Context, events []entities.DailyEvents) (int, error) {
	args := m.Called(ctx, events)
	return args.Int(0), args.Error(1)
}

func (m *mockWeatherRepo) SaveAllHourlyWeather(ctx context.Context, weather []entities.HourlyWeather) (int, error) {
	args := m.Called(ctx, weather)
	// A func result reports the number of rows it was given, as the real repository does.
	if count, ok := args.Get(0).(func(weather []entities.HourlyWeather) int); ok {
		return count(weather), args.Error(1)
	}
	return args.Int(0), args.Error(1)
}

// TestMigrateWeatherData_IDRemapping verifies that legacy DailyEventsIDs are correctly
// remapped to V2 IDs (Legacy ID -> Date -> V2 ID).
func TestMigrateWeatherData_IDRemapping(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	// Legacy data: ID 100 = "2024-01-01", ID 101 = "2024-01-02"
	legacyEvents := []datastore.DailyEvents{
		{ID: 100, Date: "2024-01-01", Sunrise: 1704096000, Sunset: 1704132000, Country: "US", CityName: "NYC"},
		{ID: 101, Date: "2024-01-02", Sunrise: 1704182400, Sunset: 1704218400, Country: "US", CityName: "NYC"},
	}

	// Legacy weather points to legacy IDs (100, 101)
	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	legacyWeather := []datastore.HourlyWeather{
		{ID: 1, DailyEventsID: 100, Time: testTime, Temperature: 5.5},
		{ID: 2, DailyEventsID: 101, Time: testTime.AddDate(0, 0, 1), Temperature: 3.2},
	}

	// V2 after migration: Different IDs for same dates!
	// ID 1 = "2024-01-01", ID 2 = "2024-01-02"
	v2Events := []entities.DailyEvents{
		{ID: 1, Date: "2024-01-01"},
		{ID: 2, Date: "2024-01-02"},
	}

	// Set up mock expectations
	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return(legacyWeather, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(2, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)

	// Verify remapping: Legacy ID 100 -> Date "2024-01-01" -> V2 ID 1
	//                   Legacy ID 101 -> Date "2024-01-02" -> V2 ID 2
	mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.MatchedBy(func(weather []entities.HourlyWeather) bool {
		if len(weather) != 2 {
			return false
		}
		// First weather record should point to V2 ID 1 (was legacy ID 100)
		// Second weather record should point to V2 ID 2 (was legacy ID 101)
		return weather[0].DailyEventsID == 1 && weather[1].DailyEventsID == 2
	})).Return(2, nil)

	// Create migrator
	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	// Execute migration
	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	// Verify all expectations were met
	mockWeather.AssertExpectations(t)
}

// TestMigrateWeatherData_EmptyData verifies that empty legacy data is handled gracefully.
func TestMigrateWeatherData_EmptyData(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	// Empty legacy data
	mockStore.EXPECT().GetAllDailyEvents().Return([]datastore.DailyEvents{}, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return([]datastore.HourlyWeather{}, nil)

	// Create migrator
	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	// Execute migration
	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	// SaveAllDailyEvents and SaveAllHourlyWeather should NOT be called
	// because there's no data to migrate
	mockWeather.AssertNotCalled(t, "SaveAllDailyEvents")
	mockWeather.AssertNotCalled(t, "SaveAllHourlyWeather")
}

// storedWeatherTime builds a time the way the SQLite driver returns a stored one:
// the wall clock as written, in a fixed zone carrying the stored offset.
func storedWeatherTime(year int, month time.Month, day, hour, minute, sec, offsetSeconds int) time.Time {
	return time.Date(year, month, day, hour, minute, sec, 0, time.FixedZone("", offsetSeconds))
}

const (
	offsetEEST = 3 * 60 * 60
	offsetEET  = 2 * 60 * 60
)

// savedWeatherTemps runs migrateWeatherData with the given legacy data and a v2 repo
// that holds v2Events, and returns the saved rows keyed by their Temperature marker.
func savedWeatherTemps(t *testing.T, legacyEvents []datastore.DailyEvents, legacyWeather []datastore.HourlyWeather, v2Events []entities.DailyEvents) (saved map[float64]uint, result *AuxiliaryMigrationResult) {
	t.Helper()
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)

	var rows []entities.HourlyWeather
	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return(legacyWeather, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(len(v2Events), nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)
	mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			rows = args.Get(1).([]entities.HourlyWeather)
		}).
		Return(func(weather []entities.HourlyWeather) int { return len(weather) }, nil)

	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      testLogger(),
	})
	result = &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(t.Context(), result)

	saved = make(map[float64]uint, len(rows))
	for i := range rows {
		saved[rows[i].Temperature] = rows[i].DailyEventsID
	}
	return saved, result
}

// TestMigrateWeatherData_ParentlessRowsAttachByStoredDate verifies that hourly rows
// whose legacy parent does not exist are attached to the daily event of the date of
// their stored time, read in the offset they were stored with.
func TestMigrateWeatherData_ParentlessRowsAttachByStoredDate(t *testing.T) {
	legacyEvents := []datastore.DailyEvents{
		{ID: 100, Date: "2024-06-13"},
		{ID: 101, Date: "2024-10-27"},
		{ID: 102, Date: "2025-01-05"},
		{ID: 103, Date: "2025-01-06"},
	}
	v2Events := []entities.DailyEvents{
		{ID: 11, Date: "2024-06-13"},
		{ID: 12, Date: "2024-10-27"},
		{ID: 13, Date: "2025-01-05"},
		{ID: 14, Date: "2025-01-06"},
	}
	const (
		tempParented   = 1.0
		tempZeroParent = 2.0
		tempDangling   = 3.0
		tempDSTFirst   = 4.0
		tempDSTSecond  = 5.0
		tempUTCRow     = 6.0
		tempNoEvent    = 7.0
	)
	legacyWeather := []datastore.HourlyWeather{
		{ID: 1, DailyEventsID: 100, Time: storedWeatherTime(2024, 6, 13, 12, 0, 0, offsetEEST), Temperature: tempParented},
		// Its UTC date is 2024-06-12, so a conversion to UTC files it under the wrong day.
		{ID: 2, DailyEventsID: 0, Time: storedWeatherTime(2024, 6, 13, 0, 1, 47, offsetEEST), Temperature: tempZeroParent},
		{ID: 3, DailyEventsID: 999, Time: storedWeatherTime(2024, 6, 13, 13, 0, 0, offsetEEST), Temperature: tempDangling},
		// The repeated hour at the end of DST: two distinct instants with the same wall clock.
		{ID: 4, DailyEventsID: 0, Time: storedWeatherTime(2024, 10, 27, 3, 8, 42, offsetEEST), Temperature: tempDSTFirst},
		{ID: 5, DailyEventsID: 0, Time: storedWeatherTime(2024, 10, 27, 3, 8, 42, offsetEET), Temperature: tempDSTSecond},
		// 22:00 UTC is already the next day in zones east of UTC.
		{ID: 6, DailyEventsID: 0, Time: storedWeatherTime(2025, 1, 5, 22, 0, 0, 0), Temperature: tempUTCRow},
		{ID: 7, DailyEventsID: 0, Time: storedWeatherTime(2024, 7, 1, 12, 0, 0, offsetEEST), Temperature: tempNoEvent},
	}

	saved, result := savedWeatherTemps(t, legacyEvents, legacyWeather, v2Events)

	assert.Equal(t, map[float64]uint{
		tempParented:   11,
		tempZeroParent: 11,
		tempDangling:   11,
		tempDSTFirst:   12,
		tempDSTSecond:  12,
		tempUTCRow:     13,
	}, saved)
	assert.False(t, result.HasErrors())
	assert.Equal(t, 1, result.Weather.Skipped)
	assert.Zero(t, result.Weather.HourlyWeatherFailed)
	assert.Equal(t, result.Weather.HourlyWeatherTotal,
		result.Weather.HourlyWeatherMigrated+result.Weather.Skipped+result.Weather.HourlyWeatherFailed)
}

// TestMigrateWeatherData_DateNotInV2 verifies that weather records, with or without
// a legacy parent, are counted as failed when their legacy date is not found in V2
// after migration.
func TestMigrateWeatherData_DateNotInV2(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	// Legacy has two dates
	legacyEvents := []datastore.DailyEvents{
		{ID: 100, Date: "2024-01-01"},
		{ID: 101, Date: "2024-01-02"},
	}

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	legacyWeather := []datastore.HourlyWeather{
		{ID: 1, DailyEventsID: 100, Time: testTime, Temperature: 5.5},
		{ID: 2, DailyEventsID: 101, Time: testTime.AddDate(0, 0, 1), Temperature: 3.2},
		// No legacy parent, but its own date is a legacy date that is missing in v2.
		{ID: 3, DailyEventsID: 0, Time: testTime.AddDate(0, 0, 1).Add(time.Hour), Temperature: 4.1},
	}

	// V2 only has one date (2024-01-01) - 2024-01-02 is missing
	v2Events := []entities.DailyEvents{
		{ID: 1, Date: "2024-01-01"},
		// Note: 2024-01-02 is missing from V2
	}

	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return(legacyWeather, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(1, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)

	// Only weather for 2024-01-01 should be migrated
	mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.MatchedBy(func(weather []entities.HourlyWeather) bool {
		if len(weather) != 1 {
			return false
		}
		return weather[0].DailyEventsID == 1 && weather[0].Temperature == 5.5
	})).Return(1, nil)

	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	// A legacy date missing in v2 means the daily event was lost, a failure.
	assert.True(t, result.HasErrors())
	require.ErrorIs(t, result.Weather.Error, ErrAuxDailyEventNotInV2)
	assert.Equal(t, 2, result.Weather.HourlyWeatherFailed)
	assert.Zero(t, result.Weather.Skipped)

	mockWeather.AssertExpectations(t)
}

// TestMigrateWeatherData_DuplicateInstantsCopiedOnce verifies that rows sharing an
// instant are copied once, the lowest legacy id wins whatever the read order, and a
// row that cannot be copied never suppresses one that can.
func TestMigrateWeatherData_DuplicateInstantsCopiedOnce(t *testing.T) {
	legacyEvents := []datastore.DailyEvents{{ID: 100, Date: "2024-06-13"}}
	v2Events := []entities.DailyEvents{{ID: 11, Date: "2024-06-13"}}
	const (
		tempSameInstantHigh = 11.0
		tempSameInstantLow  = 10.0
		tempParentedLow     = 2.0
		tempParentlessHigh  = 3.0
		tempNoEventLow      = 5.0
		tempCopyableHigh    = 6.0
		tempZeroTimeA       = 7.0
		tempZeroTimeB       = 8.0
	)
	instant := storedWeatherTime(2024, 6, 13, 0, 1, 47, offsetEEST)
	otherInstant := storedWeatherTime(2024, 6, 13, 6, 0, 0, offsetEEST)
	legacyWeather := []datastore.HourlyWeather{
		{ID: 11, DailyEventsID: 0, Time: otherInstant, Temperature: tempSameInstantHigh},
		{ID: 10, DailyEventsID: 0, Time: otherInstant, Temperature: tempSameInstantLow},
		{ID: 2, DailyEventsID: 100, Time: instant, Temperature: tempParentedLow},
		{ID: 3, DailyEventsID: 0, Time: instant, Temperature: tempParentlessHigh},
		// Same instant, the lower id has no daily event for its own date (2024-06-12).
		{ID: 5, DailyEventsID: 0, Time: storedWeatherTime(2024, 6, 12, 21, 30, 0, 0), Temperature: tempNoEventLow},
		{ID: 6, DailyEventsID: 0, Time: storedWeatherTime(2024, 6, 13, 0, 30, 0, offsetEEST), Temperature: tempCopyableHigh},
		{ID: 20, DailyEventsID: 100, Time: time.Time{}, Temperature: tempZeroTimeA},
		{ID: 21, DailyEventsID: 100, Time: time.Time{}, Temperature: tempZeroTimeB},
	}
	want := map[float64]uint{
		tempSameInstantLow: 11,
		tempParentedLow:    11,
		tempCopyableHigh:   11,
		tempZeroTimeA:      11,
		tempZeroTimeB:      11,
	}
	const wantDuplicatesAndUnresolvable = 3 // ids 11 and 3 are duplicates, id 5 has no daily event

	reversed := make([]datastore.HourlyWeather, len(legacyWeather))
	for i := range legacyWeather {
		reversed[len(legacyWeather)-1-i] = legacyWeather[i]
	}

	for name, input := range map[string][]datastore.HourlyWeather{"input order": legacyWeather, "reversed": reversed} {
		t.Run(name, func(t *testing.T) {
			saved, result := savedWeatherTemps(t, legacyEvents, input, v2Events)

			assert.Equal(t, want, saved)
			assert.False(t, result.HasErrors())
			assert.Equal(t, wantDuplicatesAndUnresolvable, result.Weather.Skipped)
			assert.Zero(t, result.Weather.HourlyWeatherFailed)
			assert.Equal(t, result.Weather.HourlyWeatherTotal,
				result.Weather.HourlyWeatherMigrated+result.Weather.Skipped+result.Weather.HourlyWeatherFailed)
		})
	}
}

// TestMigrateWeatherData_NilRepo verifies that migration is skipped when weather repo is nil.
func TestMigrateWeatherData_NilRepo(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	log := testLogger()

	// Create migrator without weather repo
	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: nil, // No weather repo
		Logger:      log,
	})

	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	// No methods should be called on mockStore for weather operations
}

// TestMigrateWeatherData_PreservesFields verifies that all weather data fields
// are preserved during migration.
func TestMigrateWeatherData_PreservesFields(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	legacyEvents := []datastore.DailyEvents{
		{ID: 1, Date: "2024-01-01", Sunrise: 1704096000, Sunset: 1704132000, Country: "US", CityName: "New York"},
	}

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	legacyWeather := []datastore.HourlyWeather{
		{
			ID:            1,
			DailyEventsID: 1,
			Time:          testTime,
			Temperature:   5.5,
			FeelsLike:     3.2,
			TempMin:       2.0,
			TempMax:       8.0,
			Pressure:      1013,
			Humidity:      75,
			Visibility:    10000,
			WindSpeed:     5.5,
			WindDeg:       180,
			WindGust:      8.0,
			Clouds:        50,
			WeatherMain:   "Clouds",
			WeatherDesc:   "Scattered clouds",
			WeatherIcon:   "03d",
		},
	}

	v2Events := []entities.DailyEvents{
		{ID: 1, Date: "2024-01-01"},
	}

	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return(legacyWeather, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.MatchedBy(func(events []entities.DailyEvents) bool {
		if len(events) != 1 {
			return false
		}
		e := events[0]
		return e.Date == "2024-01-01" &&
			e.Sunrise == 1704096000 &&
			e.Sunset == 1704132000 &&
			e.Country == "US" &&
			e.CityName == "New York"
	})).Return(1, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)

	// Verify all weather fields are preserved
	mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.MatchedBy(func(weather []entities.HourlyWeather) bool {
		if len(weather) != 1 {
			return false
		}
		w := weather[0]
		return w.DailyEventsID == 1 &&
			w.Time.Equal(testTime) &&
			w.Temperature == 5.5 &&
			w.FeelsLike == 3.2 &&
			w.TempMin == 2.0 &&
			w.TempMax == 8.0 &&
			w.Pressure == 1013 &&
			w.Humidity == 75 &&
			w.Visibility == 10000 &&
			w.WindSpeed == 5.5 &&
			w.WindDeg == 180 &&
			w.WindGust == 8.0 &&
			w.Clouds == 50 &&
			w.WeatherMain == "Clouds" &&
			w.WeatherDesc == "Scattered clouds" &&
			w.WeatherIcon == "03d"
	})).Return(1, nil)

	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	mockWeather.AssertExpectations(t)
}

// TestMigrateWeatherData_NoHourlyWeather verifies that having daily events
// but no hourly weather is handled correctly.
func TestMigrateWeatherData_NoHourlyWeather(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	// Has daily events but no hourly weather
	legacyEvents := []datastore.DailyEvents{
		{ID: 100, Date: "2024-01-01", Sunrise: 1704096000, Sunset: 1704132000},
	}

	v2Events := []entities.DailyEvents{
		{ID: 1, Date: "2024-01-01"},
	}

	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return([]datastore.HourlyWeather{}, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(1, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)

	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	// SaveAllHourlyWeather should NOT be called when there's no hourly weather
	mockWeather.AssertNotCalled(t, "SaveAllHourlyWeather")
}

// TestMigrateWeatherData_ComplexIDMapping verifies ID mapping with multiple dates
// and weather records with non-sequential IDs.
func TestMigrateWeatherData_ComplexIDMapping(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	// Legacy: non-sequential IDs
	legacyEvents := []datastore.DailyEvents{
		{ID: 50, Date: "2024-01-01"},
		{ID: 75, Date: "2024-01-02"},
		{ID: 200, Date: "2024-01-03"},
	}

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	// Multiple weather records per day, non-sequential IDs
	legacyWeather := []datastore.HourlyWeather{
		{ID: 10, DailyEventsID: 50, Time: testTime, Temperature: 1.0},
		{ID: 20, DailyEventsID: 50, Time: testTime.Add(time.Hour), Temperature: 2.0},
		{ID: 30, DailyEventsID: 75, Time: testTime.AddDate(0, 0, 1), Temperature: 3.0},
		{ID: 40, DailyEventsID: 200, Time: testTime.AddDate(0, 0, 2), Temperature: 4.0},
		{ID: 50, DailyEventsID: 200, Time: testTime.AddDate(0, 0, 2).Add(time.Hour), Temperature: 5.0},
	}

	// V2: sequential IDs starting from 1
	v2Events := []entities.DailyEvents{
		{ID: 1, Date: "2024-01-01"},
		{ID: 2, Date: "2024-01-02"},
		{ID: 3, Date: "2024-01-03"},
	}

	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return(legacyWeather, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(3, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)

	// Verify complex ID remapping:
	// Legacy ID 50 (2024-01-01) -> V2 ID 1
	// Legacy ID 75 (2024-01-02) -> V2 ID 2
	// Legacy ID 200 (2024-01-03) -> V2 ID 3
	mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.MatchedBy(func(weather []entities.HourlyWeather) bool {
		if len(weather) != 5 {
			return false
		}
		// Records 0,1 should map to V2 ID 1 (from legacy ID 50)
		// Record 2 should map to V2 ID 2 (from legacy ID 75)
		// Records 3,4 should map to V2 ID 3 (from legacy ID 200)
		return weather[0].DailyEventsID == 1 &&
			weather[1].DailyEventsID == 1 &&
			weather[2].DailyEventsID == 2 &&
			weather[3].DailyEventsID == 3 &&
			weather[4].DailyEventsID == 3
	})).Return(5, nil)

	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	ctx := t.Context()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	mockWeather.AssertExpectations(t)
}

// TestMigrateWeatherData_ErrorHandling tests error handling scenarios.
func TestMigrateWeatherData_ErrorHandling(t *testing.T) {
	t.Run("continues on GetAllDailyEvents error", func(t *testing.T) {
		mockStore := mocks.NewMockInterface(t)
		mockWeather := new(mockWeatherRepo)
		log := testLogger()

		// Return error from GetAllDailyEvents
		mockStore.EXPECT().GetAllDailyEvents().Return(nil, assert.AnError)

		migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
			LegacyStore: mockStore,
			WeatherRepo: mockWeather,
			Logger:      log,
		})

		ctx := t.Context()
		result := &AuxiliaryMigrationResult{}
		migrator.migrateWeatherData(ctx, result)
		// Error should be captured in result (non-fatal)
		require.Error(t, result.Weather.Error)
	})

	t.Run("continues on GetAllHourlyWeather error", func(t *testing.T) {
		mockStore := mocks.NewMockInterface(t)
		mockWeather := new(mockWeatherRepo)
		log := testLogger()

		legacyEvents := []datastore.DailyEvents{
			{ID: 1, Date: "2024-01-01"},
		}

		mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
		mockStore.EXPECT().GetAllHourlyWeather().Return(nil, assert.AnError)

		migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
			LegacyStore: mockStore,
			WeatherRepo: mockWeather,
			Logger:      log,
		})

		ctx := t.Context()
		result := &AuxiliaryMigrationResult{}
		migrator.migrateWeatherData(ctx, result)
		// Error should be captured in result (non-fatal)
		require.Error(t, result.Weather.Error)
	})
	t.Run("records error when v2 daily events read fails", func(t *testing.T) {
		mockStore := mocks.NewMockInterface(t)
		mockWeather := new(mockWeatherRepo)

		testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		mockStore.EXPECT().GetAllDailyEvents().Return([]datastore.DailyEvents{{ID: 1, Date: "2024-01-01"}}, nil)
		mockStore.EXPECT().GetAllHourlyWeather().Return([]datastore.HourlyWeather{
			{ID: 1, DailyEventsID: 1, Time: testTime},
			{ID: 2, DailyEventsID: 1, Time: testTime.Add(time.Hour)},
		}, nil)
		mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(1, nil)
		mockWeather.On("GetAllDailyEvents", mock.Anything).Return([]entities.DailyEvents(nil), assert.AnError)

		migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
			LegacyStore: mockStore,
			WeatherRepo: mockWeather,
			Logger:      testLogger(),
		})

		result := &AuxiliaryMigrationResult{}
		migrator.migrateWeatherData(t.Context(), result)

		require.ErrorIs(t, result.Weather.Error, assert.AnError)
		assert.True(t, result.HasErrors())
		assert.Equal(t, result.Weather.HourlyWeatherTotal, result.Weather.HourlyWeatherFailed)
		mockWeather.AssertNotCalled(t, "SaveAllHourlyWeather")
	})
	t.Run("counts rows not saved when daily event and hourly saves fail", func(t *testing.T) {
		mockStore := mocks.NewMockInterface(t)
		mockWeather := new(mockWeatherRepo)

		testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		mockStore.EXPECT().GetAllDailyEvents().Return([]datastore.DailyEvents{
			{ID: 1, Date: "2024-01-01"}, {ID: 2, Date: "2024-01-02"},
		}, nil)
		mockStore.EXPECT().GetAllHourlyWeather().Return([]datastore.HourlyWeather{
			{ID: 1, DailyEventsID: 1, Time: testTime},
			{ID: 2, DailyEventsID: 2, Time: testTime.AddDate(0, 0, 1)},
		}, nil)
		// One of two daily events saved; only 2024-01-01 exists in v2.
		mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(1, assert.AnError)
		mockWeather.On("GetAllDailyEvents", mock.Anything).Return([]entities.DailyEvents{{ID: 7, Date: "2024-01-01"}}, nil)
		// The one mappable hourly row is not saved either.
		mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.Anything).Return(0, assert.AnError)

		migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
			LegacyStore: mockStore,
			WeatherRepo: mockWeather,
			Logger:      testLogger(),
		})

		result := &AuxiliaryMigrationResult{}
		migrator.migrateWeatherData(t.Context(), result)

		require.ErrorIs(t, result.Weather.Error, assert.AnError)
		assert.Equal(t, 1, result.Weather.DailyEventsFailed)
		// One row has no daily event in v2, one failed to save.
		assert.Equal(t, 2, result.Weather.HourlyWeatherFailed)
		assert.Zero(t, result.Weather.Skipped)
	})

	t.Run("hourly rows without any daily event are skipped, not failed", func(t *testing.T) {
		mockStore := mocks.NewMockInterface(t)
		mockWeather := new(mockWeatherRepo)

		testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		mockStore.EXPECT().GetAllDailyEvents().Return(nil, nil)
		mockStore.EXPECT().GetAllHourlyWeather().Return([]datastore.HourlyWeather{
			{ID: 1, DailyEventsID: 9, Time: testTime},
			{ID: 2, DailyEventsID: 9, Time: testTime.Add(time.Hour)},
		}, nil)

		migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
			LegacyStore: mockStore,
			WeatherRepo: mockWeather,
			Logger:      testLogger(),
		})

		result := &AuxiliaryMigrationResult{}
		migrator.migrateWeatherData(t.Context(), result)

		assert.False(t, result.HasErrors())
		assert.Equal(t, 2, result.Weather.Skipped)
		assert.Zero(t, result.Weather.HourlyWeatherFailed)
	})
}

// When the v2 daily events cannot be read, hourly rows are classified by their parent
// date: a date with a legacy daily event is a failure, a date without one is a skip.
func TestMigrateWeatherData_V2ReadFailureClassifiesByParentDate(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	mockStore.EXPECT().GetAllDailyEvents().Return([]datastore.DailyEvents{{ID: 1, Date: "2024-01-01"}}, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return([]datastore.HourlyWeather{
		{ID: 1, DailyEventsID: 1, Time: testTime},
		{ID: 2, DailyEventsID: 1, Time: testTime.Add(time.Hour)},
		{ID: 3, DailyEventsID: 0, Time: testTime.Add(2 * time.Hour)},                 // Parentless, legacy date
		{ID: 4, DailyEventsID: 999, Time: testTime.AddDate(0, 0, 10).Add(time.Hour)}, // Dangling, no legacy event on its date
	}, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(1, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return([]entities.DailyEvents(nil), assert.AnError)

	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      testLogger(),
	})

	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(t.Context(), result)

	require.ErrorIs(t, result.Weather.Error, assert.AnError)
	assert.Equal(t, 3, result.Weather.HourlyWeatherFailed)
	assert.Equal(t, 1, result.Weather.Skipped)
	mockWeather.AssertNotCalled(t, "SaveAllHourlyWeather")
}
