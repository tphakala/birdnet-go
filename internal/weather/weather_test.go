package weather

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// mockProvider is a test double for the Provider interface.
type mockProvider struct {
	fetchFunc func(settings *conf.Settings) (*WeatherData, error)
}

func (m *mockProvider) FetchWeather(settings *conf.Settings) (*WeatherData, error) {
	return m.fetchFunc(settings)
}

func TestNewService(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		wantDisabled bool // true when provider returns ErrWeatherDisabled
	}{
		{"yrno_provider", "yrno", false},
		{"openweather_provider", "openweather", false},
		{"wunderground_provider", "wunderground", false},
		{"invalid_provider_disabled", "invalid", true},
		{"empty_provider_defaults_to_yrno", "", false},
		{"none_provider_disabled", "none", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := createTestSettings(t, tt.provider)

			service, err := NewService(settings, nil, nil)

			switch {
			case tt.wantDisabled:
				require.ErrorIs(t, err, ErrWeatherDisabled,
					"empty/unrecognized provider should return ErrWeatherDisabled")
				assert.Nil(t, service)
			default:
				require.NoError(t, err)
				require.NotNil(t, service)
			}
		})
	}
}

func TestValidateWeatherData(t *testing.T) {
	tests := []struct {
		name    string
		data    *datastore.HourlyWeather
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid_data",
			data: &datastore.HourlyWeather{
				Temperature: 20.0,
				WindSpeed:   5.0,
			},
			wantErr: false,
		},
		{
			name: "zero_temperature",
			data: &datastore.HourlyWeather{
				Temperature: 0.0,
				WindSpeed:   5.0,
			},
			wantErr: false,
		},
		{
			name: "negative_temperature_valid",
			data: &datastore.HourlyWeather{
				Temperature: -20.0,
				WindSpeed:   5.0,
			},
			wantErr: false,
		},
		{
			name: "temperature_below_absolute_zero",
			data: &datastore.HourlyWeather{
				Temperature: -300.0, // Below -273.15°C
				WindSpeed:   5.0,
			},
			wantErr: true,
			errMsg:  "absolute zero",
		},
		{
			name: "negative_wind_speed",
			data: &datastore.HourlyWeather{
				Temperature: 20.0,
				WindSpeed:   -5.0,
			},
			wantErr: true,
			errMsg:  "negative",
		},
		{
			name: "zero_wind_speed_valid",
			data: &datastore.HourlyWeather{
				Temperature: 20.0,
				WindSpeed:   0.0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWeatherData(tt.data)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAbsoluteZeroCelsiusConstant(t *testing.T) {
	// Verify the constant is set correctly
	assert.InDelta(t, -273.15, absoluteZeroCelsius, 0.01)
}

// TestTimezoneConversionForWeatherStorage tests the behavior of timezone conversion
// when storing weather data. This demonstrates the expected behavior and can reveal
// timezone-related bugs.
func TestTimezoneConversionForWeatherStorage(t *testing.T) {
	// This test documents the timezone handling behavior in SaveWeatherData.
	// The UTC time from weather providers is converted to local time for storage.
	// This is critical for correct date-based queries.

	t.Run("utc_to_local_conversion", func(t *testing.T) {
		// Simulate weather data received at midnight UTC on Jan 13, 2026
		utcTime := time.Date(2026, 1, 13, 0, 0, 0, 0, time.UTC)

		// In Finland (UTC+2), this should be 02:00 on Jan 13
		// The date should still be Jan 13
		localTime := utcTime.In(time.Local)

		// This test will behave differently based on the system timezone
		// The key assertion is that the date conversion happens correctly
		assert.False(t, localTime.IsZero(), "Local time should not be zero")
		assert.Equal(t, utcTime.Unix(), localTime.Unix(), "Unix timestamps should be equal")
	})

	t.Run("late_night_utc_conversion", func(t *testing.T) {
		// Weather data at 22:00 UTC on Jan 12, 2026
		utcTime := time.Date(2026, 1, 12, 22, 0, 0, 0, time.UTC)

		// In Finland (UTC+2), this would be 00:00 on Jan 13
		// This is where the timezone bug can cause issues
		localTime := utcTime.In(time.Local)

		// Document the expected behavior
		t.Logf("UTC time: %v", utcTime.Format(time.RFC3339))
		t.Logf("Local time: %v", localTime.Format(time.RFC3339))
		t.Logf("UTC date: %s", utcTime.Format(time.DateOnly))
		t.Logf("Local date: %s", localTime.Format(time.DateOnly))
	})
}

// TestWeatherDataDateFormatting tests the date formatting used for storage.
// This is critical for the daily events feature to work correctly.
func TestWeatherDataDateFormatting(t *testing.T) {
	t.Run("date_format_consistency", func(t *testing.T) {
		testTime := time.Date(2026, 1, 13, 14, 30, 0, 0, time.UTC)
		localTime := testTime.In(time.Local)

		expectedFormat := "2006-01-02"
		formattedDate := localTime.Format(expectedFormat)

		assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, formattedDate, "Date should be in YYYY-MM-DD format")
	})
}

// TIMEZONE BUG DEMONSTRATION TESTS
// These tests document the timezone bug that causes weather data for hours 00-01
// in UTC+2 (Finland) to be missing from daily weather queries.
//
// THE BUG: SQLite's date() function converts timezone-aware timestamps to UTC
// before extracting the date, causing local midnight (00:00+02:00) to be
// associated with the previous UTC date (2026-01-12 instead of 2026-01-13).
//
// EXPECTED BEHAVIOR: Querying weather for "2026-01-13" should return all weather
// data where the LOCAL date is 2026-01-13.
//
// ACTUAL BEHAVIOR: Weather data stored at 00:00+02:00 (midnight local) is not
// returned because SQLite date() extracts 2026-01-12 from the UTC conversion.

func TestTimezoneAwareWeatherQuery_BugDemonstration(t *testing.T) {
	// This test demonstrates what the expected behavior should be.
	// It can be used to verify a fix for the timezone bug.

	t.Run("midnight_local_time_date_extraction", func(t *testing.T) {
		// Create a time representing midnight local time on Jan 13, 2026
		// in Finland (UTC+2)
		loc, err := time.LoadLocation("Europe/Helsinki")
		if err != nil {
			t.Skip("Could not load Europe/Helsinki timezone")
		}

		// Midnight on Jan 13 in Helsinki = 22:00 on Jan 12 in UTC
		localMidnight := time.Date(2026, 1, 13, 0, 0, 0, 0, loc)

		// The expected date for this weather data should be 2026-01-13
		expectedDate := "2026-01-13"
		actualLocalDate := localMidnight.Format(time.DateOnly)

		assert.Equal(t, expectedDate, actualLocalDate,
			"Local midnight should be associated with local date, not UTC date")

		// Document the UTC equivalent
		utcTime := localMidnight.UTC()
		utcDate := utcTime.Format(time.DateOnly)

		t.Logf("Local time: %v (date: %s)", localMidnight.Format(time.RFC3339), actualLocalDate)
		t.Logf("UTC time: %v (date: %s)", utcTime.Format(time.RFC3339), utcDate)
		t.Logf("BUG: SQLite date() would use UTC date %s instead of local date %s", utcDate, actualLocalDate)

		// This assertion shows the discrepancy that causes the bug
		if utcDate != actualLocalDate {
			t.Log("CONFIRMED: UTC date differs from local date - this causes the timezone bug")
		}
	})

	t.Run("early_morning_hours_affected", func(t *testing.T) {
		loc, err := time.LoadLocation("Europe/Helsinki")
		if err != nil {
			t.Skip("Could not load Europe/Helsinki timezone")
		}

		// Test hours 00:00 and 01:00 local time - these are affected by the bug
		affectedHours := []int{0, 1}

		for _, hour := range affectedHours {
			localTime := time.Date(2026, 1, 13, hour, 30, 0, 0, loc)
			utcTime := localTime.UTC()

			localDate := localTime.Format(time.DateOnly)
			utcDate := utcTime.Format(time.DateOnly)

			t.Logf("Hour %02d:30 local -> UTC date: %s, Local date: %s",
				hour, utcDate, localDate)

			// For hours 00-01 in UTC+2, the UTC date will be the previous day
			if utcDate != localDate {
				t.Logf("  BUG AFFECTS THIS HOUR: Weather at %02d:30 local time "+
					"will be queried by UTC date %s instead of local date %s",
					hour, utcDate, localDate)
			}
		}
	})

	t.Run("hours_not_affected_by_bug", func(t *testing.T) {
		loc, err := time.LoadLocation("Europe/Helsinki")
		if err != nil {
			t.Skip("Could not load Europe/Helsinki timezone")
		}

		// Hours from 02:00 onwards should not be affected in UTC+2
		unaffectedHours := []int{2, 3, 12, 18, 23}

		for _, hour := range unaffectedHours {
			localTime := time.Date(2026, 1, 13, hour, 30, 0, 0, loc)
			utcTime := localTime.UTC()

			localDate := localTime.Format(time.DateOnly)
			utcDate := utcTime.Format(time.DateOnly)

			if hour >= 2 { // In UTC+2, hours 02:00+ should have same date
				assert.Equal(t, localDate, utcDate,
					"Hour %02d:30 should have same date in UTC and local", hour)
			}
		}
	})
}

// TestSQLiteDateFunctionBehavior demonstrates the SQLite date() function behavior
// that causes the timezone bug.
func TestSQLiteDateFunctionBehavior(t *testing.T) {
	// This test documents what SQLite's date() function does with timezone info.
	// SQLite: date('2026-01-13 00:00:00+02:00') returns '2026-01-12'
	// because it converts to UTC before extracting the date.

	t.Run("documented_sqlite_behavior", func(t *testing.T) {
		// Input: timestamp with +02:00 timezone
		input := "2026-01-13 00:00:00+02:00"

		// What SQLite date() returns (converts to UTC first)
		expectedSQLiteResult := "2026-01-12" // UTC date, not local date

		// What we actually want for local queries
		expectedLocalResult := "2026-01-13"

		t.Logf("Input timestamp: %s", input)
		t.Logf("SQLite date() returns: %s (UTC date)", expectedSQLiteResult)
		t.Logf("Expected for local queries: %s (local date)", expectedLocalResult)
		t.Log("This mismatch causes weather data to be missing for hours 00-01 in positive UTC offsets")
	})
}

// TestWeatherDataCreation tests the creation of WeatherData structures.
func TestWeatherDataCreation(t *testing.T) {
	t.Run("using_test_helper", func(t *testing.T) {
		data := createTestWeatherData(t)

		require.NotNil(t, data)
		assert.False(t, data.Time.IsZero())
		assert.NotEmpty(t, data.Location.Country)
		assert.NotEmpty(t, data.Location.City)
	})

	t.Run("with_custom_options", func(t *testing.T) {
		customTemp := 25.5
		data := createTestWeatherData(t, func(d *WeatherData) {
			d.Temperature.Current = customTemp
		})

		assert.InDelta(t, customTemp, data.Temperature.Current, 0.01)
	})
}

// TestSettingsCreation tests the creation of test settings.
func TestSettingsCreation(t *testing.T) {
	providers := []string{"yrno", "openweather", "wunderground"}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			settings := createTestSettings(t, provider)

			require.NotNil(t, settings)
			assert.Equal(t, provider, settings.Realtime.Weather.Provider)
			assert.NotZero(t, settings.BirdNET.Latitude)
			assert.NotZero(t, settings.BirdNET.Longitude)
		})
	}

	t.Run("with_custom_options", func(t *testing.T) {
		customLat := 40.7128
		settings := createTestSettings(t, "yrno", func(s *conf.Settings) {
			s.BirdNET.Latitude = customLat
		})

		assert.InDelta(t, customLat, settings.BirdNET.Latitude, 0.001)
	})
}

