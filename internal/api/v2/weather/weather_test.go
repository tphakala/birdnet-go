// weather_test.go: tests for the API v2 weather domain endpoints.

package weather

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
	"gorm.io/gorm"
)

// setupWeatherTestEnvironment creates a test environment with Echo, a mock
// datastore, and a weather Handler built from a *apicore.Core via apitest. The
// returned Echo is used to build request contexts; the returned mock is the one
// the Handler reads through, so tests set expectations on it.
func setupWeatherTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))

	return e, mockDS, New(core)
}

// TestGetDailyWeather tests the daily weather endpoint
func TestGetDailyWeather(t *testing.T) {
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "weather")
	t.Attr("type", "integration")
	t.Attr("feature", "daily-weather")

	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Create mock data
	mockDailyEvents := datastore.DailyEvents{
		Date:     "2023-01-01",
		Sunrise:  1672559400, // Unix timestamp for sunrise (approx 7:30 AM)
		Sunset:   1672594800, // Unix timestamp for sunset (approx 5:00 PM)
		Country:  "Finland",
		CityName: "Helsinki",
	}

	// Setup mock expectations
	mockDS.On("GetDailyEvents", "2023-01-01").Return(mockDailyEvents, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/daily/2023-01-01", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/daily/:date")
	c.SetParamNames("date")
	c.SetParamValues("2023-01-01")

	// Test
	if assert.NoError(t, controller.GetDailyWeather(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response dailyWeatherResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, "2023-01-01", response.Date)
		assert.True(t, time.Unix(1672559400, 0).Equal(response.Sunrise), "sunrise time mismatch")
		assert.True(t, time.Unix(1672594800, 0).Equal(response.Sunset), "sunset time mismatch")
		assert.Equal(t, "Finland", response.Country)
		assert.Equal(t, "Helsinki", response.CityName)
	}

	// Verify mock expectations
}

// TestGetDailyWeatherMissingDate tests the daily weather endpoint with missing date
func TestGetDailyWeatherMissingDate(t *testing.T) {
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "weather")
	t.Attr("type", "validation")
	t.Attr("feature", "missing-date")

	// Setup
	e, _, controller := setupWeatherTestEnvironment(t)

	// Create a request with missing date parameter
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/daily/", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/daily/:date")
	// Intentionally not setting the date parameter

	// Test
	err := controller.GetDailyWeather(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Parse error response
	var errorResponse apicore.ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Equal(t, "Date parameter is required", errorResponse.Message)
}

// TestGetDailyWeatherDatabaseError tests the daily weather endpoint with database error
func TestGetDailyWeatherDatabaseError(t *testing.T) {
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "weather")
	t.Attr("type", "error-handling")
	t.Attr("feature", "database-error")

	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Setup mock expectations to return an error
	mockDS.On("GetDailyEvents", "2023-01-01").Return(datastore.DailyEvents{}, errors.NewStd("database error"))

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/daily/2023-01-01", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/daily/:date")
	c.SetParamNames("date")
	c.SetParamValues("2023-01-01")

	// Test
	err := controller.GetDailyWeather(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var errorResponse apicore.ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message: in non-debug mode, Error field uses sanitized message
	assert.Equal(t, "Failed to get daily weather data", errorResponse.Message)
	assert.Equal(t, "Failed to get daily weather data", errorResponse.Error)

	// Verify mock expectations
}

