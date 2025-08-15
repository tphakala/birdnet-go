// source_registry.go - Core audio source registry implementation
package myaudio

import (
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// AudioSourceRegistry manages all audio sources in the system
type AudioSourceRegistry struct {
	// Core storage
	sources       map[string]*AudioSource // ID -> AudioSource
	connectionMap map[string]string       // connectionString -> ID (for fast lookups)

	// Thread safety
	mu sync.RWMutex

	// ID generation
	idCounter int
}

var (
	registry     *AudioSourceRegistry
	registryOnce sync.Once
)

// GetRegistry returns the singleton registry instance
func GetRegistry() *AudioSourceRegistry {
	registryOnce.Do(func() {
		registry = &AudioSourceRegistry{
			sources:       make(map[string]*AudioSource),
			connectionMap: make(map[string]string),
			idCounter:     0,
		}
	})
	return registry
}

// RegisterSource registers a new audio source or updates existing one
func (r *AudioSourceRegistry) RegisterSource(connectionString string, config SourceConfig) (*AudioSource, error) {
	// Validate connection string before acquiring lock
	if err := r.validateConnectionString(connectionString, config.Type); err != nil {
		return nil, fmt.Errorf("invalid connection string: %w", err)
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if source already exists
	if existingID, exists := r.connectionMap[connectionString]; exists {
		source := r.sources[existingID]
		// Update metadata if provided
		if config.DisplayName != "" {
			source.DisplayName = config.DisplayName
		}
		source.LastSeen = time.Now()
		source.IsActive = true
		return source, nil
	}

	// Create new source
	source := &AudioSource{
		ID:               config.ID,
		DisplayName:      config.DisplayName,
		Type:             config.Type,
		connectionString: connectionString,
		SafeString:       r.sanitizeConnectionString(connectionString, config.Type),
		RegisteredAt:     time.Now(),
		LastSeen:         time.Now(),
		IsActive:         true,
		TotalBytes:       0,
		ErrorCount:       0,
	}

	// Auto-generate ID if not provided
	if source.ID == "" {
		source.ID = r.generateID(config.Type)
	}

	// Auto-generate display name if not provided
	if source.DisplayName == "" {
		source.DisplayName = r.generateDisplayName(source)
	}

	// Store in registry
	r.sources[source.ID] = source
	r.connectionMap[connectionString] = source.ID

	log.Printf("📝 Registered audio source: %s (%s) -> ID: %s", source.DisplayName, source.SafeString, source.ID)

	return source, nil
}

// GetSourceByID retrieves a source by its ID
func (r *AudioSourceRegistry) GetSourceByID(id string) (*AudioSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	source, exists := r.sources[id]
	return source, exists
}

// GetSourceByConnection retrieves a source by its connection string
func (r *AudioSourceRegistry) GetSourceByConnection(connectionString string) (*AudioSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if id, exists := r.connectionMap[connectionString]; exists {
		return r.sources[id], true
	}
	return nil, false
}

// GetOrCreateSource ensures a source exists and returns it
func (r *AudioSourceRegistry) GetOrCreateSource(connectionString string, sourceType SourceType) *AudioSource {
	// For GetOrCreateSource, we auto-detect type if it seems wrong
	actualType := sourceType
	if sourceType == SourceTypeRTSP && !strings.HasPrefix(connectionString, "rtsp") {
		// Auto-detect the actual type
		actualType = detectSourceTypeFromString(connectionString)
	}
	
	source, err := r.RegisterSource(connectionString, SourceConfig{
		Type: actualType,
	})
	if err != nil {
		log.Printf("❌ Failed to register source: %v", err)
		return nil
	}
	return source
}

// detectSourceTypeFromString determines source type from connection string
func detectSourceTypeFromString(connectionString string) SourceType {
	// RTSP URLs
	if strings.HasPrefix(connectionString, "rtsp://") || strings.HasPrefix(connectionString, "rtsps://") {
		return SourceTypeRTSP
	}
	
	// Audio device patterns
	if strings.HasPrefix(connectionString, "hw:") || 
	   strings.HasPrefix(connectionString, "plughw:") ||
	   strings.Contains(connectionString, "alsa") ||
	   strings.Contains(connectionString, "pulse") ||
	   connectionString == "default" {
		return SourceTypeAudioCard
	}
	
	// File patterns (check for common audio extensions)
	if strings.Contains(connectionString, ".wav") ||
	   strings.Contains(connectionString, ".mp3") ||
	   strings.Contains(connectionString, ".flac") ||
	   strings.Contains(connectionString, ".m4a") ||
	   strings.Contains(connectionString, ".ogg") {
		return SourceTypeFile
	}
	
	// Default to audio card for unknown patterns
	return SourceTypeAudioCard
}

// MigrateSourceAtomic atomically migrates a source identifier to a registry ID
// This prevents race conditions during concurrent migration attempts
func (r *AudioSourceRegistry) MigrateSourceAtomic(source string, sourceType SourceType) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if it's already a source ID (avoid double migration)
	if _, exists := r.sources[source]; exists {
		return source
	}

	// Check if we have this connection string registered
	if existingID, exists := r.connectionMap[source]; exists {
		// Already registered, trust it (was validated on initial registration)
		return existingID
	}
	
	// Detect source type if unknown (do this inside the lock for atomicity)
	if sourceType == SourceTypeUnknown {
		sourceType = detectSourceTypeFromString(source)
	}
	
	// NEW source - must pass validation for security
	// Do validation while holding the lock to ensure atomicity
	if err := r.validateConnectionString(source, sourceType); err != nil {
		// Security: reject invalid NEW sources completely
		log.Printf("❌ Rejected invalid source during migration: %s (type: %v) - %v", 
			r.sanitizeConnectionString(source, sourceType), sourceType, err)
		// Return the original source - it won't work but at least won't crash
		// The calling code will handle the failure appropriately
		return source
	}

	// Need to create new source - do it while holding the lock
	// Generate ID
	id := r.generateID(sourceType)
	
	// Create new source
	audioSource := &AudioSource{
		ID:               id,
		DisplayName:      r.generateDisplayName(&AudioSource{ID: id, Type: sourceType, SafeString: r.sanitizeConnectionString(source, sourceType)}),
		Type:             sourceType,
		connectionString: source,
		SafeString:       r.sanitizeConnectionString(source, sourceType),
		RegisteredAt:     time.Now(),
		LastSeen:         time.Now(),
		IsActive:         true,
		TotalBytes:       0,
		ErrorCount:       0,
	}

	// Store in registry
	r.sources[audioSource.ID] = audioSource
	r.connectionMap[source] = audioSource.ID

	log.Printf("🔄 Auto-migrated source: %s -> %s", audioSource.SafeString, audioSource.ID)
	
	return audioSource.ID
}

// ListSources returns all registered sources (without connection strings)
func (r *AudioSourceRegistry) ListSources() []*AudioSource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]*AudioSource, 0, len(r.sources))
	for _, source := range r.sources {
		// Create a copy without the connection string for safety
		sourceCopy := *source
		sourceCopy.connectionString = "" // Never expose connection string
		sources = append(sources, &sourceCopy)
	}
	return sources
}