// =============================================================================
// DATABASE INTEGRATION TESTS WITH MOCK DATASTORE
// =============================================================================

// TestService_SaveWeatherData tests the SaveWeatherData method with mock datastore.
func TestService_SaveWeatherData(t *testing.T) {
	t.Run("success_saves_daily_and_hourly", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		// Create service with mock DB
		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		// Create test data with fixed time for deterministic testing
		fixedTime := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
		testData := createTestWeatherData(t, func(d *WeatherData) {
			d.Time = fixedTime
		})

		// Expect SaveDailyEvents - use Run to simulate DB assigning an ID
		mockDB.On("SaveDailyEvents", mock.MatchedBy(func(de *datastore.DailyEvents) bool {
			return de.CityName == testData.Location.City
		})).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 123 // Simulate DB auto-increment ID
		}).Return(nil).Once()

		// Expect SaveHourlyWeather - verify it uses the ID from SaveDailyEvents
		mockDB.On("SaveHourlyWeather", mock.MatchedBy(func(hw *datastore.HourlyWeather) bool {
			return hw.DailyEventsID == 123 &&
				hw.Temperature == testData.Temperature.Current &&
				hw.WindSpeed == testData.Wind.Speed
		})).Return(nil).Once()

		err := service.SaveWeatherData(testData)

		require.NoError(t, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("daily_events_error_fallback_to_existing", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		fixedTime := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
		testData := createTestWeatherData(t, func(d *WeatherData) {
			d.Time = fixedTime
		})

		// SaveDailyEvents fails (e.g., SQLITE_BUSY)
		mockDB.On("SaveDailyEvents", mock.Anything).Return(errors.New("database is locked")).Once()

		// Fallback: GetDailyEvents returns existing row
		existingID := uint(42)
		localDate := fixedTime.UTC().In(time.Local).Format(time.DateOnly)
		mockDB.On("GetDailyEvents", localDate).Return(datastore.DailyEvents{
			ID:       existingID,
			Date:     localDate,
			Country:  "FI",
			CityName: "Helsinki",
		}, nil).Once()

		// SaveHourlyWeather should still be called with the existing row's ID
		var capturedHW *datastore.HourlyWeather
		mockDB.On("SaveHourlyWeather", mock.Anything).Run(func(args mock.Arguments) {
			capturedHW = args.Get(0).(*datastore.HourlyWeather)
		}).Return(nil).Once()

		err := service.SaveWeatherData(testData)

		require.NoError(t, err)
		require.NotNil(t, capturedHW, "HourlyWeather should have been saved")
		assert.Equal(t, existingID, capturedHW.DailyEventsID,
			"Should use existing daily events ID from fallback lookup")
		mockDB.AssertExpectations(t)
	})

	t.Run("daily_events_error_and_lookup_fails_retry_succeeds", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		fixedTime := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
		testData := createTestWeatherData(t, func(d *WeatherData) {
			d.Time = fixedTime
		})

		// First SaveDailyEvents fails (e.g., SQLITE_BUSY)
		mockDB.On("SaveDailyEvents", mock.Anything).Return(errors.New("database is locked")).Once()

		// GetDailyEvents also fails — no existing row
		localDate := fixedTime.UTC().In(time.Local).Format(time.DateOnly)
		mockDB.On("GetDailyEvents", localDate).Return(datastore.DailyEvents{}, errors.New("no rows")).Once()

		// Retry SaveDailyEvents succeeds
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 99
		}).Return(nil).Once()

		// SaveHourlyWeather should be called with the retried row's ID
		var capturedHW *datastore.HourlyWeather
		mockDB.On("SaveHourlyWeather", mock.Anything).Run(func(args mock.Arguments) {
			capturedHW = args.Get(0).(*datastore.HourlyWeather)
		}).Return(nil).Once()

		err := service.SaveWeatherData(testData)

		require.NoError(t, err)
		require.NotNil(t, capturedHW, "HourlyWeather should have been saved")
		assert.Equal(t, uint(99), capturedHW.DailyEventsID,
			"Should use daily events ID from retry save")
		mockDB.AssertExpectations(t)
	})

	t.Run("daily_events_error_and_lookup_fails_retry_also_fails", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		fixedTime := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
		testData := createTestWeatherData(t, func(d *WeatherData) {
			d.Time = fixedTime
		})

		// First SaveDailyEvents fails
		mockDB.On("SaveDailyEvents", mock.Anything).Return(errors.New("database is locked")).Once()

		// GetDailyEvents also fails — no existing row
		localDate := fixedTime.UTC().In(time.Local).Format(time.DateOnly)
		mockDB.On("GetDailyEvents", localDate).Return(datastore.DailyEvents{}, errors.New("no rows")).Once()

		// Retry SaveDailyEvents also fails
		mockDB.On("SaveDailyEvents", mock.Anything).Return(errors.New("database is locked")).Once()

		err := service.SaveWeatherData(testData)

		// Graceful skip: no error returned, next poll will retry
		require.NoError(t, err)

		// SaveHourlyWeather should NOT be called
		mockDB.AssertNotCalled(t, "SaveHourlyWeather", mock.Anything)
	})

	t.Run("daily_events_error_and_lookup_returns_empty_row_retry_succeeds", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		fixedTime := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
		testData := createTestWeatherData(t, func(d *WeatherData) {
			d.Time = fixedTime
		})

		// First SaveDailyEvents fails
		mockDB.On("SaveDailyEvents", mock.Anything).Return(errors.New("database is locked")).Once()

		// GetDailyEvents returns empty row with nil error (v1 legacy "not found" contract)
		localDate := fixedTime.UTC().In(time.Local).Format(time.DateOnly)
		mockDB.On("GetDailyEvents", localDate).Return(datastore.DailyEvents{}, nil).Once()

		// Retry SaveDailyEvents succeeds
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 77
		}).Return(nil).Once()

		// SaveHourlyWeather should be called with the retried row's ID
		var capturedHW *datastore.HourlyWeather
		mockDB.On("SaveHourlyWeather", mock.Anything).Run(func(args mock.Arguments) {
			capturedHW = args.Get(0).(*datastore.HourlyWeather)
		}).Return(nil).Once()

		err := service.SaveWeatherData(testData)

		require.NoError(t, err)
		require.NotNil(t, capturedHW, "HourlyWeather should have been saved")
		assert.Equal(t, uint(77), capturedHW.DailyEventsID,
			"Should use daily events ID from retry save")
		mockDB.AssertExpectations(t)
	})

	t.Run("hourly_weather_error", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		testData := createTestWeatherData(t)

		// SaveDailyEvents succeeds
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 456
		}).Return(nil).Once()

		// SaveHourlyWeather fails
		mockDB.On("SaveHourlyWeather", mock.Anything).Return(errors.New("disk full")).Once()

		err := service.SaveWeatherData(testData)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disk full")
		mockDB.AssertExpectations(t)
	})

	t.Run("daily_events_id_propagated", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		testData := createTestWeatherData(t)

		// Track the ID assigned by SaveDailyEvents
		assignedID := uint(999)

		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = assignedID
		}).Return(nil).Once()

		// Capture the HourlyWeather to verify ID propagation
		var capturedHW *datastore.HourlyWeather
		mockDB.On("SaveHourlyWeather", mock.Anything).Run(func(args mock.Arguments) {
			capturedHW = args.Get(0).(*datastore.HourlyWeather)
		}).Return(nil).Once()

		err := service.SaveWeatherData(testData)

		require.NoError(t, err)
		require.NotNil(t, capturedHW, "HourlyWeather should have been captured")
		assert.Equal(t, assignedID, capturedHW.DailyEventsID, "DailyEventsID should be propagated")
	})

	t.Run("local_time_conversion", func(t *testing.T) {
		mockDB := mocks.NewMockInterface(t)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		// UTC time that will be converted to local
		utcTime := time.Date(2026, 1, 13, 10, 30, 0, 0, time.UTC)
		expectedLocalTime := utcTime.In(time.Local)

		testData := createTestWeatherData(t, func(d *WeatherData) {
			d.Time = utcTime
		})

		// Capture DailyEvents to verify date formatting
		var capturedDE *datastore.DailyEvents
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			capturedDE = args.Get(0).(*datastore.DailyEvents)
			capturedDE.ID = 1
		}).Return(nil).Once()

		// Capture HourlyWeather to verify time
		var capturedHW *datastore.HourlyWeather
		mockDB.On("SaveHourlyWeather", mock.Anything).Run(func(args mock.Arguments) {
			capturedHW = args.Get(0).(*datastore.HourlyWeather)
		}).Return(nil).Once()

		err := service.SaveWeatherData(testData)

		require.NoError(t, err)
		require.NotNil(t, capturedDE)
		require.NotNil(t, capturedHW)

		// Verify date is in local time format
		assert.Equal(t, expectedLocalTime.Format(time.DateOnly), capturedDE.Date)

		// Verify time stored is local time (same instant, different zone)
		assert.True(t, capturedHW.Time.Equal(expectedLocalTime),
			"Stored time should equal local time (same instant)")
	})
}