// TestGetHourlyWeatherForDay tests the hourly weather for day endpoint
func TestGetHourlyWeatherForDay(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Create mock data - use Local time since weather data is stored in local time
	mockTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.Local)
	mockHourlyData := []datastore.HourlyWeather{
		{
			Time:        mockTime,
			Temperature: 5.5,
			FeelsLike:   4.0,
			TempMin:     3.2,
			TempMax:     6.8,
			Pressure:    1013,
			Humidity:    75,
			Visibility:  10000,
			WindSpeed:   4.5,
			WindDeg:     270,
			WindGust:    7.2,
			Clouds:      40,
			WeatherMain: "Clouds",
			WeatherDesc: "scattered clouds",
			WeatherIcon: "03d",
		},
		{
			Time:        mockTime.Add(1 * time.Hour),
			Temperature: 6.0,
			FeelsLike:   4.5,
			Humidity:    70,
			WeatherMain: "Clouds",
			WeatherDesc: "broken clouds",
			WeatherIcon: "04d",
		},
	}

	// Setup mock expectations
	mockDS.On("GetHourlyWeather", "2023-01-01").Return(mockHourlyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/2023-01-01", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date")
	c.SetParamNames("date")
	c.SetParamValues("2023-01-01")

	// Test
	if assert.NoError(t, controller.GetHourlyWeatherForDay(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response struct {
			Data []hourlyWeatherResponse `json:"data"`
		}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Len(t, response.Data, 2)

		// Check first hour data
		assert.Equal(t, "12:00:00", response.Data[0].Time)
		assert.InDelta(t, 5.5, response.Data[0].Temperature, 0.01)
		assert.InDelta(t, 4.0, response.Data[0].FeelsLike, 0.01)
		assert.InDelta(t, 3.2, response.Data[0].TempMin, 0.01)
		assert.InDelta(t, 6.8, response.Data[0].TempMax, 0.01)
		assert.Equal(t, 1013, response.Data[0].Pressure)
		assert.Equal(t, 75, response.Data[0].Humidity)
		assert.Equal(t, 10000, response.Data[0].Visibility)
		assert.InDelta(t, 4.5, response.Data[0].WindSpeed, 0.01)
		assert.Equal(t, 270, response.Data[0].WindDeg)
		assert.InDelta(t, 7.2, response.Data[0].WindGust, 0.01)
		assert.Equal(t, 40, response.Data[0].Clouds)
		assert.Equal(t, "Clouds", response.Data[0].WeatherMain)
		assert.Equal(t, "scattered clouds", response.Data[0].WeatherDesc)
		assert.Equal(t, "03d", response.Data[0].WeatherIcon)

		// Check second hour data
		assert.Equal(t, "13:00:00", response.Data[1].Time)
		assert.InDelta(t, 6.0, response.Data[1].Temperature, 0.01)
		assert.InDelta(t, 4.5, response.Data[1].FeelsLike, 0.01)
		assert.Equal(t, 70, response.Data[1].Humidity)
		assert.Equal(t, "Clouds", response.Data[1].WeatherMain)
		assert.Equal(t, "broken clouds", response.Data[1].WeatherDesc)
		assert.Equal(t, "04d", response.Data[1].WeatherIcon)
	}

	// Verify mock expectations
}

// Literal wording of the API messages and the error parameter key. They are repeated here on
// purpose so the tests pin the text clients see instead of the production constants.
const (
	wantInvalidDateMessage = "Invalid date format. Use YYYY-MM-DD"
	wantNoHourlyData       = "No weather data found for the specified date"
	wantFutureDate         = "No weather data available for future date"
	wantParamNameKey       = "paramName"
	wantParamNameValue     = "date"
)

// Fixed clock used by the local-calendar test: 2026-03-14 is the base day, and the two
// instants sit half an hour before and after the following local midnight.
const (
	westOffset             = -8 * time.Hour
	eastOffset             = 13 * time.Hour
	clockYear              = 2026
	clockMonth             = time.March
	clockDay               = 14
	beforeMidnightHour     = 23
	afterMidnightHour      = 0
	clockMinute            = 30
	yesterdayOffsetDays    = -1
	todayOffsetDays        = 0
	tomorrowOffsetDays     = 1
	leapDay                = "2024-02-29"
	leapDayHour            = 13
	leapDayHourParam       = "13"
	leapDayTemperature     = 7.5
	leapDayTemperatureJSON = `"temperature":7.5`
)

// weatherDateRoute describes one route that takes a :date parameter. stubValid sets up the
// datastore call a valid date makes on that route.
type weatherDateRoute struct {
	name       string
	path       string
	paramNames []string
	extra      []string
	handler    func(*Handler) echo.HandlerFunc
	stubValid  func(*mocks.MockInterface)
	wantBody   string
}

