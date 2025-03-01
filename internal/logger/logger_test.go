package logger_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// testingWriter is a writer that writes to the testing.T log.
type testingWriter struct {
	t *testing.T
}

func (tw *testingWriter) Write(p []byte) (n int, err error) {
	tw.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

// testLogCapture allows capturing logs for testing
type testLogCapture struct {
	buf *bytes.Buffer
}

func newTestLogCapture() *testLogCapture {
	return &testLogCapture{
		buf: &bytes.Buffer{},
	}
}

func (c *testLogCapture) Write(p []byte) (n int, err error) {
	return c.buf.Write(p)
}

func (c *testLogCapture) String() string {
	return c.buf.String()
}

// setupTestFs sets up an Afero in-memory filesystem for testing
func setupTestFs() afero.Fs {
	return afero.NewMemMapFs()
}

// captureOutput redirects standard output to a buffer and returns a function to restore it
func captureOutput(t *testing.T) (*bytes.Buffer, func()) {
	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	go func() {
		_, err := io.Copy(&buf, r)
		assert.NoError(t, err)
	}()

	return &buf, func() {
		w.Close()
		os.Stdout = oldStdout
	}
}

// TestLogLevels tests that different log levels are properly handled
func TestLogLevels(t *testing.T) {
	// Define test cases for each level
	testCases := []struct {
		name          string
		configLevel   string
		logLevel      string
		logFunc       func(l *logger.Logger, msg string)
		shouldContain bool
	}{
		{
			name:          "Debug log in Debug level",
			configLevel:   "debug",
			logLevel:      "DEBUG",
			logFunc:       func(l *logger.Logger, msg string) { l.Debug(msg) },
			shouldContain: true,
		},
		{
			name:          "Info log in Debug level",
			configLevel:   "debug",
			logLevel:      "INFO",
			logFunc:       func(l *logger.Logger, msg string) { l.Info(msg) },
			shouldContain: true,
		},
		{
			name:          "Debug log in Info level",
			configLevel:   "info",
			logLevel:      "DEBUG",
			logFunc:       func(l *logger.Logger, msg string) { l.Debug(msg) },
			shouldContain: false,
		},
		{
			name:          "Info log in Info level",
			configLevel:   "info",
			logLevel:      "INFO",
			logFunc:       func(l *logger.Logger, msg string) { l.Info(msg) },
			shouldContain: true,
		},
		{
			name:          "Warn log in Info level",
			configLevel:   "info",
			logLevel:      "WARN",
			logFunc:       func(l *logger.Logger, msg string) { l.Warn(msg) },
			shouldContain: true,
		},
		{
			name:          "Error log in Warn level",
			configLevel:   "warn",
			logLevel:      "ERROR",
			logFunc:       func(l *logger.Logger, msg string) { l.Error(msg) },
			shouldContain: true,
		},
		{
			name:          "Warn log in Error level",
			configLevel:   "error",
			logLevel:      "WARN",
			logFunc:       func(l *logger.Logger, msg string) { l.Warn(msg) },
			shouldContain: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			buf := newTestLogCapture()

			// Create a logger with the specific level
			config := logger.Config{
				Level:        tc.configLevel,
				JSON:         false,
				Development:  false,
				FilePath:     "",
				DisableColor: true, // Disable color for easier testing
			}

			// Create a custom core to redirect output to our buffer
			core, err := logger.CreateTestCore(config, buf)
			require.NoError(t, err)

			log := logger.NewLoggerWithCore(core, config)

			// Act
			testMessage := "test message for " + tc.name
			tc.logFunc(log, testMessage)
			log.Sync()

			// Assert
			output := buf.String()
			if tc.shouldContain {
				assert.Contains(t, output, tc.logLevel, "Log should contain the level %s", tc.logLevel)
				assert.Contains(t, output, testMessage, "Log should contain the test message")
			} else {
				assert.NotContains(t, output, testMessage, "Log should not contain the test message")
			}
		})
	}
}