// =============================================================================
// POLL AND STARTPOLLING TESTS
// =============================================================================

// TestService_Poll tests the Poll method which wraps fetchAndSave.
func TestService_Poll(t *testing.T) {
	t.Run("success_fetch_and_save", func(t *testing.T) {
		setupHTTPMock(t)
		mockDB := mocks.NewMockInterface(t)

		// Register successful Yr.no response
		registerYrNoResponder(t, http.StatusOK, yrNoSuccessResponse(), nil)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		// Mock DB expectations
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 1
		}).Return(nil).Once()
		mockDB.On("SaveHourlyWeather", mock.Anything).Return(nil).Once()

		err := service.Poll()

		require.NoError(t, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("fetch_error", func(t *testing.T) {
		setupHTTPMock(t)
		mockDB := mocks.NewMockInterface(t)

		// Register error response
		registerYrNoResponder(t, http.StatusInternalServerError, `{"error": "server error"}`, nil)

		settings := createTestSettings(t, "yrno")
		service := &Service{
			provider: NewYrNoProvider(),
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		err := service.Poll()

		require.Error(t, err)
		// DB should not be called when fetch fails
		mockDB.AssertNotCalled(t, "SaveDailyEvents", mock.Anything)
		mockDB.AssertNotCalled(t, "SaveHourlyWeather", mock.Anything)
	})

	t.Run("not_modified_returns_nil", func(t *testing.T) {
		setupHTTPMock(t)
		mockDB := mocks.NewMockInterface(t)

		// Register 304 response for conditional request
		httpmock.RegisterResponder("GET", `=~^https://api\.met\.no/weatherapi/locationforecast/2\.0/complete`,
			func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("If-Modified-Since") != "" {
					return httpmock.NewStringResponse(http.StatusNotModified, ""), nil
				}
				resp := httpmock.NewStringResponse(http.StatusOK, yrNoSuccessResponse())
				resp.Header.Set("Last-Modified", "Mon, 13 Jan 2026 10:00:00 GMT")
				return resp, nil
			})

		settings := createTestSettings(t, "yrno")
		provider := NewYrNoProvider()
		service := &Service{
			provider: provider,
			db:       mockDB,
			settings: settings,
			metrics:  nil,
		}

		// First poll to populate lastModified
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 1
		}).Return(nil).Once()
		mockDB.On("SaveHourlyWeather", mock.Anything).Return(nil).Once()

		err := service.Poll()
		require.NoError(t, err)

		// Second poll should return "not modified" - no DB calls
		err = service.Poll()
		require.NoError(t, err, "Not modified should return nil, not error")

		// Only the first poll should have called DB
		mockDB.AssertExpectations(t)
	})
}

