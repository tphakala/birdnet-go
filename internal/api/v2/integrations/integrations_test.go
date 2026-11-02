// integrations_test.go: tests for the api/v2 integrations domain endpoints.

package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test constants for integration tests
const testBirdNetName = "TestBirdNet"

// testMQTTTopic is the topic the MQTT status tests configure and expect echoed back.
const testMQTTTopic = "birdnet/detections"

// connectionHandlerCase is one row of runIntegrationConnectionHandlerTest.
// Every request is sent with an already-cancelled context, so a valid
// configuration reaches the streaming entry without any network stage running.
// expectedType, when set, is compared with the response Content-Type and the
// response must also carry the streaming Cache-Control header.
type connectionHandlerCase struct {
	name           string
	setupSettings  func(*Handler)
	expectedStatus int
	expectedBody   string
	expectedType   string
}

// runIntegrationConnectionHandlerTest runs table-driven tests for integration connection handlers
func runIntegrationConnectionHandlerTest(t *testing.T, handlerFunc func(*Handler, echo.Context) error, endpoint string, testCases []connectionHandlerCase) {
	t.Helper()

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, controller := newIntegrationsTestHandler(t)

			// Configure settings
			tc.setupSettings(controller)

			// Create request with appropriate body
			var req *http.Request
			if strings.Contains(endpoint, "birdweather") {
				// BirdWeather endpoint expects JSON body matching the controller settings
				bwSettings := controller.Settings.Load().Realtime.Birdweather
				bodyJSON := fmt.Sprintf(`{"enabled":%t,"id":%q,"threshold":%f,"locationAccuracy":%f}`,
					bwSettings.Enabled, bwSettings.ID, bwSettings.Threshold, bwSettings.LocationAccuracy)
				req = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(bodyJSON))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(http.MethodPost, endpoint, http.NoBody)
			}
			req = req.WithContext(cancelledContext(t))
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler
			err := handlerFunc(controller, c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)
			if tc.expectedType != "" {
				assert.Equal(t, tc.expectedType, rec.Header().Get(echo.HeaderContentType))
				assert.Equal(t, "no-cache", rec.Header().Get(echo.HeaderCacheControl))
			}

			// Check response body
			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}

// TestGetMQTTStatus tests the GetMQTTStatus handler
func TestGetMQTTStatus(t *testing.T) {
	refusingBroker := newRefusingMQTTBroker(t)

	// Define test cases
	testCases := []struct {
		name           string
		mqttEnabled    bool
		mqttBroker     string
		mqttTopic      string
		expectedStatus int
		validateResult func(*testing.T, map[string]any)
	}{
		{
			name:           "MQTT Disabled",
			mqttEnabled:    false,
			mqttBroker:     refusingBroker,
			mqttTopic:      testMQTTTopic,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.False(t, result["connected"].(bool), "Connected should be false when MQTT is disabled")
				assert.Equal(t, refusingBroker, result["broker"], "Broker should match configuration")
				assert.Equal(t, testMQTTTopic, result["topic"], "Topic should match configuration")
				_, hasLastError := result["last_error"]
				assert.False(t, hasLastError, "There should be no error when MQTT is disabled")
			},
		},
		{
			name:           "MQTT Enabled but Not Connected",
			mqttEnabled:    true,
			mqttBroker:     refusingBroker,
			mqttTopic:      testMQTTTopic,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.False(t, result["connected"].(bool), "Connected should be false when MQTT connection fails")
				assert.Equal(t, refusingBroker, result["broker"], "Broker should match configuration")
				assert.Equal(t, testMQTTTopic, result["topic"], "Topic should match configuration")
				assert.Contains(t, result["last_error"], "error:connection:mqtt_broker", "Error should mention connection failure")
			},
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, controller := newIntegrationsTestHandler(t)

			// Configure settings
			controller.Settings.Load().Realtime.MQTT.Enabled = tc.mqttEnabled
			controller.Settings.Load().Realtime.MQTT.Broker = tc.mqttBroker
			controller.Settings.Load().Realtime.MQTT.Topic = tc.mqttTopic
			controller.Settings.Load().Main.Name = testBirdNetName

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler - we'll patch the client creation in the real system
			err := controller.GetMQTTStatus(c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Parse and validate response
			var result map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			require.NoError(t, err)

			tc.validateResult(t, result)
		})
	}
}