// TestLogFormatting tests that logs are properly formatted
func TestLogFormatting(t *testing.T) {
	testCases := []struct {
		name         string
		config       logger.Config
		expectedJSON bool
		fields       map[string]interface{}
	}{
		{
			name: "Console format",
			config: logger.Config{
				Level:        "debug",
				JSON:         false,
				Development:  false,
				DisableColor: true,
			},
			expectedJSON: false,
			fields: map[string]interface{}{
				"user_id": 12345,
				"action":  "login",
			},
		},
		{
			name: "JSON format",
			config: logger.Config{
				Level:        "debug",
				JSON:         true,
				Development:  false,
				DisableColor: true,
			},
			expectedJSON: true,
			fields: map[string]interface{}{
				"user_id": 12345,
				"action":  "login",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			buf := newTestLogCapture()

			// Create a custom core to redirect output to our buffer
			core, err := logger.CreateTestCore(tc.config, buf)
			require.NoError(t, err)

			log := logger.NewLoggerWithCore(core, tc.config)

			// Act - Log with fields
			message := "Test log message"
			var args []interface{}
			for k, v := range tc.fields {
				args = append(args, k, v)
			}
			log.Info(message, args...)
			log.Sync()

			// Assert
			output := buf.String()
			assert.Contains(t, output, message, "Log should contain the message")

			// Check format
			if tc.expectedJSON {
				assert.Contains(t, output, "\"msg\":\""+message+"\"", "JSON logs should format message in a JSON structure")

				// Verify we can parse it as JSON
				var jsonObj map[string]interface{}
				jsonBytes := []byte(output)
				err := json.Unmarshal(jsonBytes, &jsonObj)
				assert.NoError(t, err, "JSON log should be valid JSON")

				// Check fields (handle type conversion for numbers)
				for k, v := range tc.fields {
					assert.Contains(t, output, "\""+k+"\":", "JSON log should contain field "+k)
					jsonValue, exists := jsonObj[k]
					assert.True(t, exists, "Field %s should exist in JSON", k)

					// Compare values accounting for type conversion in JSON
					if intVal, ok := v.(int); ok {
						if floatVal, ok := jsonValue.(float64); ok {
							assert.Equal(t, float64(intVal), floatVal, "Field %s should have correct value", k)
						}
					} else {
						assert.Equal(t, v, jsonValue, "Field %s should have correct value", k)
					}
				}
			} else {
				// For console format, just check that message and fields are included somehow
				// This is more lenient since the exact format might vary
				for k, v := range tc.fields {
					assert.Contains(t, output, k, "Console log should contain field name "+k)
					assert.Contains(t, output, fmt.Sprintf("%v", v), "Console log should contain field value for "+k)
				}
			}
		})
	}
}