// TestService_StartPolling tests that StartPolling respects the stop channel.
func TestService_StartPolling(t *testing.T) {
	t.Run("stops_on_channel_close", func(t *testing.T) {
		setupHTTPMock(t)
		mockDB := mocks.NewMockInterface(t)

		// Register successful response
		registerYrNoResponder(t, http.StatusOK, yrNoSuccessResponse(), nil)

		settings := createTestSettings(t, "yrno", func(s *conf.Settings) {
			// Use a long interval so the ticker doesn't fire during the test
			s.Realtime.Weather.PollInterval = 60
		})
		service := &Service{
			provider:     NewYrNoProvider(),
			db:           mockDB,
			settings:     settings,
			metrics:      nil,
			startupDelay: 0, // No delay for this test
		}

		// Channel to signal initial fetch completion (avoids flaky time.Sleep)
		initialFetchComplete := make(chan struct{})

		// Expect initial fetch to succeed and signal completion
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 1
			close(initialFetchComplete)
		}).Return(nil).Once()
		mockDB.On("SaveHourlyWeather", mock.Anything).Return(nil).Once()

		stopChan := make(chan struct{})
		done := make(chan struct{})

		go func() {
			service.StartPolling(stopChan)
			close(done)
		}()

		// Wait for initial fetch to complete with timeout
		select {
		case <-initialFetchComplete:
			// Initial fetch done, proceed to test stop
		case <-time.After(2 * time.Second):
			t.Fatal("Initial fetch did not complete within timeout")
		}

		// Signal stop
		close(stopChan)

		// Wait for it to stop with timeout
		select {
		case <-done:
			// Success - StartPolling returned
		case <-time.After(2 * time.Second):
			t.Fatal("StartPolling did not stop within timeout")
		}

		mockDB.AssertExpectations(t)
	})
}