func weatherDateRoutes() []weatherDateRoute {
	leapDayRows := func(m *mocks.MockInterface) {
		m.On("GetHourlyWeather", leapDay).Return([]datastore.HourlyWeather{
			{Time: time.Date(2024, time.February, 29, leapDayHour, 0, 0, 0, time.Local), Temperature: leapDayTemperature},
		}, nil)
	}
	return []weatherDateRoute{
		{"daily", "/api/v2/weather/daily/:date", []string{"date"}, nil,
			func(h *Handler) echo.HandlerFunc { return h.GetDailyWeather },
			func(m *mocks.MockInterface) {
				m.On("GetDailyEvents", leapDay).Return(datastore.DailyEvents{Date: leapDay}, nil)
			}, `"date":"` + leapDay + `"`},
		{"hourly", "/api/v2/weather/hourly/:date", []string{"date"}, nil,
			func(h *Handler) echo.HandlerFunc { return h.GetHourlyWeatherForDay },
			leapDayRows, leapDayTemperatureJSON},
		{"hour", "/api/v2/weather/hourly/:date/:hour", []string{"date", "hour"}, []string{leapDayHourParam},
			func(h *Handler) echo.HandlerFunc { return h.GetHourlyWeatherForHour },
			leapDayRows, leapDayTemperatureJSON},
	}
}

// serveWeatherDateRoute calls the route's handler directly with date as the :date parameter.
func serveWeatherDateRoute(t *testing.T, e *echo.Echo, h *Handler, route *weatherDateRoute, date string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath(route.path)
	c.SetParamNames(route.paramNames...)
	c.SetParamValues(append([]string{date}, route.extra...)...)
	require.NoError(t, route.handler(h)(c))
	return rec
}

// TestWeatherDateRoutes_InvalidDateIsBadRequest checks that each route in weatherDateRoutes
// rejects a malformed or impossible date with 400 before the datastore is touched. No mock
// expectation is set, so any datastore call fails the test.
func TestWeatherDateRoutes_InvalidDateIsBadRequest(t *testing.T) {
	invalidDates := []string{
		"notadate",    // garbage
		"2026-2-03",   // unpadded month
		"2026-02-03Z", // trailing text
		"2026-02-30",  // impossible day
		"2026-13-01",  // impossible month
		"2025-02-29",  // Feb 29 in a non-leap year
	}

	for _, route := range weatherDateRoutes() {
		for _, date := range invalidDates {
			t.Run(route.name+"/"+date, func(t *testing.T) {
				e, _, h := setupWeatherTestEnvironment(t)
				rec := serveWeatherDateRoute(t, e, h, &route, date)
				assert.Equal(t, http.StatusBadRequest, rec.Code)

				var resp apicore.ErrorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Equal(t, wantInvalidDateMessage, resp.Message)
				assert.Equal(t, notification.MsgErrDetectionInvalidDate, resp.ErrorKey)
				assert.Equal(t, wantParamNameValue, resp.ErrorParams[wantParamNameKey])
			})
		}
	}
}

// TestWeatherDateRoutes_LeapDayReachesDatastore checks that a real date that a strict
// validator could wrongly reject, a Feb 29 in a leap year, still reaches the datastore on
// each route in weatherDateRoutes and is answered with its data.
func TestWeatherDateRoutes_LeapDayReachesDatastore(t *testing.T) {
	for _, route := range weatherDateRoutes() {
		t.Run(route.name, func(t *testing.T) {
			e, mockDS, h := setupWeatherTestEnvironment(t)
			route.stubValid(mockDS)

			rec := serveWeatherDateRoute(t, e, h, &route, leapDay)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), route.wantBody)
		})
	}
}

