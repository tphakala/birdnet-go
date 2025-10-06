package notification

import (
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter to prevent overwhelming external services.
// This provides additional DoS protection beyond circuit breakers.
type PushRateLimiter struct {
	rate       int           // tokens per interval
	interval   time.Duration // time window for rate
	tokens     int           // current available tokens
	maxTokens  int           // maximum tokens (burst capacity)
	lastRefill time.Time     // last time tokens were refilled
	mu         sync.Mutex
}

// PushRateLimiterConfig holds configuration for rate limiting.
type PushRateLimiterConfig struct {
	// RequestsPerMinute limits how many requests can be made per minute
	RequestsPerMinute int
	// BurstSize allows bursts up to this many requests
	BurstSize int
}

// DefaultPushRateLimiterConfig returns safe default rate limiting configuration.
// These defaults prevent overwhelming external APIs while allowing reasonable notification rates.
func DefaultPushRateLimiterConfig() PushRateLimiterConfig {
	return PushRateLimiterConfig{
		RequestsPerMinute: 60,  // 1 request per second average
		BurstSize:         10,  // Allow bursts of up to 10 requests
	}
}

// NewPushRateLimiter creates a new token bucket rate limiter.
func NewPushRateLimiter(config PushRateLimiterConfig) *PushRateLimiter {
	if config.RequestsPerMinute <= 0 {
		config.RequestsPerMinute = 60
	}
	if config.BurstSize <= 0 {
		config.BurstSize = 10
	}

	return &PushRateLimiter{
		rate:       config.RequestsPerMinute,
		interval:   time.Minute,
		tokens:     config.BurstSize,
		maxTokens:  config.BurstSize,
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed under the rate limit.
// Returns true if request is allowed, false if rate limit exceeded.
func (rl *PushRateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Refill tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)
	if elapsed >= rl.interval {
		// Full refill if a full interval has passed
		periods := int(elapsed / rl.interval)
		tokensToAdd := periods * rl.rate
		rl.tokens = minInt(rl.maxTokens, rl.tokens+tokensToAdd)
		rl.lastRefill = now
	} else {
		// Partial refill based on elapsed time
		tokensToAdd := int(float64(rl.rate) * (elapsed.Seconds() / rl.interval.Seconds()))
		if tokensToAdd > 0 {
			rl.tokens = minInt(rl.maxTokens, rl.tokens+tokensToAdd)
			rl.lastRefill = now
		}
	}

	// Check if we have tokens available
	if rl.tokens > 0 {
		rl.tokens--
		return true
	}

	return false
}

// WaitUntilAllowed blocks until a request is allowed.
// Returns immediately if request is allowed, otherwise waits.
func (rl *PushRateLimiter) WaitUntilAllowed() {
	for !rl.Allow() {
		// Calculate wait time
		rl.mu.Lock()
		timeUntilNextToken := rl.interval / time.Duration(rl.rate)
		rl.mu.Unlock()

		time.Sleep(timeUntilNextToken)
	}
}

// GetStats returns current rate limiter statistics.
func (rl *PushRateLimiter) GetStats() PushRateLimiterStats {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	return PushRateLimiterStats{
		AvailableTokens: rl.tokens,
		MaxTokens:       rl.maxTokens,
		Rate:            rl.rate,
		Interval:        rl.interval,
		LastRefill:      rl.lastRefill,
	}
}

// PushRateLimiterStats contains statistics about the rate limiter.
type PushRateLimiterStats struct {
	AvailableTokens int
	MaxTokens       int
	Rate            int
	Interval        time.Duration
	LastRefill      time.Time
}

// min returns the minimum of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