// UpdateSourceMetrics updates metrics for a source
func (r *AudioSourceRegistry) UpdateSourceMetrics(sourceID string, bytesProcessed int64, hasError bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if source, exists := r.sources[sourceID]; exists {
		source.TotalBytes += bytesProcessed
		source.LastSeen = time.Now()
		if hasError {
			source.ErrorCount++
		}
	}
}

// RemoveSource removes a source from the registry and cleans up associated resources
// This prevents memory leaks when sources are no longer needed
func (r *AudioSourceRegistry) RemoveSource(sourceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	source, exists := r.sources[sourceID]
	if !exists {
		return fmt.Errorf("source not found: %s", sourceID)
	}

	// Remove from both maps
	delete(r.sources, sourceID)
	delete(r.connectionMap, source.connectionString)

	log.Printf("🗑️ Removed audio source: %s (ID: %s)", source.SafeString, sourceID)
	
	return nil
}

// RemoveSourceByConnection removes a source by its connection string
func (r *AudioSourceRegistry) RemoveSourceByConnection(connectionString string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sourceID, exists := r.connectionMap[connectionString]
	if !exists {
		return fmt.Errorf("source not found for connection: %s", connectionString)
	}

	source := r.sources[sourceID]
	
	// Remove from both maps
	delete(r.sources, sourceID)
	delete(r.connectionMap, connectionString)

	log.Printf("🗑️ Removed audio source: %s (ID: %s)", source.SafeString, sourceID)
	
	return nil
}