// TestGetHourlyWeatherForDay_EmptyResultFutureUsesLocalCalendar checks that an empty hourly
// result is labelled future only when the requested date is after the local calendar date,
// on both sides of UTC and on both sides of local midnight. It replaces time.Local, so it
// must stay sequential (no t.Parallel).
func TestGetHourlyWeatherForDay_EmptyResultFutureUsesLocalCalendar(t *testing.T) {
	westZone := time.FixedZone("UTC-8", int(westOffset/time.Second))
	eastZone := time.FixedZone("UTC+13", int(eastOffset/time.Second))

	zones := []struct {
		name string
		zone *time.Location
	}{{"west", westZone}, {"east", eastZone}}

	// Instants just before and just after local midnight, on a fixed base day.
	instants := []struct {
		name string
		at   func(*time.Location) time.Time
	}{
		{"beforeMidnight", func(l *time.Location) time.Time {
			return time.Date(clockYear, clockMonth, clockDay, beforeMidnightHour, clockMinute, 0, 0, l)
		}},
		{"afterMidnight", func(l *time.Location) time.Time {
			return time.Date(clockYear, clockMonth, clockDay+1, afterMidnightHour, clockMinute, 0, 0, l)
		}},
	}

	days := []struct {
		name    string
		offset  int
		message string
	}{
		{"yesterday", yesterdayOffsetDays, wantNoHourlyData},
		{"today", todayOffsetDays, wantNoHourlyData},
		{"tomorrow", tomorrowOffsetDays, wantFutureDate},
	}

	for _, z := range zones {
		for _, in := range instants {
			for _, d := range days {
				t.Run(z.name+"/"+in.name+"/"+d.name, func(t *testing.T) {
					prev := time.Local
					t.Cleanup(func() { time.Local = prev })
					time.Local = z.zone

					now := in.at(z.zone)
					date := now.AddDate(0, 0, d.offset).Format(time.DateOnly)

					synctest.Test(t, func(t *testing.T) {
						e, mockDS, h := setupWeatherTestEnvironment(t)
						time.Sleep(time.Until(now))
						mockDS.On("GetHourlyWeather", date).Return([]datastore.HourlyWeather{}, nil)

						req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/"+date, http.NoBody)
						rec := httptest.NewRecorder()
						c := e.NewContext(req, rec)
						c.SetPath("/api/v2/weather/hourly/:date")
						c.SetParamNames("date")
						c.SetParamValues(date)

						require.NoError(t, h.GetHourlyWeatherForDay(c))
						assert.Equal(t, http.StatusOK, rec.Code)

						var resp struct {
							Message string                  `json:"message"`
							Data    []hourlyWeatherResponse `json:"data"`
						}
						require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
						assert.Equal(t, d.message, resp.Message)
						require.NotNil(t, resp.Data, "data must be a JSON array, not null")
						assert.Empty(t, resp.Data)
					})
				})
			}
		}
	}
}

// TestGetHourlyWeatherForDatabaseError tests the hourly weather endpoint with a database error
func TestGetHourlyWeatherForDayDatabaseError(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Setup mock expectations to return an error
	mockDS.On("GetHourlyWeather", "2023-01-01").Return([]datastore.HourlyWeather{}, errors.NewStd("database error"))

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/2023-01-01", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date")
	c.SetParamNames("date")
	c.SetParamValues("2023-01-01")

	// Test
	err := controller.GetHourlyWeatherForDay(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var errorResponse apicore.ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message: in non-debug mode, Error field uses sanitized message
	assert.Equal(t, "Failed to get hourly weather data", errorResponse.Message)
	assert.Equal(t, "Failed to get hourly weather data", errorResponse.Error)

	// Verify mock expectations
}