// TestGetBirdWeatherStatus tests the GetBirdWeatherStatus handler
func TestGetBirdWeatherStatus(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		bwEnabled      bool
		bwID           string
		bwThreshold    float64
		bwLocationAcc  float64
		expectedStatus int
		validateResult func(*testing.T, map[string]any)
	}{
		{
			name:           "BirdWeather Disabled",
			bwEnabled:      false,
			bwID:           "",
			bwThreshold:    0.7,
			bwLocationAcc:  50.0,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.False(t, result["enabled"].(bool), "Enabled should be false")
				assert.Empty(t, result["station_id"], "Station ID should be empty")
				assert.InDelta(t, 0.7, result["threshold"], 0.01, "Threshold should match configuration")
				assert.InDelta(t, 50.0, result["location_accuracy"], 0.01, "Location accuracy should match configuration")
			},
		},
		{
			name:           "BirdWeather Enabled",
			bwEnabled:      true,
			bwID:           "ABC123",
			bwThreshold:    0.8,
			bwLocationAcc:  100.0,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.True(t, result["enabled"].(bool), "Enabled should be true")
				assert.Equal(t, "ABC123", result["station_id"], "Station ID should match configuration")
				assert.InDelta(t, 0.8, result["threshold"], 0.01, "Threshold should match configuration")
				assert.InDelta(t, 100.0, result["location_accuracy"], 0.01, "Location accuracy should match configuration")
			},
		},
	}

	// Run all test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, controller := newIntegrationsTestHandler(t)

			// Configure settings for the test case
			controller.Settings.Load().Realtime.Birdweather.Enabled = tc.bwEnabled
			controller.Settings.Load().Realtime.Birdweather.ID = tc.bwID
			controller.Settings.Load().Realtime.Birdweather.Threshold = tc.bwThreshold
			controller.Settings.Load().Realtime.Birdweather.LocationAccuracy = tc.bwLocationAcc

			// Create request context
			req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/birdweather/status", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call the handler
			err := controller.GetBirdWeatherStatus(c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Parse response body
			var result map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			require.NoError(t, err)

			// Validate the result using the test case's validation function
			tc.validateResult(t, result)
		})
	}
}

// TestTestMQTTConnection tests the TestMQTTConnection handler
func TestTestMQTTConnection(t *testing.T) {
	refusingBroker := newRefusingMQTTBroker(t)

	// Define test cases
	testCases := []connectionHandlerCase{
		{
			name: "MQTT Not Enabled",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.MQTT.Enabled = false
				controller.Settings.Load().Realtime.MQTT.Broker = refusingBroker
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"MQTT is not enabled in settings"}`,
		},
		{
			name: "Broker Not Configured",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.MQTT.Enabled = true
				controller.Settings.Load().Realtime.MQTT.Broker = ""
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"MQTT broker not configured"}`,
		},
		{
			name: "Valid Config Sets Streaming Headers",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.MQTT.Enabled = true
				controller.Settings.Load().Realtime.MQTT.Broker = refusingBroker
			},
			expectedStatus: http.StatusOK,
			expectedType:   echo.MIMEApplicationJSON,
		},
	}

	runIntegrationConnectionHandlerTest(t, (*Handler).TestMQTTConnection, "/api/v2/integrations/mqtt/test", testCases)
}