// CleanupInactiveSources removes sources that haven't been seen for the specified duration
func (r *AudioSourceRegistry) CleanupInactiveSources(inactiveDuration time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoffTime := time.Now().Add(-inactiveDuration)
	removedCount := 0

	for id, source := range r.sources {
		if source.LastSeen.Before(cutoffTime) && !source.IsActive {
			delete(r.sources, id)
			delete(r.connectionMap, source.connectionString)
			removedCount++
			log.Printf("🗑️ Cleaned up inactive source: %s (ID: %s, last seen: %v)", 
				source.SafeString, id, source.LastSeen)
		}
	}

	if removedCount > 0 {
		log.Printf("✨ Cleaned up %d inactive audio sources", removedCount)
	}

	return removedCount
}

// validateConnectionString validates connection strings to prevent injection attacks
func (r *AudioSourceRegistry) validateConnectionString(connectionString string, sourceType SourceType) error {
	// Basic validation - non-empty
	if connectionString == "" {
		return fmt.Errorf("connection string cannot be empty")
	}
	
	// Quick check for common safe patterns to avoid expensive validation
	// This optimization helps for frequently used valid sources
	if sourceType == SourceTypeAudioCard {
		// Common audio devices are simple and safe
		if connectionString == "default" || strings.HasPrefix(connectionString, "hw:") {
			if !strings.ContainsAny(connectionString, ";&|`$\n\r()") {
				return r.validateAudioDevice(connectionString)
			}
		}
	}
	
	// Check for obvious shell injection attempts
	// Use ContainsAny for better performance on multiple patterns
	if strings.ContainsAny(connectionString, ";&|`$\n\r") || 
	   strings.Contains(connectionString, "$(") || 
	   strings.Contains(connectionString, "${") ||
	   strings.Contains(connectionString, "<(") ||
	   strings.Contains(connectionString, ">(") {
		// Allow semicolon only in RTSP URLs where it's part of the URL spec
		if strings.Contains(connectionString, ";") && sourceType == SourceTypeRTSP {
			// Validate it's actually in a URL context
			if _, err := url.Parse(connectionString); err != nil {
				return fmt.Errorf("dangerous pattern ';' detected in invalid URL")
			}
			// Semicolon is OK in valid RTSP URL, check other patterns
			if strings.ContainsAny(connectionString, "&|`$\n\r") || 
			   strings.Contains(connectionString, "$(") || 
			   strings.Contains(connectionString, "${") {
				return fmt.Errorf("dangerous pattern detected in connection string")
			}
		} else if strings.ContainsAny(connectionString, ";&|`$\n\r") {
			return fmt.Errorf("dangerous pattern detected in connection string")
		}
	}
	
	// Type-specific validation
	switch sourceType {
	case SourceTypeRTSP:
		return r.validateRTSPURL(connectionString)
	case SourceTypeFile:
		return r.validateFilePath(connectionString)
	case SourceTypeAudioCard:
		return r.validateAudioDevice(connectionString)
	default:
		// Unknown types are allowed but logged
		log.Printf("⚠️ Unknown source type for validation: %v", sourceType)
		return nil
	}
}