// TestGetHourlyWeatherForHour tests the hourly weather for specific hour endpoint
func TestGetHourlyWeatherForHour(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Create mock data for two hours - use Local time since weather data is stored in local time
	mockTime1 := time.Date(2023, 1, 1, 12, 0, 0, 0, time.Local) // 12:00
	mockTime2 := time.Date(2023, 1, 1, 13, 0, 0, 0, time.Local) // 13:00
	mockHourlyData := []datastore.HourlyWeather{
		{
			Time:        mockTime1,
			Temperature: 5.5,
			FeelsLike:   4.0,
			TempMin:     3.2,
			TempMax:     6.8,
			WeatherMain: "Clouds",
			WeatherDesc: "scattered clouds",
		},
		{
			Time:        mockTime2,
			Temperature: 6.0,
			FeelsLike:   4.5,
			TempMin:     3.5,
			TempMax:     7.0,
			WeatherMain: "Clear",
			WeatherDesc: "clear sky",
		},
	}

	// Setup mock expectations
	mockDS.On("GetHourlyWeather", "2023-01-01").Return(mockHourlyData, nil)

	// Create a request for hour 13
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/2023-01-01/13", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date/:hour")
	c.SetParamNames("date", "hour")
	c.SetParamValues("2023-01-01", "13")

	// Test
	if assert.NoError(t, controller.GetHourlyWeatherForHour(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response hourlyWeatherResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content for hour 13
		assert.Equal(t, "13:00:00", response.Time)
		assert.InDelta(t, 6.0, response.Temperature, 0.01)
		assert.InDelta(t, 4.5, response.FeelsLike, 0.01)
		assert.InDelta(t, 3.5, response.TempMin, 0.01)
		assert.InDelta(t, 7.0, response.TempMax, 0.01)
		assert.Equal(t, "Clear", response.WeatherMain)
		assert.Equal(t, "clear sky", response.WeatherDesc)
	}

	// Verify mock expectations
}

// TestGetHourlyWeatherForHourNotFound tests the hourly weather for hour endpoint when hour not found
func TestGetHourlyWeatherForHourNotFound(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Create mock data for hour 12 only - use Local time since weather data is stored in local time
	mockTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.Local)
	mockHourlyData := []datastore.HourlyWeather{
		{
			Time:        mockTime,
			Temperature: 5.5,
			FeelsLike:   4.0,
			WeatherMain: "Clouds",
		},
	}

	// Setup mock expectations
	mockDS.On("GetHourlyWeather", "2023-01-01").Return(mockHourlyData, nil)

	// Create a request for hour 13 (which doesn't exist in our mock data)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/2023-01-01/13", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date/:hour")
	c.SetParamNames("date", "hour")
	c.SetParamValues("2023-01-01", "13")

	// Test
	err := controller.GetHourlyWeatherForHour(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Parse error response
	var errorResponse apicore.ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Equal(t, "Weather data not found for specified hour", errorResponse.Message)

	// Verify mock expectations
}

// TestGetHourlyWeatherForHourInvalidHour tests the hourly weather for hour endpoint with invalid hour format
func TestGetHourlyWeatherForHourInvalidHour(t *testing.T) {
	// Setup
	e, _, controller := setupWeatherTestEnvironment(t)

	// Create a request with invalid hour
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/2023-01-01/not-an-hour", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date/:hour")
	c.SetParamNames("date", "hour")
	c.SetParamValues("2023-01-01", "not-an-hour")

	// Test
	err := controller.GetHourlyWeatherForHour(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Parse error response
	var errorResponse apicore.ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Equal(t, "Invalid hour format", errorResponse.Message)
}

// TestGetWeatherForDetection tests the weather for detection endpoint
func TestGetWeatherForDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Create mock detection note
	mockNote := datastore.Note{
		ID:   123,
		Date: "2023-01-01",
		Time: "12:30:00",
	}

	// Create mock daily events
	mockDailyEvents := datastore.DailyEvents{
		Date:     "2023-01-01",
		Sunrise:  1672559400, // Unix timestamp for sunrise (approx 7:30 AM)
		Sunset:   1672594800, // Unix timestamp for sunset (approx 5:00 PM)
		Country:  "Finland",
		CityName: "Helsinki",
	}

	// Create mock hourly weather - use Local time since weather data is stored in local time
	mockTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.Local)
	mockHourlyData := []datastore.HourlyWeather{
		{
			Time:        mockTime,
			Temperature: 5.5,
			FeelsLike:   4.0,
			WeatherMain: "Clouds",
			WeatherDesc: "scattered clouds",
		},
	}

	// Setup mock expectations
	mockDS.On("Get", "123").Return(mockNote, nil)
	mockDS.On("GetDailyEvents", "2023-01-01").Return(mockDailyEvents, nil)
	mockDS.On("GetHourlyWeather", "2023-01-01").Return(mockHourlyData, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/detection/123", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/detection/:id")
	c.SetParamNames("id")
	c.SetParamValues("123")

	// Test
	if assert.NoError(t, controller.GetWeatherForDetection(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response detectionWeatherResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		// Daily weather
		assert.Equal(t, "2023-01-01", response.Daily.Date)
		assert.True(t, time.Unix(1672559400, 0).Equal(response.Daily.Sunrise), "sunrise time mismatch")
		assert.True(t, time.Unix(1672594800, 0).Equal(response.Daily.Sunset), "sunset time mismatch")
		assert.Equal(t, "Finland", response.Daily.Country)
		assert.Equal(t, "Helsinki", response.Daily.CityName)

		// Hourly weather (closest to 12:30)
		assert.Equal(t, "12:00:00", response.Hourly.Time)
		assert.InDelta(t, 5.5, response.Hourly.Temperature, 0.01)
		assert.InDelta(t, 4.0, response.Hourly.FeelsLike, 0.01)
		assert.Equal(t, "Clouds", response.Hourly.WeatherMain)
		assert.Equal(t, "scattered clouds", response.Hourly.WeatherDesc)

		// Time of day is classified from the stored instant, 12:30 on a January day
		assert.Equal(t, datastore.TimeOfDayDay, response.TimeOfDay)
	}

	// Verify mock expectations
}

