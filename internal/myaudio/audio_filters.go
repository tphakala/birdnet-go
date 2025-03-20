package myaudio

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio/equalizer"
)

// Global variables for filter chain and mutex
var (
	filterChain *equalizer.FilterChain
	filterMutex sync.RWMutex
)

// InitializeFilterChain sets up the initial filter chain based on settings
func InitializeFilterChain(settings *conf.Settings) error {
	filterMutex.Lock()
	defer filterMutex.Unlock()

	// Create a new filter chain
	filterChain = equalizer.NewFilterChain()

	// If equalizer is enabled in settings, add filters
	if settings.Realtime.Audio.Equalizer.Enabled {
		for _, filterConfig := range settings.Realtime.Audio.Equalizer.Filters {
			// Create and add each filter
			filter, err := createFilter(filterConfig, float64(conf.SampleRate))
			if err != nil {
				return err
			}
			if filter != nil {
				if err := filterChain.AddFilter(filter); err != nil {
					return fmt.Errorf("failed to add audio EQ filter: %w", err)
				}
			}
		}
	}

	return nil
}

// UpdateFilterChain updates the filter chain based on new settings
func UpdateFilterChain(settings *conf.Settings) error {
	// Lock the mutex to ensure thread-safety
	filterMutex.Lock()
	defer filterMutex.Unlock()

	// Create a new filter chain
	newChain := equalizer.NewFilterChain()

	// If equalizer is enabled in settings, add filters
	if settings.Realtime.Audio.Equalizer.Enabled {
		// Iterate through each filter configuration
		for _, filterConfig := range settings.Realtime.Audio.Equalizer.Filters {
			// Create a new filter based on the configuration
			filter, err := createFilter(filterConfig, float64(conf.SampleRate))
			if err != nil {
				return fmt.Errorf("failed to create audio EQ filter: %w", err)
			}
			// If filter was successfully created, add it to the new chain
			if filter != nil {
				if err := newChain.AddFilter(filter); err != nil {
					return fmt.Errorf("failed to add audio EQ filter: %w", err)
				}
			}
		}
	}

	// Replace the old filter chain with the new one
	filterChain = newChain
	return nil
}

// createFilter creates a single filter based on the configuration
func createFilter(config conf.EqualizerFilter, sampleRate float64) (*equalizer.Filter, error) {
	// If passes is 0 or less, return nil without an error (filter is off)
	if config.Passes <= 0 {
		return nil, nil
	}

	// Create different types of filters based on the configuration
	switch config.Type {
	case "LowPass":
		return equalizer.NewLowPass(sampleRate, config.Frequency, config.Q, config.Passes)
	case "HighPass":
		return equalizer.NewHighPass(sampleRate, config.Frequency, config.Q, config.Passes)
	case "AllPass":
		return equalizer.NewAllPass(sampleRate, config.Frequency, config.Q, config.Passes)
	case "BandPass":
		return equalizer.NewBandPass(sampleRate, config.Frequency, config.Width, config.Passes)
	case "BandReject":
		return equalizer.NewBandReject(sampleRate, config.Frequency, config.Width, config.Passes)
	case "LowShelf":
		return equalizer.NewLowShelf(sampleRate, config.Frequency, config.Q, config.Gain, config.Passes)
	case "HighShelf":
		return equalizer.NewHighShelf(sampleRate, config.Frequency, config.Q, config.Gain, config.Passes)
	case "Peaking":
		return equalizer.NewPeaking(sampleRate, config.Frequency, config.Width, config.Gain, config.Passes)
	default:
		return nil, fmt.Errorf("unknown filter type: %s", config.Type)
	}
}

// ApplyFilters applies the current filter chain to a byte slice of audio samples
func ApplyFilters(samples []byte) error {
	if len(samples)%2 != 0 {
		return fmt.Errorf("invalid sample length: must be even")
	}

	filterMutex.RLock()
	defer filterMutex.RUnlock()

	// If no filters, return early
	if filterChain.Length() == 0 {
		return nil // No filters to apply
	}

	// Convert byte slice to float64 slice
	floatSamples := make([]float64, len(samples)/2)
	for i := 0; i < len(samples); i += 2 {
		floatSamples[i/2] = float64(int16(binary.LittleEndian.Uint16(samples[i:]))) / 32768.0
	}

	// Apply filters to the float samples in batch
	filterChain.ApplyBatch(floatSamples)

	// Convert back to byte slice
	for i, sample := range floatSamples {
		intSample := int16(sample * 32767.0)
		binary.LittleEndian.PutUint16(samples[i*2:], uint16(intSample))
	}

	return nil
}