// validateRTSPURL validates RTSP URLs for security
func (r *AudioSourceRegistry) validateRTSPURL(rtspURL string) error {
	// Parse URL
	u, err := url.Parse(rtspURL)
	if err != nil {
		return fmt.Errorf("invalid RTSP URL: %w", err)
	}
	
	// Check scheme
	if u.Scheme != "rtsp" && u.Scheme != "rtsps" {
		return fmt.Errorf("invalid scheme '%s', expected rtsp or rtsps", u.Scheme)
	}
	
	// Check for localhost/private network access (optional, depending on security policy)
	// This is commented out as it might be valid for home users
	// if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" {
	//     return fmt.Errorf("localhost URLs not allowed")
	// }
	
	// Validate host exists
	if u.Host == "" {
		return fmt.Errorf("RTSP URL must have a host")
	}
	
	return nil
}

// validateFilePath validates file paths for security
func (r *AudioSourceRegistry) validateFilePath(filePath string) error {
	// Clean the path to prevent directory traversal
	cleanPath := filepath.Clean(filePath)
	
	// Check for directory traversal attempts
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("directory traversal detected in file path")
	}
	
	// Check for absolute paths trying to access system directories
	systemDirs := []string{"/etc", "/sys", "/proc", "/dev", "/boot"}
	for _, dir := range systemDirs {
		if strings.HasPrefix(cleanPath, dir) {
			return fmt.Errorf("access to system directory '%s' not allowed", dir)
		}
	}
	
	// Note: We don't check if file exists here as it might be created later
	
	return nil
}

// validateAudioDevice validates audio device identifiers
func (r *AudioSourceRegistry) validateAudioDevice(device string) error {
	// Common audio device patterns
	validPrefixes := []string{
		"hw:", "plughw:", "default", "pulse", "alsa",
		"sysdefault:", "front:", "surround",
	}
	
	isValid := false
	for _, prefix := range validPrefixes {
		if strings.HasPrefix(device, prefix) || device == "default" {
			isValid = true
			break
		}
	}
	
	if !isValid {
		// Check if it looks like a numeric device
		// e.g., "0" or "1" for card numbers
		if len(device) == 1 && device[0] >= '0' && device[0] <= '9' {
			isValid = true
		}
	}
	
	if !isValid {
		return fmt.Errorf("invalid audio device identifier: %s", device)
	}
	
	return nil
}

// GetSourceStats returns summary statistics
func (r *AudioSourceRegistry) GetSourceStats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := map[string]interface{}{
		"total_sources":   len(r.sources),
		"active_sources":  0,
		"rtsp_sources":    0,
		"device_sources":  0,
		"file_sources":    0,
	}

	for _, source := range r.sources {
		if source.IsActive {
			stats["active_sources"] = stats["active_sources"].(int) + 1
		}
		
		switch source.Type {
		case SourceTypeRTSP:
			stats["rtsp_sources"] = stats["rtsp_sources"].(int) + 1
		case SourceTypeAudioCard:
			stats["device_sources"] = stats["device_sources"].(int) + 1
		case SourceTypeFile:
			stats["file_sources"] = stats["file_sources"].(int) + 1
		case SourceTypeUnknown:
			// Unknown sources shouldn't normally exist, but handle for completeness
			// These would be sources that failed type detection
		}
	}

	return stats
}

// Helper methods

func (r *AudioSourceRegistry) sanitizeConnectionString(conn string, sourceType SourceType) string {
	switch sourceType {
	case SourceTypeRTSP:
		return privacy.SanitizeRTSPUrl(conn)
	case SourceTypeAudioCard, SourceTypeFile:
		// These are generally safe to log as-is
		return conn
	default:
		return conn
	}
}

func (r *AudioSourceRegistry) generateID(sourceType SourceType) string {
	r.idCounter++
	return fmt.Sprintf("%s_%03d", sourceType, r.idCounter)
}

func (r *AudioSourceRegistry) generateDisplayName(source *AudioSource) string {
	switch source.Type {
	case SourceTypeRTSP:
		return fmt.Sprintf("RTSP Camera %d", r.idCounter)
	case SourceTypeAudioCard:
		return fmt.Sprintf("Audio Device %d", r.idCounter)
	case SourceTypeFile:
		return fmt.Sprintf("Audio File %d", r.idCounter)
	default:
		return fmt.Sprintf("Audio Source %d", r.idCounter)
	}
}