// repeatedHourCase names a zone and the two UTC instants at which its clock reads
// the same wall time at the end of DST (first the DST occurrence, then standard time).
type repeatedHourCase struct {
	name   string
	zone   string
	first  time.Time
	second time.Time
}

// TestGetWeatherForDetection_RepeatedHourMatchesOwnInstant verifies that the
// hourly weather of a detection in a repeated DST hour is the row at the
// detection's own stored instant, for both occurrences. Rebuilding the instant
// from the wall-clock text resolves one occurrence of each pair to the other.
func TestGetWeatherForDetection_RepeatedHourMatchesOwnInstant(t *testing.T) {
	t.Attr("component", "weather")
	t.Attr("type", "integration")
	t.Attr("feature", "repeated-hour-weather")

	zones := []repeatedHourCase{
		{
			name: "New York", zone: "America/New_York",
			first:  time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC),
			second: time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC),
		},
		{
			name: "Helsinki", zone: "Europe/Helsinki",
			first:  time.Date(2026, time.October, 25, 0, 30, 0, 0, time.UTC),
			second: time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC),
		},
		{
			// A 30 minute shift: an hour-only match would not pass.
			name: "Lord Howe", zone: "Australia/Lord_Howe",
			first:  time.Date(2026, time.April, 4, 14, 50, 0, 0, time.UTC),
			second: time.Date(2026, time.April, 4, 15, 20, 0, 0, time.UTC),
		},
	}

	const (
		firstTemperature  = 11.0
		secondTemperature = 22.0
	)

	for _, tc := range zones {
		loc, err := time.LoadLocation(tc.zone)
		require.NoError(t, err)

		occurrences := []struct {
			name        string
			instant     time.Time
			temperature float64
		}{
			{"first occurrence", tc.first, firstTemperature},
			{"second occurrence", tc.second, secondTemperature},
		}

		for _, occ := range occurrences {
			t.Run(tc.name+" "+occ.name, func(t *testing.T) {
				e, mockDS, controller := setupWeatherTestEnvironment(t)
				repo := mocks.NewMockDetectionRepository(t)
				controller.Repo = repo

				result := &detection.Result{ID: 7, Timestamp: occ.instant.In(loc)}
				date := result.Date()
				repo.EXPECT().Get(mock.Anything, "7").Return(result, nil)

				mockDS.On("GetDailyEvents", date).Return(datastore.DailyEvents{Date: date}, nil)
				mockDS.On("GetHourlyWeather", date).Return([]datastore.HourlyWeather{
					{Time: tc.first, Temperature: firstTemperature},
					{Time: tc.second, Temperature: secondTemperature},
				}, nil)

				req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/detection/7", http.NoBody)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				c.SetPath("/api/v2/weather/detection/:id")
				c.SetParamNames("id")
				c.SetParamValues("7")

				require.NoError(t, controller.GetWeatherForDetection(c))
				require.Equal(t, http.StatusOK, rec.Code)

				var response detectionWeatherResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				assert.InDelta(t, occ.temperature, response.Hourly.Temperature, 0.01,
					"the weather row must be the one at the detection's own instant")
			})
		}
	}
}