// TestTestBirdWeatherConnection tests the TestBirdWeatherConnection handler
func TestTestBirdWeatherConnection(t *testing.T) {
	// Define test cases
	testCases := []connectionHandlerCase{
		{
			name: "BirdWeather Not Enabled",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.Birdweather.Enabled = false
				controller.Settings.Load().Realtime.Birdweather.ID = "ABC123"
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"BirdWeather integration is not enabled","state":"failed"}`,
		},
		{
			name: "Station ID Not Configured",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.Birdweather.Enabled = true
				controller.Settings.Load().Realtime.Birdweather.ID = ""
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"BirdWeather station ID not configured","state":"failed"}`,
		},
		{
			name: "Valid Config Sets Streaming Headers",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.Birdweather.Enabled = true
				controller.Settings.Load().Realtime.Birdweather.ID = "ABC123"
			},
			expectedStatus: http.StatusOK,
			expectedType:   echo.MIMEApplicationJSON,
		},
	}

	runIntegrationConnectionHandlerTest(t, (*Handler).TestBirdWeatherConnection, "/api/v2/integrations/birdweather/test", testCases)
}

// TestTestEBirdConnection tests the TestEBirdConnection handler
func TestTestEBirdConnection(t *testing.T) {
	testCases := []struct {
		name           string
		requestBody    string
		expectedStatus int
		expectedType   string
		validateResult func(*testing.T, string)
	}{
		{
			name:           "eBird Not Enabled",
			requestBody:    `{"enabled":false,"apiKey":"test-key","locale":"en"}`,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, `"success":false`)
				assert.Contains(t, body, "not enabled")
			},
		},
		{
			name:           "API Key Not Configured",
			requestBody:    `{"enabled":true,"apiKey":"","locale":"en"}`,
			expectedStatus: http.StatusBadRequest,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, `"success":false`)
				assert.Contains(t, body, "API key is required")
			},
		},
		{
			name:           "Invalid Request Body",
			requestBody:    `{invalid json`,
			expectedStatus: http.StatusBadRequest,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, "Invalid eBird test request")
			},
		},
		{
			name:           "Valid Request Enters Streaming",
			requestBody:    `{"enabled":true,"apiKey":"test-key-123","locale":"en"}`,
			expectedStatus: http.StatusOK,
			expectedType:   mimeNDJSON,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				firstLine, _, _ := strings.Cut(body, "\n")
				var stage WeatherTestStage
				require.NoError(t, json.Unmarshal([]byte(firstLine), &stage), "first NDJSON line: %q", firstLine)
				assert.Equal(t, "connectivity", stage.ID)
				assert.Equal(t, "in_progress", stage.Status)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e, controller := newIntegrationsTestHandler(t)

			// The request context is already cancelled, so a valid request reaches
			// the streaming stages but no HTTP request leaves the process.
			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/ebird/test",
				strings.NewReader(tc.requestBody)).WithContext(cancelledContext(t))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			require.NoError(t, controller.TestEBirdConnection(c))

			assert.Equal(t, tc.expectedStatus, rec.Code)
			if tc.expectedType != "" {
				assert.Equal(t, tc.expectedType, rec.Header().Get(echo.HeaderContentType))
			}

			tc.validateResult(t, strings.TrimSpace(rec.Body.String()))
		})
	}
}

// fakeStageResult is the result type streamed by fakeIntegrationClient.
type fakeStageResult struct {
	Stage string `json:"stage"`
}

// fakeIntegrationClient is an IntegrationTestClient that sends one result. When
// cancelRequest is set it then cancels the HTTP request context, as a departing
// client would, and waits for the test context to end before returning.
type fakeIntegrationClient struct {
	result        fakeStageResult
	cancelRequest context.CancelFunc
	cleanups      atomic.Int32
}

func (f *fakeIntegrationClient) TestConnection(ctx context.Context, resultChan chan<- fakeStageResult) {
	resultChan <- f.result
	if f.cancelRequest != nil {
		f.cancelRequest()
		<-ctx.Done()
	}
}

func (f *fakeIntegrationClient) Cleanup() { f.cleanups.Add(1) }

