package myaudio

import (
	"encoding/binary"
	"math"
	"sync"
	"time"
)

// AudioLevelData holds audio level data
type AudioLevelData struct {
	Level    int    `json:"level"`    // 0-100
	Clipping bool   `json:"clipping"` // true if clipping is detected
	Source   string `json:"source"`   // Source identifier (e.g., "malgo" for device, or RTSP URL)
	Name     string `json:"name"`     // Human-readable name of the source
}

// Global variables to track throttling state
var (
	audioLevelThrottleMu  sync.Mutex
	audioLevelThrottleMap = make(map[string]*audioLevelThrottleState)
	defaultSampleInterval = 50 * time.Millisecond
	maxSampleInterval     = 2 * time.Second
)

type audioLevelThrottleState struct {
	lastSent        time.Time
	sampleInterval  time.Duration
	consecutiveFull int // Count consecutive times the channel was full
}

// CalculateAudioLevel calculates the RMS (Root Mean Square) of the audio samples
// and returns an AudioLevelData struct with the level and clipping status
func CalculateAudioLevel(samples []byte, source, name string) AudioLevelData {
	// If there are no samples, return zero level and no clipping
	if len(samples) == 0 {
		return AudioLevelData{Level: 0, Clipping: false, Source: source, Name: name}
	}

	// Ensure we have an even number of bytes (16-bit samples)
	if len(samples)%2 != 0 {
		// Truncate to even number of bytes
		samples = samples[:len(samples)-1]
	}

	var sum float64
	sampleCount := len(samples) / 2 // 2 bytes per sample for 16-bit audio
	isClipping := false
	maxSample := float64(0)

	// Iterate through samples, calculating sum of squares and checking for clipping
	for i := 0; i < len(samples); i += 2 {
		if i+1 >= len(samples) {
			break
		}

		// Convert two bytes to a 16-bit sample
		sample := int16(binary.LittleEndian.Uint16(samples[i : i+2]))
		sampleAbs := math.Abs(float64(sample))
		sum += sampleAbs * sampleAbs

		// Keep track of the maximum sample value
		if sampleAbs > maxSample {
			maxSample = sampleAbs
		}

		// Check for clipping (maximum positive or negative 16-bit value)
		if sample == 32767 || sample == -32768 {
			isClipping = true
		}
	}

	// If we ended up with no samples, return zero level and no clipping
	if sampleCount == 0 {
		return AudioLevelData{Level: 0, Clipping: false, Source: source, Name: name}
	}

	// Calculate Root Mean Square (RMS)
	rms := math.Sqrt(sum / float64(sampleCount))

	// Convert RMS to decibels
	// 32768 is max value for 16-bit audio
	db := 20 * math.Log10(rms/32768.0)

	// Scale decibels to 0-100 range
	// Adjust the range to make it more sensitive
	scaledLevel := (db + 60) * (100.0 / 50.0)

	// If the audio is clipping, ensure the level is at or near 100
	if isClipping {
		scaledLevel = math.Max(scaledLevel, 95)
	}

	// Clamp the value between 0 and 100
	if scaledLevel < 0 {
		scaledLevel = 0
	} else if scaledLevel > 100 {
		scaledLevel = 100
	}

	// Return the calculated audio level data
	return AudioLevelData{
		Level:    int(scaledLevel),
		Clipping: isClipping,
		Source:   source,
		Name:     name,
	}
}

