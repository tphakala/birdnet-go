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

func (m *mockWeatherRepo) GetHourlyWeatherInLocation(ctx context.Context, date string, loc *time.Location) ([]entities.HourlyWeather, error) {
	args := m.Called(ctx, date, loc)
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
		{ID: 2, DailyEventsID: 101, Time: testTime.Add(24 * time.Hour), Temperature: 3.2},
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
	ctx := context.Background()
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
	ctx := context.Background()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	// SaveAllDailyEvents and SaveAllHourlyWeather should NOT be called
	// because there's no data to migrate
	mockWeather.AssertNotCalled(t, "SaveAllDailyEvents")
	mockWeather.AssertNotCalled(t, "SaveAllHourlyWeather")
}

// TestMigrateWeatherData_OrphanRecords verifies that orphan hourly weather records
// (those with invalid DailyEventsID) are skipped during migration.
func TestMigrateWeatherData_OrphanRecords(t *testing.T) {
	mockStore := mocks.NewMockInterface(t)
	mockWeather := new(mockWeatherRepo)
	log := testLogger()

	// Legacy daily events - only has ID 100
	legacyEvents := []datastore.DailyEvents{
		{ID: 100, Date: "2024-01-01", Sunrise: 1704096000, Sunset: 1704132000},
	}

	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Legacy weather includes:
	// - ID 1 with valid DailyEventsID 100
	// - ID 2 with ORPHAN DailyEventsID 999 (doesn't exist in legacy events)
	// - ID 3 with ORPHAN DailyEventsID 888 (doesn't exist in legacy events)
	legacyWeather := []datastore.HourlyWeather{
		{ID: 1, DailyEventsID: 100, Time: testTime, Temperature: 5.5},
		{ID: 2, DailyEventsID: 999, Time: testTime, Temperature: 10.0}, // Orphan
		{ID: 3, DailyEventsID: 888, Time: testTime, Temperature: 15.0}, // Orphan
	}

	// V2 events after migration
	v2Events := []entities.DailyEvents{
		{ID: 1, Date: "2024-01-01"},
	}

	mockStore.EXPECT().GetAllDailyEvents().Return(legacyEvents, nil)
	mockStore.EXPECT().GetAllHourlyWeather().Return(legacyWeather, nil)
	mockWeather.On("SaveAllDailyEvents", mock.Anything, mock.Anything).Return(1, nil)
	mockWeather.On("GetAllDailyEvents", mock.Anything).Return(v2Events, nil)

	// Only 1 weather record should be saved (the non-orphan one)
	mockWeather.On("SaveAllHourlyWeather", mock.Anything, mock.MatchedBy(func(weather []entities.HourlyWeather) bool {
		if len(weather) != 1 {
			return false
		}
		// Only the valid weather record should be included
		return weather[0].DailyEventsID == 1 && weather[0].Temperature == 5.5
	})).Return(1, nil)

	// Create migrator
	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mockStore,
		WeatherRepo: mockWeather,
		Logger:      log,
	})

	// Execute migration
	ctx := context.Background()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	mockWeather.AssertExpectations(t)
}

// TestMigrateWeatherData_DateNotInV2 verifies that weather records are skipped
// when their date is not found in V2 after migration.
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
		{ID: 2, DailyEventsID: 101, Time: testTime.Add(24 * time.Hour), Temperature: 3.2},
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

	ctx := context.Background()
	result := &AuxiliaryMigrationResult{}
	migrator.migrateWeatherData(ctx, result)

	mockWeather.AssertExpectations(t)
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

	ctx := context.Background()
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

	ctx := context.Background()
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

	ctx := context.Background()
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
		{ID: 30, DailyEventsID: 75, Time: testTime.Add(24 * time.Hour), Temperature: 3.0},
		{ID: 40, DailyEventsID: 200, Time: testTime.Add(48 * time.Hour), Temperature: 4.0},
		{ID: 50, DailyEventsID: 200, Time: testTime.Add(49 * time.Hour), Temperature: 5.0},
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

	ctx := context.Background()
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

		ctx := context.Background()
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

		ctx := context.Background()
		result := &AuxiliaryMigrationResult{}
		migrator.migrateWeatherData(ctx, result)
		// Error should be captured in result (non-fatal)
		require.Error(t, result.Weather.Error)
	})
}