// =============================================================================
// STARTUP DELAY TESTS
// =============================================================================

// TestService_StartPolling_StartupDelay tests the startup delay behavior.
func TestService_StartPolling_StartupDelay(t *testing.T) {
	t.Run("delay_postpones_initial_fetch", func(t *testing.T) {
		setupHTTPMock(t)
		mockDB := mocks.NewMockInterface(t)

		registerYrNoResponder(t, http.StatusOK, yrNoSuccessResponse(), nil)

		settings := createTestSettings(t, "yrno", func(s *conf.Settings) {
			s.Realtime.Weather.PollInterval = 60
		})

		delay := 200 * time.Millisecond
		service := &Service{
			provider:     NewYrNoProvider(),
			db:           mockDB,
			settings:     settings,
			metrics:      nil,
			startupDelay: delay,
		}

		fetchCalled := make(chan struct{})
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 1
			close(fetchCalled)
		}).Return(nil).Once()
		mockDB.On("SaveHourlyWeather", mock.Anything).Return(nil).Once()

		stopChan := make(chan struct{})
		done := make(chan struct{})
		startTime := time.Now()

		go func() {
			service.StartPolling(stopChan)
			close(done)
		}()

		// Wait for initial fetch to complete
		select {
		case <-fetchCalled:
			elapsed := time.Since(startTime)
			assert.GreaterOrEqual(t, elapsed, delay,
				"Initial fetch should be delayed by at least the startup delay")
		case <-time.After(5 * time.Second):
			t.Fatal("Initial fetch did not complete within timeout")
		}

		close(stopChan)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("StartPolling did not stop within timeout")
		}

		mockDB.AssertExpectations(t)
	})

	t.Run("zero_delay_fetches_immediately", func(t *testing.T) {
		setupHTTPMock(t)
		mockDB := mocks.NewMockInterface(t)

		registerYrNoResponder(t, http.StatusOK, yrNoSuccessResponse(), nil)

		settings := createTestSettings(t, "yrno", func(s *conf.Settings) {
			s.Realtime.Weather.PollInterval = 60
		})

		service := &Service{
			provider:     NewYrNoProvider(),
			db:           mockDB,
			settings:     settings,
			metrics:      nil,
			startupDelay: 0,
		}

		fetchCalled := make(chan struct{})
		mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
			de := args.Get(0).(*datastore.DailyEvents)
			de.ID = 1
			close(fetchCalled)
		}).Return(nil).Once()
		mockDB.On("SaveHourlyWeather", mock.Anything).Return(nil).Once()

		stopChan := make(chan struct{})
		done := make(chan struct{})
		startTime := time.Now()

		go func() {
			service.StartPolling(stopChan)
			close(done)
		}()

		// Fetch should happen almost immediately
		select {
		case <-fetchCalled:
			elapsed := time.Since(startTime)
			assert.Less(t, elapsed, 500*time.Millisecond,
				"With zero delay, fetch should happen almost immediately")
		case <-time.After(2 * time.Second):
			t.Fatal("Initial fetch did not complete within timeout")
		}

		close(stopChan)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("StartPolling did not stop within timeout")
		}

		mockDB.AssertExpectations(t)
	})

	t.Run("delay_interruptible_via_stop_chan", func(t *testing.T) {
		settings := createTestSettings(t, "yrno", func(s *conf.Settings) {
			s.Realtime.Weather.PollInterval = 60
		})

		// Use a long delay that we will interrupt
		service := &Service{
			provider:     NewYrNoProvider(),
			db:           nil,
			settings:     settings,
			metrics:      nil,
			startupDelay: 10 * time.Second,
		}

		stopChan := make(chan struct{})
		done := make(chan struct{})

		go func() {
			service.StartPolling(stopChan)
			close(done)
		}()

		// Close stop channel after a short time to interrupt the delay
		time.AfterFunc(50*time.Millisecond, func() {
			close(stopChan)
		})

		// StartPolling should return quickly after stopChan is closed
		select {
		case <-done:
			// Success - StartPolling was interrupted during delay
		case <-time.After(2 * time.Second):
			t.Fatal("StartPolling was not interrupted by stopChan during startup delay")
		}
	})
}