// TestRunStreamingIntegrationTest verifies the shared streaming runner: it
// streams client results, writes one final "completed" line when the test
// finishes, writes nothing more once the HTTP client has gone, and cleans the
// client up exactly once. Lines are checked without relying on their order.
func TestRunStreamingIntegrationTest(t *testing.T) {
	const streamTimeout = 20 * time.Second

	tests := []struct {
		name          string
		clientGone    bool
		wantCompleted int
	}{
		{name: "completes", wantCompleted: 1},
		{name: "client gone", clientGone: true, wantCompleted: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The handler is built outside the bubble.
			e, controller := newIntegrationsTestHandler(t)

			synctest.Test(t, func(t *testing.T) {
				reqCtx, cancel := context.WithCancel(t.Context())
				defer cancel()
				req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/fake/test", http.NoBody).WithContext(reqCtx)
				rec := httptest.NewRecorder()

				client := &fakeIntegrationClient{result: fakeStageResult{Stage: "connect"}}
				if tt.clientGone {
					client.cancelRequest = cancel
				}

				err := runStreamingIntegrationTest(controller, e.NewContext(req, rec),
					make(chan fakeStageResult), client, streamTimeout, "fake")
				require.NoError(t, err)
				synctest.Wait()

				var results, completed int
				for line := range strings.SplitSeq(strings.TrimSpace(rec.Body.String()), "\n") {
					var decoded map[string]any
					require.NoError(t, json.Unmarshal([]byte(line), &decoded), "line: %q", line)
					switch {
					case decoded["stage"] == "connect":
						results++
					case decoded["state"] == "completed":
						completed++
					default:
						assert.Fail(t, "unexpected line", "line: %q", line)
					}
				}
				assert.Equal(t, 1, results, "the client result is streamed once")
				assert.Equal(t, tt.wantCompleted, completed, "completed lines in %q", rec.Body.String())
				assert.Equal(t, int32(1), client.cleanups.Load(), "client is cleaned up once")
			})
		})
	}
}

// redactedSecretPlaceholder is the sentinel the settings UI returns in place of
// stored secrets; it aliases the shared apicore.RedactedValue constant so the
// tests and the handlers agree on the exact value a client sends when the user
// has not re-entered a secret.
const redactedSecretPlaceholder = apicore.RedactedValue

// publishWeatherTestSettings sets the saved weather provider config the handler
// reads via CurrentSettings() and returns the handler. It re-publishes the
// settings so CurrentSettings() (which reads the global snapshot first) observes
// the saved keys/endpoint.
func publishWeatherTestSettings(t *testing.T, mutate func(*conf.Settings)) (*echo.Echo, *Handler) {
	t.Helper()
	e, controller := newIntegrationsTestHandler(t)
	settings := controller.Settings.Load()
	mutate(settings)
	apitest.PublishTestSettings(t, settings)
	return e, controller
}

// TestTestWeatherConnection_RestoresRedactedAPIKey verifies the weather
// test-connection handler restores redacted API-key placeholders from the saved
// settings before validating the request, fixing the bug where clicking "Test"
// after saving (without re-entering the key) failed with a bogus authentication
// error. Each case sends the placeholder through the handler. With a saved key
// the request passes validation and starts streaming; with none it fails
// validation. The request context is already cancelled, so a request that
// reaches the streaming stages stops at the first network stage without any
// HTTP request leaving the process.
func TestTestWeatherConnection_RestoresRedactedAPIKey(t *testing.T) {
	const savedKey = "real-saved-key-123"

	providers := []struct {
		name      string
		provider  string
		bodyField string
		saveKey   func(s *conf.Settings, key string)
	}{
		{
			name:      "OpenWeather",
			provider:  WeatherProviderOpenWeather,
			bodyField: "openWeather",
			saveKey:   func(s *conf.Settings, key string) { s.Realtime.Weather.OpenWeather.APIKey = key },
		},
		{
			name:      "PirateWeather",
			provider:  WeatherProviderPirateWeather,
			bodyField: "pirateWeather",
			saveKey:   func(s *conf.Settings, key string) { s.Realtime.Weather.PirateWeather.APIKey = key },
		},
	}

	for _, p := range providers {
		for _, saved := range []string{"", savedKey} {
			t.Run(fmt.Sprintf("%s saved key %q", p.name, saved), func(t *testing.T) {
				e, controller := publishWeatherTestSettings(t, func(s *conf.Settings) {
					p.saveKey(s, saved)
				})

				body := fmt.Sprintf(`{"provider":%q,%q:{"apiKey":%q}}`,
					p.provider, p.bodyField, redactedSecretPlaceholder)
				req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/weather/test",
					strings.NewReader(body)).WithContext(cancelledContext(t))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()

				require.NoError(t, controller.TestWeatherConnection(e.NewContext(req, rec)))
				assertRestoredKeyOutcome(t, rec, saved)
			})
		}
	}
}

