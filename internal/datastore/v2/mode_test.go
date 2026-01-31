package v2

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsEnhancedDatabase_DefaultFalse(t *testing.T) {
	ResetDatabaseMode()
	assert.False(t, IsEnhancedDatabase(), "should default to false")
}

func TestSetEnhancedDatabaseMode(t *testing.T) {
	ResetDatabaseMode()

	SetEnhancedDatabaseMode()
	assert.True(t, IsEnhancedDatabase(), "should be true after setting")
}

func TestResetDatabaseMode(t *testing.T) {
	SetEnhancedDatabaseMode()
	assert.True(t, IsEnhancedDatabase(), "should be true after setting")

	ResetDatabaseMode()
	assert.False(t, IsEnhancedDatabase(), "should be false after reset")
}

func TestIsEnhancedDatabase_ThreadSafe(t *testing.T) {
	ResetDatabaseMode()

	var wg sync.WaitGroup

	// Set mode in one goroutine
	wg.Go(func() {
		SetEnhancedDatabaseMode()
	})

	// Read mode from multiple goroutines
	for range 100 {
		wg.Go(func() {
			_ = IsEnhancedDatabase() // Should not panic
		})
	}

	wg.Wait()
	assert.True(t, IsEnhancedDatabase())
}