// =============================================================================
// BACKOFF AND AUTH FAILURE TESTS
// =============================================================================

// TestBackoffState_Reset verifies that reset clears all failure counters.
func TestBackoffState_Reset(t *testing.T) {
	b := &backoffState{}
	b.recordFailure()
	b.recordAuthFailure()

	b.reset()

	assert.False(t, b.shouldSkip(), "After reset, shouldSkip should be false")
	assert.Equal(t, 0, b.consecutiveFailures)
	assert.Equal(t, 0, b.consecutiveAuthFails)
	assert.Equal(t, time.Duration(0), b.currentBackoff)
}

// TestBackoffState_ExponentialBackoff verifies the backoff increases exponentially.
func TestBackoffState_ExponentialBackoff(t *testing.T) {
	b := &backoffState{}

	backoff1, failures1 := b.recordFailure()
	assert.Equal(t, initialBackoffDuration, backoff1)
	assert.Equal(t, 1, failures1)

	backoff2, failures2 := b.recordFailure()
	assert.Equal(t, initialBackoffDuration*backoffMultiplier, backoff2)
	assert.Equal(t, 2, failures2)

	backoff3, _ := b.recordFailure()
	assert.Equal(t, initialBackoffDuration*backoffMultiplier*backoffMultiplier, backoff3)
}

// TestBackoffState_MaxBackoff verifies the backoff caps at maxBackoffDuration.
func TestBackoffState_MaxBackoff(t *testing.T) {
	b := &backoffState{}

	// Record enough failures to exceed max
	for range 20 {
		b.recordFailure()
	}

	backoff, _ := b.recordFailure()
	assert.LessOrEqual(t, backoff, maxBackoffDuration,
		"Backoff should never exceed max")
}

