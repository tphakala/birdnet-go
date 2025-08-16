// source_registry_test.go - Unit tests for audio source registry
package myaudio

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	
	"github.com/tphakala/birdnet-go/internal/logging"
)

func getTestLogger() *slog.Logger {
	logger := logging.ForService("test")
	if logger == nil {
		logger = slog.Default()
	}
	return logger
}

func TestSourceRegistration(t *testing.T) {
	// Create a fresh registry for testing
	registry := &AudioSourceRegistry{
		sources:           make(map[string]*AudioSource),
		connectionMap:     make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}

	// Test RTSP source registration
	rtspURL := "rtsp://admin:password@192.168.1.100/stream"
	config := SourceConfig{
		ID:          "test_cam",
		DisplayName: "Test Camera",
		Type:        SourceTypeRTSP,
	}

	source, err := registry.RegisterSource(rtspURL, config)
	if err != nil {
		t.Fatalf("Failed to register RTSP source: %v", err)
	}

	// Verify source properties
	if source.ID != "test_cam" {
		t.Errorf("Expected ID 'test_cam', got '%s'", source.ID)
	}
	if source.DisplayName != "Test Camera" {
		t.Errorf("Expected display name 'Test Camera', got '%s'", source.DisplayName)
	}
	if source.Type != SourceTypeRTSP {
		t.Errorf("Expected type RTSP, got %s", source.Type)
	}
	connStr, err := source.GetConnectionString()
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}
	if connStr != rtspURL {
		t.Errorf("Connection string mismatch")
	}

	// Verify safe string doesn't contain credentials
	if strings.Contains(source.SafeString, "password") {
		t.Errorf("Safe string contains credentials: %s", source.SafeString)
	}
}

func TestRTSPCredentialSanitization(t *testing.T) {
	registry := GetRegistry()

	testCases := []struct {
		name        string
		input       string
		shouldHide  bool
	}{
		{
			name:       "RTSP with credentials",
			input:      "rtsp://admin:secret123@192.168.1.100/stream",
			shouldHide: true,
		},
		{
			name:       "RTSP without credentials",
			input:      "rtsp://192.168.1.100/stream",
			shouldHide: false,
		},
		{
			name:       "Audio device",
			input:      "hw:1,0",
			shouldHide: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Detect the actual source type
			sourceType := SourceTypeRTSP
			if strings.HasPrefix(tc.input, "hw:") {
				sourceType = SourceTypeAudioCard
			}
			source := registry.GetOrCreateSource(tc.input, sourceType)
			if source == nil {
				t.Fatalf("Failed to create source for %s", tc.input)
			}

			if tc.shouldHide {
				// Should not contain credentials in safe string
				if strings.Contains(source.SafeString, "secret123") ||
				   strings.Contains(source.SafeString, "admin:secret123") {
					t.Errorf("Safe string contains credentials: %s", source.SafeString)
				}
			}

			// Original connection string should always be preserved
			connStr, err := source.GetConnectionString()
			if err != nil {
				t.Fatalf("Failed to get connection string: %v", err)
			}
			if connStr != tc.input {
				t.Errorf("Original connection string not preserved")
			}
		})
	}
}

func TestSourceIDGeneration(t *testing.T) {
	registry := &AudioSourceRegistry{
		sources:           make(map[string]*AudioSource),
		connectionMap:     make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}

	// Test auto-generated IDs
	source1 := registry.GetOrCreateSource("rtsp://cam1.local/stream", SourceTypeRTSP)
	source2 := registry.GetOrCreateSource("rtsp://cam2.local/stream", SourceTypeRTSP)

	if source1.ID == source2.ID {
		t.Errorf("Generated IDs should be unique: %s == %s", source1.ID, source2.ID)
	}

	// IDs should follow the pattern
	if !strings.HasPrefix(source1.ID, "rtsp_") {
		t.Errorf("RTSP source ID should start with 'rtsp_': %s", source1.ID)
	}
}

func TestConcurrentSourceAccess(t *testing.T) {
	registry := GetRegistry()

	// Test concurrent registration
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func(id int) {
			source := registry.GetOrCreateSource(
				fmt.Sprintf("rtsp://cam%d.local/stream", id),
				SourceTypeRTSP,
			)
			if source == nil {
				t.Errorf("Failed to create source %d", id)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify we have 10 sources
	sources := registry.ListSources()
	if len(sources) < 10 {
		t.Errorf("Expected at least 10 sources, got %d", len(sources))
	}
}

func TestBackwardCompatibility(t *testing.T) {
	// Test that GetOrCreateSource works correctly
	testURL := "rtsp://test.local/stream"
	
	// This should auto-register the source
	source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	if source == nil {
		t.Fatal("GetOrCreateSource returned nil")
	}
	
	// Should return a source with an ID, not the original URL
	if source.ID == testURL {
		t.Errorf("Source should have generated ID, not original URL")
	}

	// Second call should return the same source
	source2 := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	if source2 == nil || source.ID != source2.ID {
		t.Errorf("GetOrCreateSource should be idempotent: %s != %s", source.ID, source2.ID)
	}
}

func TestSourceMetricsUpdate(t *testing.T) {
	registry := GetRegistry()
	
	source := registry.GetOrCreateSource("rtsp://metrics.test/stream", SourceTypeRTSP)
	initialBytes := source.TotalBytes
	initialErrors := source.ErrorCount

	// Update metrics
	registry.UpdateSourceMetrics(source.ID, 1024, false)
	registry.UpdateSourceMetrics(source.ID, 2048, true)

	// Verify updates
	updatedSource, _ := registry.GetSourceByID(source.ID)
	if updatedSource.TotalBytes != initialBytes+1024+2048 {
		t.Errorf("Expected total bytes %d, got %d", 
			initialBytes+1024+2048, updatedSource.TotalBytes)
	}
	if updatedSource.ErrorCount != initialErrors+1 {
		t.Errorf("Expected error count %d, got %d", 
			initialErrors+1, updatedSource.ErrorCount)
	}
}

func TestSourceStats(t *testing.T) {
	registry := &AudioSourceRegistry{
		sources:           make(map[string]*AudioSource),
		connectionMap:     make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}

	// Create sources of different types
	registry.GetOrCreateSource("rtsp://cam1.local/stream", SourceTypeRTSP)
	registry.GetOrCreateSource("rtsp://cam2.local/stream", SourceTypeRTSP)
	registry.GetOrCreateSource("hw:1,0", SourceTypeAudioCard)

	stats := registry.GetSourceStats()

	if stats.Total != 3 {
		t.Errorf("Expected 3 total sources, got %v", stats.Total)
	}
	if stats.RTSP != 2 {
		t.Errorf("Expected 2 RTSP sources, got %v", stats.RTSP)
	}
	if stats.Device != 1 {
		t.Errorf("Expected 1 device source, got %v", stats.Device)
	}
}