package suncalc

import (
	"log"
	"sync"
	"time"

	tzf "github.com/ringsaturn/tzf"
)

var (
	tzFinder  tzf.F
	tzOnce    sync.Once
	errTZInit error
)

// initTZFinder initializes the singleton timezone finder.
// Uses sync.Once to ensure the expensive polygon data is loaded only once.
func initTZFinder() {
	tzOnce.Do(func() {
		tzFinder, errTZInit = tzf.NewDefaultFinder()
	})
}

// resolveTimezone returns the IANA timezone for the given coordinates.
// Falls back to time.Local if lookup fails, with a warning log.
func resolveTimezone(latitude, longitude float64) *time.Location {
	initTZFinder()
	if errTZInit != nil {
		log.Printf("suncalc: timezone finder init failed, falling back to system timezone: %v", errTZInit)
		return time.Local
	}
	// tzf API takes (longitude, latitude) -- longitude first
	tzName := tzFinder.GetTimezoneName(longitude, latitude)
	if tzName == "" {
		log.Printf("suncalc: no timezone found for coordinates (%.4f, %.4f), falling back to system timezone", latitude, longitude)
		return time.Local
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Printf("suncalc: failed to load timezone %q for coordinates (%.4f, %.4f), falling back to system timezone: %v",
			tzName, latitude, longitude, err)
		return time.Local
	}
	return loc
}