// TestBackoffState_AuthFailureThreshold verifies auth failures stop after threshold.
func TestBackoffState_AuthFailureThreshold(t *testing.T) {
	b := &backoffState{}

	// First two auth failures should not disable
	for range maxConsecutiveAuthFailures - 1 {
		stopped := b.recordAuthFailure()
		assert.False(t, stopped)
		assert.False(t, b.isAuthDisabled())
	}

	// Third auth failure should disable
	stopped := b.recordAuthFailure()
	assert.True(t, stopped)
	assert.True(t, b.isAuthDisabled())
	assert.True(t, b.shouldSkip(), "After auth disabled, shouldSkip should be true")
}

// TestBackoffState_AuthDisabledNotResetByReset verifies that authDisabled persists
// through reset() calls — a restart or config change is required.
func TestBackoffState_AuthDisabledNotResetByReset(t *testing.T) {
	b := &backoffState{}

	for range maxConsecutiveAuthFailures {
		b.recordAuthFailure()
	}
	assert.True(t, b.isAuthDisabled())

	b.reset()
	assert.True(t, b.isAuthDisabled(),
		"authDisabled should persist through reset — requires service restart")
}

// TestBackoffState_ShouldSkipDuringBackoff verifies skip behavior during backoff window.
func TestBackoffState_ShouldSkipDuringBackoff(t *testing.T) {
	b := &backoffState{}

	// Before any failure, should not skip
	assert.False(t, b.shouldSkip())

	// After failure, should skip (backoff is 1 minute)
	b.recordFailure()
	assert.True(t, b.shouldSkip(), "Should skip during backoff window")
}

// TestFetchAndSave_AuthFailure tests that auth failures are tracked and eventually
// stop retrying.
func TestFetchAndSave_AuthFailure(t *testing.T) {
	settings := createTestSettings(t, "wunderground")

	callCount := 0
	provider := &mockProvider{
		fetchFunc: func(_ *conf.Settings) (*WeatherData, error) {
			callCount++
			return nil, ErrWeatherAuthFailed
		},
	}

	service := &Service{
		provider: provider,
		db:       nil,
		settings: settings,
		metrics:  nil,
	}

	// First few calls should attempt fetches and return auth error
	for range maxConsecutiveAuthFailures {
		err := service.fetchAndSave()
		require.ErrorIs(t, err, ErrWeatherAuthFailed)
	}
	assert.Equal(t, maxConsecutiveAuthFailures, callCount,
		"Provider should be called exactly maxConsecutiveAuthFailures times")

	// After threshold, fetchAndSave should skip without calling provider
	prevCount := callCount
	err := service.fetchAndSave()
	require.NoError(t, err, "Should return nil when skipping due to auth disabled")
	assert.Equal(t, prevCount, callCount, "Provider should NOT be called after auth disabled")
}

// TestFetchAndSave_NoData tests that HTTP 204 is handled gracefully without backoff.
func TestFetchAndSave_NoData(t *testing.T) {
	settings := createTestSettings(t, "wunderground")

	callCount := 0
	provider := &mockProvider{
		fetchFunc: func(_ *conf.Settings) (*WeatherData, error) {
			callCount++
			return nil, ErrWeatherNoData
		},
	}

	service := &Service{
		provider: provider,
		db:       nil,
		settings: settings,
		metrics:  nil,
	}

	// Should return nil and not trigger backoff
	err := service.fetchAndSave()
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Next call should still attempt (no backoff)
	err = service.fetchAndSave()
	require.NoError(t, err)
	assert.Equal(t, 2, callCount, "No backoff should be applied for no-data responses")
}

// TestFetchAndSave_GeneralFailureBackoff verifies exponential backoff for general errors.
func TestFetchAndSave_GeneralFailureBackoff(t *testing.T) {
	settings := createTestSettings(t, "wunderground")

	callCount := 0
	provider := &mockProvider{
		fetchFunc: func(_ *conf.Settings) (*WeatherData, error) {
			callCount++
			return nil, errors.New("network timeout")
		},
	}

	service := &Service{
		provider: provider,
		db:       nil,
		settings: settings,
		metrics:  nil,
	}

	// First call should fail and apply backoff
	err := service.fetchAndSave()
	require.Error(t, err)
	assert.Equal(t, 1, callCount)

	// Second call should be skipped due to backoff (nextAllowedFetchTime is 1 minute away)
	err = service.fetchAndSave()
	require.NoError(t, err, "Should return nil when skipping due to backoff")
	assert.Equal(t, 1, callCount, "Provider should NOT be called during backoff")
}

// TestFetchAndSave_SuccessResetsBackoff verifies that a successful fetch clears backoff.
func TestFetchAndSave_SuccessResetsBackoff(t *testing.T) {
	mockDB := mocks.NewMockInterface(t)

	settings := createTestSettings(t, "wunderground")

	failOnFirst := true
	provider := &mockProvider{
		fetchFunc: func(_ *conf.Settings) (*WeatherData, error) {
			if failOnFirst {
				failOnFirst = false
				return nil, errors.New("transient error")
			}
			return createTestWeatherData(t), nil
		},
	}

	// Mock DB for the successful save
	mockDB.On("SaveDailyEvents", mock.Anything).Run(func(args mock.Arguments) {
		de := args.Get(0).(*datastore.DailyEvents)
		de.ID = 1
	}).Return(nil).Once()
	mockDB.On("SaveHourlyWeather", mock.Anything).Return(nil).Once()

	service := &Service{
		provider: provider,
		db:       mockDB,
		settings: settings,
		metrics:  nil,
	}

	// First call fails — sets backoff
	err := service.fetchAndSave()
	require.Error(t, err)
	assert.True(t, service.backoff.shouldSkip(), "Should be in backoff after failure")

	// Manually clear the backoff time so the next call proceeds
	// (simulating the backoff period elapsing)
	service.backoff.mu.Lock()
	service.backoff.nextAllowedFetchTime = time.Time{}
	service.backoff.mu.Unlock()

	// Second call succeeds — should reset backoff
	err = service.fetchAndSave()
	require.NoError(t, err)
	assert.False(t, service.backoff.shouldSkip(), "Backoff should be cleared after success")

	mockDB.AssertExpectations(t)
}