// assertRestoredKeyOutcome checks the result of sending the redacted placeholder
// to a test-connection handler: with no saved key the restored value is empty
// and validation fails with 400; with a saved key validation passes and the
// handler starts streaming with 200.
func assertRestoredKeyOutcome(t *testing.T, rec *httptest.ResponseRecorder, savedKey string) {
	t.Helper()
	if savedKey == "" {
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "API key is required",
			"placeholder restored to an empty key must fail the required-key validation")
		return
	}
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "API key is required",
		"placeholder restored to the saved key must pass the required-key validation")
}

// TestTestWeatherAuthentication_OpenWeatherSendsConfiguredKey verifies the
// OpenWeather authentication stage sends the configured API key as the appid
// query parameter to the configured endpoint.
func TestTestWeatherAuthentication_OpenWeatherSendsConfiguredKey(t *testing.T) {
	const apiKey = "real-openweather-key-123"

	gotAppID := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotAppID <- r.URL.Query().Get("appid"):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	_, controller := newIntegrationsTestHandler(t)
	testSettings := conf.CloneSettings(controller.CurrentSettings())
	testSettings.Realtime.Weather = conf.WeatherSettings{
		Provider: WeatherProviderOpenWeather,
		OpenWeather: conf.OpenWeatherSettings{
			APIKey:   apiKey,
			Endpoint: server.URL,
		},
	}

	msg, err := controller.testWeatherAuthentication(t.Context(), testSettings)
	require.NoError(t, err)
	assert.Contains(t, msg, "Successfully authenticated")
	select {
	case got := <-gotAppID:
		assert.Equal(t, apiKey, got)
	default:
		require.Fail(t, "the authentication stage sent no request")
	}
}

// TestTestEBirdConnection_RestoresRedactedAPIKey verifies the eBird
// test-connection handler restores the redacted API-key placeholder from the
// saved settings before validating the request. The request context is already
// cancelled, so a request that reaches the streaming stages stops at the first
// network stage without any HTTP request leaving the process.
func TestTestEBirdConnection_RestoresRedactedAPIKey(t *testing.T) {
	for _, saved := range []string{"", "real-ebird-key-456"} {
		t.Run(fmt.Sprintf("saved key %q", saved), func(t *testing.T) {
			e, controller := publishWeatherTestSettings(t, func(s *conf.Settings) {
				s.Realtime.EBird.APIKey = saved
			})

			body := fmt.Sprintf(`{"enabled":true,"apiKey":%q,"locale":"en"}`, redactedSecretPlaceholder)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/ebird/test",
				strings.NewReader(body)).WithContext(cancelledContext(t))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			require.NoError(t, controller.TestEBirdConnection(e.NewContext(req, rec)))
			assertRestoredKeyOutcome(t, rec, saved)
		})
	}
}

// TestSetStreamingHeaders verifies the shared streaming-response header helper
// sets the content type, no-cache/keep-alive, and X-Accel-Buffering: no so the
// integration connection-test streams are not buffered behind a reverse proxy.
func TestSetStreamingHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
	}{
		{"ndjson stream", mimeNDJSON},
		{"json stream", echo.MIMEApplicationJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/test", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			setStreamingHeaders(c, tt.contentType)

			assert.Equal(t, tt.contentType, rec.Header().Get(echo.HeaderContentType))
			assert.Equal(t, "no-cache", rec.Header().Get(echo.HeaderCacheControl))
			assert.Equal(t, "keep-alive", rec.Header().Get(echo.HeaderConnection))
			assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"),
				"streaming responses must set X-Accel-Buffering: no so reverse proxies do not buffer them")
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}
