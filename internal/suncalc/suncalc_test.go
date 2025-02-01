package suncalc

import (
	"testing"
	"time"
)

func TestNewSunCalc(t *testing.T) {
	latitude, longitude := 60.1699, 24.9384 // Helsinki coordinates
	sc := NewSunCalc(latitude, longitude)
	if sc == nil {
		t.Fatal("NewSunCalc returned nil")
		return // Explicitly return to make it clear no further checks happen
	}

	// Now safe to access sc.observer since we've confirmed sc is not nil
	if sc.observer.Latitude != latitude {
		t.Errorf("Expected latitude %v, got %v", latitude, sc.observer.Latitude)
	}

	if sc.observer.Longitude != longitude {
		t.Errorf("Expected longitude %v, got %v", longitude, sc.observer.Longitude)
	}
}

func TestGetSunEventTimes(t *testing.T) {
	// Helsinki coordinates
	sc := NewSunCalc(60.1699, 24.9384)

	// Test date (midsummer in Helsinki)
	date := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)

	// First call to calculate and cache
	times1, err := sc.GetSunEventTimes(date)
	if err != nil {
		t.Fatalf("Failed to get sun event times: %v", err)
	}

	// Verify times are not zero
	if times1.Sunrise.IsZero() {
		t.Error("Sunrise time is zero")
	}
	if times1.Sunset.IsZero() {
		t.Error("Sunset time is zero")
	}
	if times1.CivilDawn.IsZero() {
		t.Error("Civil dawn time is zero")
	}
	if times1.CivilDusk.IsZero() {
		t.Error("Civil dusk time is zero")
	}

	// Second call to test cache
	times2, err := sc.GetSunEventTimes(date)
	if err != nil {
		t.Fatalf("Failed to get cached sun event times: %v", err)
	}

	// Verify cached times match original times
	if !times1.Sunrise.Equal(times2.Sunrise) {
		t.Error("Cached sunrise time doesn't match original")
	}
	if !times1.Sunset.Equal(times2.Sunset) {
		t.Error("Cached sunset time doesn't match original")
	}
}

func TestGetSunriseTime(t *testing.T) {
	sc := NewSunCalc(60.1699, 24.9384)
	date := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)

	sunrise, err := sc.GetSunriseTime(date)
	if err != nil {
		t.Fatalf("Failed to get sunrise time: %v", err)
	}

	if sunrise.IsZero() {
		t.Error("Sunrise time is zero")
	}
}

func TestGetSunsetTime(t *testing.T) {
	sc := NewSunCalc(60.1699, 24.9384)
	date := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)

	sunset, err := sc.GetSunsetTime(date)
	if err != nil {
		t.Fatalf("Failed to get sunset time: %v", err)
	}

	if sunset.IsZero() {
		t.Error("Sunset time is zero")
	}
}

func TestCacheConsistency(t *testing.T) {
	sc := NewSunCalc(60.1699, 24.9384)
	date := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)

	// Get times twice
	times1, err := sc.GetSunEventTimes(date)
	if err != nil {
		t.Fatalf("Failed to get initial sun event times: %v", err)
	}

	// Verify cache entry exists
	dateKey := date.Format("2006-01-02")
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	sc.lock.RUnlock()

	if !exists {
		t.Error("Cache entry not found after calculation")
	}

	if !entry.date.Equal(date) {
		t.Error("Cached date doesn't match requested date")
	}

	if !entry.times.Sunrise.Equal(times1.Sunrise) {
		t.Error("Cached sunrise time doesn't match calculated time")
	}
}