// TestFetchAndSave_HotReloadCoordinates verifies that coordinates updated via
// conf.StoreSettings (as done by the settings UI) are picked up on the next
// fetch without restarting the weather service. Regression test for a bug
// where the Service cached a *conf.Settings pointer at construction and kept
// sending lat=0, lon=0 to yr.no after the user set their location.
//
// Intentionally NOT t.Parallel(): this test mutates the package-global
// settings snapshot via conf.StoreSettings, so running in parallel with any
// sibling test that touches the global would race. Same pattern as
// TestSettings_GetStore_NoRace in the conf package.
func TestFetchAndSave_HotReloadCoordinates(t *testing.T) {
	// Capture the previous snapshot so any earlier test's state is restored
	// on cleanup — blindly clearing with StoreSettings(nil) could perturb
	// siblings that depend on a previously-loaded global.
	prevSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prevSettings) })

	initial := createTestSettings(t, "yrno", func(s *conf.Settings) {
		s.BirdNET.Latitude = 0
		s.BirdNET.Longitude = 0
	})
	conf.StoreSettings(initial)

	var observedLat, observedLon float64
	provider := &mockProvider{
		fetchFunc: func(settings *conf.Settings) (*WeatherData, error) {
			observedLat = settings.BirdNET.Latitude
			observedLon = settings.BirdNET.Longitude
			return nil, errors.New("short-circuit after observing coords")
		},
	}

	// Service is constructed with the initial (zero-coord) snapshot — this
	// mirrors how NewService is called at startup before the user has
	// configured their location.
	service := &Service{
		provider:     provider,
		providerName: yrNoProviderName,
		db:           nil,
		settings:     initial,
		metrics:      nil,
	}

	_ = service.fetchAndSave()
	assert.InDelta(t, 0.0, observedLat, 1e-9, "initial fetch should use the captured coords")
	assert.InDelta(t, 0.0, observedLon, 1e-9, "initial fetch should use the captured coords")

	// Clear backoff so the second fetch runs, then publish a new snapshot
	// with real coords — exactly what the settings UI does.
	service.backoff.mu.Lock()
	service.backoff.nextAllowedFetchTime = time.Time{}
	service.backoff.mu.Unlock()

	updated := createTestSettings(t, "yrno", func(s *conf.Settings) {
		s.BirdNET.Latitude = 60.1699
		s.BirdNET.Longitude = 24.9384
	})
	conf.StoreSettings(updated)

	_ = service.fetchAndSave()
	assert.InDelta(t, 60.1699, observedLat, 1e-9,
		"fetch should pick up updated latitude from global settings")
	assert.InDelta(t, 24.9384, observedLon, 1e-9,
		"fetch should pick up updated longitude from global settings")
}

// TestNewService_PinsProviderName verifies that the Service pins the
// provider name at construction based on the actual provider implementation
// it selected. This matters because fetchAndSave uses s.providerName in logs
// and metrics, and reading the name from the (hot-reloadable) settings
// snapshot instead could misreport a later UI change while s.provider still
// points at the original implementation.
func TestNewService_PinsProviderName(t *testing.T) {
	tests := []struct {
		configured string
		want       string
	}{
		{yrNoProviderName, yrNoProviderName},
		{openWeatherProviderName, openWeatherProviderName},
		{wundergroundProviderName, wundergroundProviderName},
		{"", yrNoProviderName}, // empty defaults to yrno
	}
	for _, tt := range tests {
		t.Run(tt.configured+"_pins_to_"+tt.want, func(t *testing.T) {
			settings := createTestSettings(t, tt.configured)
			svc, err := NewService(settings, nil, nil)
			require.NoError(t, err)
			require.NotNil(t, svc)
			assert.Equal(t, tt.want, svc.providerName,
				"providerName should reflect the actual provider implementation, not be re-read from settings")
		})
	}
}

// TestWundergroundProvider_HTTP204_NoContent tests that Wunderground returns
// ErrWeatherNoData for HTTP 204.
func TestWundergroundProvider_HTTP204_NoContent(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusNoContent, "")

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.ErrorIs(t, err, ErrWeatherNoData,
		"HTTP 204 should return ErrWeatherNoData sentinel")
}

// TestWundergroundProvider_HTTP401_AuthFailed tests that Wunderground returns
// ErrWeatherAuthFailed for HTTP 401.
func TestWundergroundProvider_HTTP401_AuthFailed(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusUnauthorized,
		wundergroundTestErrorResponse("CDN-0001", "Invalid API key"))

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.ErrorIs(t, err, ErrWeatherAuthFailed,
		"HTTP 401 should return ErrWeatherAuthFailed sentinel")
}
