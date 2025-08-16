// source_migration.go - Auto-migration and backward compatibility layer
package myaudio

import (
	"fmt"
	"log"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// MigrateExistingSourceToID converts a legacy source identifier to a registry source ID
// This ensures backward compatibility for existing code that uses connection strings directly
// This function is thread-safe and prevents race conditions during concurrent migrations
func MigrateExistingSourceToID(source string) string {
	registry := GetRegistry()
	
	// Pass SourceTypeUnknown to indicate type detection should happen atomically inside the lock
	// This prevents race conditions where multiple goroutines might detect different types
	return registry.MigrateSourceAtomic(source, SourceTypeUnknown)
}

// Note: detectSourceType was moved to source_registry.go as detectSourceTypeFromString
// to be used inside the atomic migration operation for thread safety

// EnableMigrationLayer enables automatic migration for buffer operations
// This should be called during application startup to ensure backward compatibility
func EnableMigrationLayer() {
	log.Printf("✅ Audio source migration layer available")
	log.Printf("💡 Buffer functions will auto-migrate source identifiers")
}

// RegisterExistingRTSPSources registers RTSP sources from configuration
// This should be called during application startup to register known sources
func RegisterExistingRTSPSources(rtspURLs []string) {
	if len(rtspURLs) == 0 {
		return
	}
	
	registry := GetRegistry()
	var errors []string
	
	for i, url := range rtspURLs {
		if url == "" {
			log.Printf("⚠️ Skipping empty RTSP URL at index %d", i)
			continue
		}
		
		config := SourceConfig{
			ID:          fmt.Sprintf("rtsp_%03d", i+1),
			DisplayName: "", // Let auto-generation use SafeString
			Type:        SourceTypeRTSP,
		}
		
		if _, err := registry.RegisterSource(url, config); err != nil {
			safeURL := privacy.SanitizeRTSPUrl(url)
			errMsg := fmt.Sprintf("source %d (%s): %v", i+1, safeURL, err)
			errors = append(errors, errMsg)
			log.Printf("❌ Failed to register RTSP %s", errMsg)
		}
	}
	
	if len(errors) > 0 {
		log.Printf("⚠️ Failed to register %d out of %d RTSP sources", len(errors), len(rtspURLs))
	}
}