// TestGetWeatherForDetectionMissingID tests the weather for detection endpoint with missing ID
// TestGetWeatherForDetection_NotFound checks that a detection missing from either store
// answers 404: the legacy bridge reports gorm.ErrRecordNotFound and the v2 repository
// reports repository.ErrDetectionNotFound.
func TestGetWeatherForDetection_NotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"legacy bridge", gorm.ErrRecordNotFound},
		{"v2 repository", repository.ErrDetectionNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _, controller := setupWeatherTestEnvironment(t)
			repo := mocks.NewMockDetectionRepository(t)
			controller.Repo = repo
			repo.EXPECT().Get(mock.Anything, "404").Return(nil, tc.err)

			req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/detection/404", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/weather/detection/:id")
			c.SetParamNames("id")
			c.SetParamValues("404")

			require.NoError(t, controller.GetWeatherForDetection(c))
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestGetWeatherForDetectionMissingID(t *testing.T) {
	// Setup
	e, _, controller := setupWeatherTestEnvironment(t)

	// Create a request with missing ID
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/detection/", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/detection/:id")
	// Intentionally not setting the ID parameter

	// Test
	err := controller.GetWeatherForDetection(c)

	// The controller's HandleError method returns a JSON response, not an error
	require.NoError(t, err)

	// Check response code
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Parse error response
	var errorResponse apicore.ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Equal(t, "Detection ID is required", errorResponse.Message)
}

// TestGetLatestWeather tests the latest weather endpoint
func TestGetLatestWeather(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Create mock latest hourly weather - use Local time since weather data is stored in local time
	mockLatestTime := time.Date(2023, 1, 1, 15, 0, 0, 0, time.Local)
	mockLatestWeather := &datastore.HourlyWeather{
		Time:        mockLatestTime,
		Temperature: 7.5,
		FeelsLike:   6.0,
		WeatherMain: "Clear",
		WeatherDesc: "clear sky",
	}

	// Create mock daily events matching the date of latest weather
	mockDailyEvents := datastore.DailyEvents{
		Date:     "2023-01-01",
		Sunrise:  1672559400,
		Sunset:   1672594800,
		Country:  "Finland",
		CityName: "Helsinki",
	}

	// Setup mock expectations
	mockDS.On("LatestHourlyWeather").Return(mockLatestWeather, nil)
	mockDS.On("GetDailyEvents", "2023-01-01").Return(mockDailyEvents, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/latest", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/latest")

	// Test
	if assert.NoError(t, controller.GetLatestWeather(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response struct {
			Daily  *dailyWeatherResponse `json:"daily"`
			Hourly hourlyWeatherResponse `json:"hourly"`
			Time   string                `json:"timestamp"`
		}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		// Daily weather should be present
		assert.NotNil(t, response.Daily)
		assert.Equal(t, "2023-01-01", response.Daily.Date)
		assert.True(t, time.Unix(1672559400, 0).Equal(response.Daily.Sunrise), "sunrise time mismatch")
		assert.True(t, time.Unix(1672594800, 0).Equal(response.Daily.Sunset), "sunset time mismatch")
		assert.Equal(t, "Finland", response.Daily.Country)
		assert.Equal(t, "Helsinki", response.Daily.CityName)

		// Hourly weather
		assert.Equal(t, "15:00:00", response.Hourly.Time)
		assert.InDelta(t, 7.5, response.Hourly.Temperature, 0.01)
		assert.InDelta(t, 6.0, response.Hourly.FeelsLike, 0.01)
		assert.Equal(t, "Clear", response.Hourly.WeatherMain)
		assert.Equal(t, "clear sky", response.Hourly.WeatherDesc)

		// Timestamp should be present
		assert.NotEmpty(t, response.Time)
	}

	// Verify mock expectations
}