// TestFileOutput tests that logs are correctly written to files
func TestFileOutput(t *testing.T) {
	// Setup in-memory filesystem for testing
	fs := setupTestFs()

	// Create a temporary log file path
	logPath := "test.log"

	// Create config for file output
	config := logger.Config{
		Level:        "info",
		JSON:         false,
		Development:  false,
		FilePath:     logPath,
		DisableColor: true,
	}

	// Create logger with file output
	log, err := logger.NewLogger(config)
	require.NoError(t, err)

	// Write some log messages
	testMessage := "This is a test log message for file output"
	log.Info(testMessage, "test", "file_output")
	log.Sync()

	// Note: This is a stub since we can't connect the in-memory fs with the logger
	// The real file-based test is below
	_, err = afero.Exists(fs, logPath)
	require.NoError(t, err)

	// Instead, let's just test with a real file and clean up after
	tempDir, err := os.MkdirTemp("", "logger_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	realLogPath := filepath.Join(tempDir, "test.log")

	realConfig := logger.Config{
		Level:        "info",
		JSON:         false,
		Development:  false,
		FilePath:     realLogPath,
		DisableColor: true,
	}

	realLog, err := logger.NewLogger(realConfig)
	require.NoError(t, err)

	realLog.Info(testMessage, "test", "file_output")
	realLog.Sync()

	// Verify file exists and contains our message
	fileContent, err := os.ReadFile(realLogPath)
	require.NoError(t, err)

	contentStr := string(fileContent)
	assert.Contains(t, contentStr, testMessage, "Log file should contain the test message")
	assert.Contains(t, contentStr, "test", "Log file should contain the field name")
	assert.Contains(t, contentStr, "file_output", "Log file should contain the field value")
}

// TestRotation tests log file rotation functionality basics
func TestRotation(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "logger_rotation_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Set up log file path
	logPath := filepath.Join(tempDir, "rotation.log")

	// Create minimal rotation config
	rotationConfig := logger.RotationConfig{
		MaxSize:    1, // 1 MB - small enough to trigger rotation with test data
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   false,
	}

	// Create logger config
	config := logger.Config{
		Level:        "debug",
		JSON:         false,
		Development:  false,
		FilePath:     logPath,
		DisableColor: true,
	}

	// Create a logger with rotation
	log, err := logger.NewLogger(config, rotationConfig)
	require.NoError(t, err)

	// Write some log messages (not enough to trigger rotation)
	for i := 0; i < 100; i++ {
		log.Info("This is a test message for log rotation",
			"count", i,
			"data", strings.Repeat("a", 10), // Small amount of data
		)
	}
	log.Sync()

	// Verify that the main log file exists
	assert.FileExists(t, logPath, "Main log file should exist")

	// Skip checking for rotated files as it might be environment-dependent
	// and requires generating a lot of data
}

// TestLoggerWithContext tests logger context functionality
func TestLoggerWithContext(t *testing.T) {
	// Arrange
	buf := newTestLogCapture()

	config := logger.Config{
		Level:        "info",
		JSON:         true, // Use JSON for easier parsing
		Development:  false,
		DisableColor: true,
	}

	core, err := logger.CreateTestCore(config, buf)
	require.NoError(t, err)

	log := logger.NewLoggerWithCore(core, config)

	// Create logger with context fields
	contextFields := map[string]interface{}{
		"user_id":    12345,
		"request_id": "abc-123",
		"session_id": "xyz-789",
	}

	// Convert to args format
	var contextArgs []interface{}
	for k, v := range contextFields {
		contextArgs = append(contextArgs, k, v)
	}

	// Create logger with context
	contextLogger := log.With(contextArgs...)

	// Act - Log some messages without adding those fields again
	testMessage := "Test log with context"
	contextLogger.Info(testMessage)
	contextLogger.Sync()

	// Assert
	output := buf.String()

	// Parse JSON to verify fields
	var jsonObj map[string]interface{}
	err = json.Unmarshal([]byte(output), &jsonObj)
	require.NoError(t, err)

	// Verify message
	assert.Equal(t, testMessage, jsonObj["msg"])

	// Verify all context fields are present
	for k, expectedValue := range contextFields {
		actualValue, exists := jsonObj[k]
		assert.True(t, exists, "Field %s should exist in context logger output", k)

		// Handle type conversion for numbers in JSON
		if intVal, ok := expectedValue.(int); ok {
			if floatVal, ok := actualValue.(float64); ok {
				assert.Equal(t, float64(intVal), floatVal, "Field %s should have the correct value", k)
				continue
			}
		}

		assert.Equal(t, expectedValue, actualValue, "Field %s should have the correct value", k)
	}
}

// TestLoggerNaming tests logger naming functionality
func TestLoggerNaming(t *testing.T) {
	// Arrange
	buf := newTestLogCapture()

	config := logger.Config{
		Level:        "info",
		JSON:         true, // Use JSON for easier parsing
		Development:  false,
		DisableColor: true,
	}

	core, err := logger.CreateTestCore(config, buf)
	require.NoError(t, err)

	log := logger.NewLoggerWithCore(core, config)

	// Create a named logger
	loggerName := "test_component"
	namedLogger := log.Named(loggerName)

	// Act
	testMessage := "Test named logger"
	namedLogger.Info(testMessage)
	namedLogger.Sync()

	// Assert
	output := buf.String()

	// Parse JSON to verify logger name
	var jsonObj map[string]interface{}
	err = json.Unmarshal([]byte(output), &jsonObj)
	require.NoError(t, err)

	// Verify message
	assert.Equal(t, testMessage, jsonObj["msg"])

	// Verify logger name is present
	logger, exists := jsonObj["logger"]
	assert.True(t, exists, "Logger name field should exist")
	assert.Equal(t, loggerName, logger, "Logger name should be correctly set")
}

// TestGlobalLogger tests the global logger functionality
func TestGlobalLogger(t *testing.T) {
	// We need to redirect stdout before initializing the global logger
	stdoutFile := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Initialize global logger with custom config after redirecting stdout
	config := logger.Config{
		Level:        "info",
		JSON:         true,
		Development:  false,
		DisableColor: true,
	}

	err := logger.InitGlobal(config)
	require.NoError(t, err)

	// Use global logger functions
	testMessage := "Test global logger"
	logger.Info(testMessage, "test", "global")
	logger.Sync()

	// Close the writer to flush output and restore stdout
	w.Close()
	os.Stdout = stdoutFile

	// Read the output from the pipe
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	output := buf.String()

	// Check that output contains our message
	assert.Contains(t, output, testMessage, "Global logger output should contain the test message")
}