// ShouldCalculateAudioLevel determines whether to calculate audio level
// based on channel capacity and adaptive sampling
func ShouldCalculateAudioLevel(ch chan AudioLevelData, sourceID string) bool {
	// Use sourceID as part of the key for better identification
	id := sourceID

	audioLevelThrottleMu.Lock()
	defer audioLevelThrottleMu.Unlock()

	// Get or create throttle state for this channel
	state, ok := audioLevelThrottleMap[id]
	if !ok {
		state = &audioLevelThrottleState{
			lastSent:        time.Now(),
			sampleInterval:  defaultSampleInterval,
			consecutiveFull: 0,
		}
		audioLevelThrottleMap[id] = state
	}

	now := time.Now()

	// Check if we should sample based on interval
	if now.Sub(state.lastSent) < state.sampleInterval {
		return false
	}

	// Update last sent time
	state.lastSent = now

	// Adjust sampling rate based on channel capacity
	channelCapacity := cap(ch)
	channelUsed := len(ch)

	// Calculate channel fullness as a percentage
	channelFullness := float64(channelUsed) / float64(channelCapacity)

	// Use channel fullness to dynamically adjust sampling rate
	switch {
	case channelFullness >= 0.5: // Channel is at least 50% full
		// Channel is getting full, increase interval (back off)
		state.consecutiveFull++

		// Exponential backoff with a cap
		if state.consecutiveFull > 10 {
			state.sampleInterval = maxSampleInterval
		} else {
			state.sampleInterval *= 2
			if state.sampleInterval > maxSampleInterval {
				state.sampleInterval = maxSampleInterval
			}
		}

		// If channel is almost full (>=90%), slow down even more aggressively
		if channelFullness >= 0.9 {
			// For near-full channels, add an additional delay
			state.sampleInterval = maxSampleInterval

			// Only sample occasionally when channel is very full
			// This ensures we don't completely stop sampling
			return state.consecutiveFull%10 == 0
		}
	case channelFullness < 0.25 && state.sampleInterval > defaultSampleInterval:
		// Channel has capacity, decrease interval gradually (speed up)
		state.consecutiveFull = 0
		state.sampleInterval /= 2
		if state.sampleInterval < defaultSampleInterval {
			state.sampleInterval = defaultSampleInterval
		}
	case channelFullness == 0:
		// Channel is empty - this means data is being consumed quickly
		// Reset to default interval for optimal performance
		state.consecutiveFull = 0
		state.sampleInterval = defaultSampleInterval
	}

	return true
}

// CleanupAudioLevelTrackers cleans up stale audio level tracking data
// This should be called periodically to prevent memory leaks
func CleanupAudioLevelTrackers() {
	// Cleanup both throttle map and consumption trackers
	CleanupAudioLevelThrottleMap()
	GlobalCleanupManager.CleanupTrackers()
}

// CleanupAudioLevelThrottleMap removes stale entries from the throttle map
// It should be called periodically to prevent memory leaks
func CleanupAudioLevelThrottleMap() {
	audioLevelThrottleMu.Lock()
	defer audioLevelThrottleMu.Unlock()

	// Remove entries that haven't been updated in the last 5 minutes
	cutoff := time.Now().Add(-5 * time.Minute)
	for id, state := range audioLevelThrottleMap {
		if state.lastSent.Before(cutoff) {
			delete(audioLevelThrottleMap, id)
		}
	}
}

// SendAudioLevel sends audio level data to the channel with appropriate throttling
// Returns true if the data was sent, false otherwise
func SendAudioLevel(audioLevelChan chan AudioLevelData, data AudioLevelData) bool {
	// Try to send level to channel (non-blocking)
	select {
	case audioLevelChan <- data:
		// Successfully sent data
		return true
	default:
		// Channel is full - silently drop the data
		return false
	}
}

// ProcessAudioLevel handles the entire audio level processing logic:
// 1. Checks if audio level should be calculated
// 2. Calculates the audio level if needed
// 3. Sends the data to the channel
//
// This centralizes the audio level processing logic that was previously duplicated
// across different audio source handlers.
func ProcessAudioLevel(samples []byte, sourceID, sourceName string, audioLevelChan chan AudioLevelData) {
	// Check if we should calculate audio level based on channel capacity and consumer status
	if ShouldCalculateAudioLevel(audioLevelChan, sourceID) {
		// Calculate audio level with source information
		audioLevelData := CalculateAudioLevel(samples, sourceID, sourceName)

		// Send level to channel (non-blocking)
		SendAudioLevel(audioLevelChan, audioLevelData)
	}
}
