package diskmanager

import "testing"

// withTestPoolConfig sets up a test pool configuration and restores the original on cleanup.
func withTestPoolConfig(t *testing.T, cfg *PoolConfig) {
	t.Helper()
	original := loadPoolConfig()
	poolConfig.Store(cfg)
	t.Cleanup(func() {
		poolConfig.Store(original)
	})
}

// smallPoolConfig returns a PoolConfig with small max capacity for testing oversized paths.
func smallPoolConfig() *PoolConfig {
	return &PoolConfig{
		InitialCapacity: 10,
		MaxPoolCapacity: 100,
		MaxParseErrors:  100,
	}
}

// normalPoolConfig returns a PoolConfig with reasonable capacity for normal pooling tests.
func normalPoolConfig() *PoolConfig {
	return &PoolConfig{
		InitialCapacity: 10,
		MaxPoolCapacity: 1000,
		MaxParseErrors:  100,
	}
}
