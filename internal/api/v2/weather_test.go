// weather_test.go: Package api provides tests for API v2 weather endpoints.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// runGetHourlyWeatherForDayNoDataTest runs the hourly weather endpoint test with no data for a given date
func runGetHourlyWeatherForDayNoDataTest(t *testing.T, date string) {
	t.Helper()

	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Setup mock expectations to return empty data
	mockDS.On("GetHourlyWeather", date).Return([]datastore.HourlyWeather{}, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/"+date, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date")
	c.SetParamNames("date")
	c.SetParamValues(date)

	// Test
	if assert.NoError(t, controller.GetHourlyWeatherForDay(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response struct {
			Message string                  `json:"message"`
			Data    []HourlyWeatherResponse `json:"data"`
		}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, "No weather data found for the specified date", response.Message)
		assert.Empty(t, response.Data)
	}

	// Verify mock expectations
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
		var response DailyWeatherResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, "2023-01-01", response.Date)
		assert.Equal(t, time.Unix(1672559400, 0).UTC(), response.Sunrise)
		assert.Equal(t, time.Unix(1672594800, 0).UTC(), response.Sunset)
		assert.Equal(t, "Finland", response.Country)
		assert.Equal(t, "Helsinki", response.CityName)
	}

	// Verify mock expectations
}

// setupWeatherTestEnvironment creates a test environment with Echo, mocks.MockInterface, and Controller
// specifically configured for weather API tests
func setupWeatherTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()
	// Create a new Echo instance
	e := echo.New()

	// Create a test datastore
	mockDS := mocks.NewMockInterface(t)

	// Create a controller with the test datastore
	controller := &Controller{
		Group: e.Group("/api/v2"),
		DS:    mockDS,
	}

	// We don't need to initialize routes for unit tests
	// But we could initialize the weather routes specifically if needed
	// controller.initWeatherRoutes()

	return e, mockDS, controller
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
	var errorResponse ErrorResponse
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
	mockDS.On("GetDailyEvents", "2023-01-01").Return(datastore.DailyEvents{}, errors.New("database error"))

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
	var errorResponse ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Equal(t, "Failed to get daily weather data", errorResponse.Message)
	assert.Contains(t, errorResponse.Error, "database error")

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
			Data []HourlyWeatherResponse `json:"data"`
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

// TestGetHourlyWeatherForDayNoData tests the hourly weather endpoint with no data
func TestGetHourlyWeatherForDayNoData(t *testing.T) {
	runGetHourlyWeatherForDayNoDataTest(t, "2023-01-01")
}

// TestGetHourlyWeatherForDayFutureDate tests the hourly weather endpoint with a future date
func TestGetHourlyWeatherForDayFutureDate(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Get tomorrow's date
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	// Setup mock expectations to return empty data
	mockDS.On("GetHourlyWeather", tomorrow).Return([]datastore.HourlyWeather{}, nil)

	// Create a request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/hourly/"+tomorrow, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/weather/hourly/:date")
	c.SetParamNames("date")
	c.SetParamValues(tomorrow)

	// Test
	if assert.NoError(t, controller.GetHourlyWeatherForDay(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response struct {
			Message string                  `json:"message"`
			Data    []HourlyWeatherResponse `json:"data"`
		}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		assert.Equal(t, "No weather data available for future date", response.Message)
		assert.Empty(t, response.Data)
	}

	// Verify mock expectations
}

// TestGetHourlyWeatherForDayInvalidDate tests the hourly weather endpoint with an invalid date
func TestGetHourlyWeatherForDayInvalidDate(t *testing.T) {
	runGetHourlyWeatherForDayNoDataTest(t, "invalid-date")
}

// TestGetHourlyWeatherForDatabaseError tests the hourly weather endpoint with a database error
func TestGetHourlyWeatherForDayDatabaseError(t *testing.T) {
	// Setup
	e, mockDS, controller := setupWeatherTestEnvironment(t)

	// Setup mock expectations to return an error
	mockDS.On("GetHourlyWeather", "2023-01-01").Return([]datastore.HourlyWeather{}, errors.New("database error"))

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
	var errorResponse ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	require.NoError(t, err)

	// Check error message
	assert.Equal(t, "Failed to get hourly weather data", errorResponse.Message)
	assert.Contains(t, errorResponse.Error, "database error")

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
		var response HourlyWeatherResponse
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
	var errorResponse ErrorResponse
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
	var errorResponse ErrorResponse
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
		var response DetectionWeatherResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		// Daily weather
		assert.Equal(t, "2023-01-01", response.Daily.Date)
		assert.Equal(t, time.Unix(1672559400, 0).UTC(), response.Daily.Sunrise)
		assert.Equal(t, time.Unix(1672594800, 0).UTC(), response.Daily.Sunset)
		assert.Equal(t, "Finland", response.Daily.Country)
		assert.Equal(t, "Helsinki", response.Daily.CityName)

		// Hourly weather (closest to 12:30)
		assert.Equal(t, "12:00:00", response.Hourly.Time)
		assert.InDelta(t, 5.5, response.Hourly.Temperature, 0.01)
		assert.InDelta(t, 4.0, response.Hourly.FeelsLike, 0.01)
		assert.Equal(t, "Clouds", response.Hourly.WeatherMain)
		assert.Equal(t, "scattered clouds", response.Hourly.WeatherDesc)
	}

	// Verify mock expectations
}

// TestGetWeatherForDetectionMissingID tests the weather for detection endpoint with missing ID
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
	var errorResponse ErrorResponse
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
			Daily  *DailyWeatherResponse `json:"daily"`
			Hourly HourlyWeatherResponse `json:"hourly"`
			Time   string                `json:"timestamp"`
		}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response content
		// Daily weather should be present
		assert.NotNil(t, response.Daily)
		assert.Equal(t, "2023-01-01", response.Daily.Date)
		assert.Equal(t, time.Unix(1672559400, 0).UTC(), response.Daily.Sunrise)
		assert.Equal(t, time.Unix(1672594800, 0).UTC(), response.Daily.Sunset)
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
