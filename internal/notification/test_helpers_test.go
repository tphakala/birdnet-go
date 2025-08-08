package notification

import "time"

// Test helper methods for InMemoryStore - only included in test builds
// These methods provide access to internal state for testing purposes
// and should not be available in production binaries.

// forceHashIndexEntry adds an entry to the hash index (test helper)
func (s *InMemoryStore) forceHashIndexEntry(hash string, notif *Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashIndex[hash] = notif
}

// getHashIndexCount returns the number of entries in hash index (test helper)
func (s *InMemoryStore) getHashIndexCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.hashIndex)
}

// hasHashIndexEntry checks if a hash exists in the index (test helper)
func (s *InMemoryStore) hasHashIndexEntry(hash string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.hashIndex[hash]
	return exists
}

// forceCleanupTrigger sets lastCleanup to trigger cleanup on next Save (test helper)
func (s *InMemoryStore) forceCleanupTrigger() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCleanup = time.Now().Add(-2 * time.Hour)
}