// source_registry.go - Core audio source registry implementation
package myaudio

import (
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// SourceStats provides structured statistics about registered sources
type SourceStats struct {
	Total  int `json:"total_sources"`
	Active int `json:"active_sources"`
	RTSP   int `json:"rtsp_sources"`
	Device int `json:"device_sources"`
	File   int `json:"file_sources"`
}

// AudioSourceRegistry manages all audio sources in the system
type AudioSourceRegistry struct {
	// Core storage
	sources       map[string]*AudioSource // ID -> AudioSource
	connectionMap map[string]string       // connectionString -> ID (for fast lookups)

	// Reference counting for cleanup
	refCounts map[string]*int32 // sourceID -> reference count

	// Failed validation cache to prevent log spam
	failedValidations map[string]bool // connectionString -> true (prevents repeated warnings)

	// Thread safety
	mu sync.RWMutex

	// Logger
	logger *slog.Logger
}

var (
	registry     *AudioSourceRegistry
	registryOnce sync.Once

	// Sentinel errors for better error handling
	ErrSourceNotFound = errors.Newf("source not found").
				Component("myaudio").
				Category(errors.CategoryValidation).
				Build()
)

// GetRegistry returns the singleton registry instance
func GetRegistry() *AudioSourceRegistry {
	registryOnce.Do(func() {
		logger := logging.ForService("myaudio")
		if logger == nil {
			// Fallback for tests or when logging is not initialized
			logger = slog.Default()
		}
		registry = &AudioSourceRegistry{
			sources:           make(map[string]*AudioSource),
			connectionMap:     make(map[string]string),
			refCounts:         make(map[string]*int32),
			failedValidations: make(map[string]bool),
			logger:            logger.With("component", "registry"),
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

	r.logger.With("id", source.ID).
		With("display_name", source.DisplayName).
		With("safe", source.SafeString).
		Info("Registered audio source")

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
	// Auto-detect type if unknown or if detection yields a different type
	actualType := sourceType
	if sourceType == SourceTypeUnknown {
		actualType = detectSourceTypeFromString(connectionString)
	} else {
		// Check if detection yields a different type
		detectedType := detectSourceTypeFromString(connectionString)
		if detectedType != SourceTypeUnknown && detectedType != sourceType {
			actualType = detectedType
		}
	}

	source, err := r.RegisterSource(connectionString, SourceConfig{
		Type: actualType,
	})
	if err != nil {
		r.logger.With("error", err).Error("Failed to register source")
		return nil
	}
	return source
}

// detectSourceTypeFromString determines source type from connection string
func detectSourceTypeFromString(connectionString string) SourceType {
	// RTSP URLs (including test URLs for testing)
	if strings.HasPrefix(connectionString, "rtsp://") ||
		strings.HasPrefix(connectionString, "rtsps://") ||
		strings.HasPrefix(connectionString, "test://") {
		return SourceTypeRTSP
	}

	// Audio device patterns
	if strings.HasPrefix(connectionString, "hw:") ||
		strings.HasPrefix(connectionString, "plughw:") ||
		strings.Contains(connectionString, "alsa") ||
		strings.Contains(connectionString, "pulse") ||
		strings.Contains(connectionString, "dsnoop") ||
		strings.Contains(connectionString, "sysdefault") ||
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
		// Only log once per unique source to prevent spam
		if !r.failedValidations[source] {
			r.failedValidations[source] = true
			r.logger.With("safe", r.sanitizeConnectionString(source, sourceType)).
				With("type", sourceType).
				With("error", err).
				Warn("Rejected invalid source during migration")
		}
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

	r.logger.With("id", audioSource.ID).
		With("safe", audioSource.SafeString).
		Info("Auto-migrated source")

	return audioSource.ID
}

// ListSources returns all registered sources (without connection strings) in deterministic order
func (r *AudioSourceRegistry) ListSources() []*AudioSource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]*AudioSource, 0, len(r.sources))

	// Collect all source IDs for sorting
	sourceIDs := make([]string, 0, len(r.sources))
	for id := range r.sources {
		sourceIDs = append(sourceIDs, id)
	}

	// Sort IDs for deterministic ordering
	sort.Strings(sourceIDs)

	// Build result in sorted order
	for _, id := range sourceIDs {
		source := r.sources[id]
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

// AcquireSourceReference increments the reference count for a source
func (r *AudioSourceRegistry) AcquireSourceReference(sourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sources[sourceID]; exists {
		if r.refCounts[sourceID] == nil {
			initialCount := int32(1)
			r.refCounts[sourceID] = &initialCount
		} else {
			// No need for atomic since we hold the mutex
			*r.refCounts[sourceID]++
		}
	}
}

// ReleaseSourceReference decrements the reference count and removes source if count reaches zero
func (r *AudioSourceRegistry) ReleaseSourceReference(sourceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	source, exists := r.sources[sourceID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrSourceNotFound, sourceID)
	}

	// Decrement reference count (no need for atomic since we hold the mutex)
	var newCount int32
	if r.refCounts[sourceID] != nil {
		*r.refCounts[sourceID]--
		newCount = *r.refCounts[sourceID]
	}

	// Remove source if no more references
	if newCount <= 0 {
		delete(r.sources, sourceID)
		delete(r.connectionMap, source.connectionString)
		delete(r.refCounts, sourceID)

		r.logger.With("id", sourceID).
			With("safe", source.SafeString).
			Info("Removed unreferenced audio source")
	}

	return nil
}

// RemoveSource removes a source from the registry and cleans up associated resources
// This prevents memory leaks when sources are no longer needed
func (r *AudioSourceRegistry) RemoveSource(sourceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	source, exists := r.sources[sourceID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrSourceNotFound, sourceID)
	}

	// Remove from all maps
	delete(r.sources, sourceID)
	delete(r.connectionMap, source.connectionString)
	delete(r.refCounts, sourceID)

	r.logger.With("id", sourceID).
		With("safe", source.SafeString).
		Info("Removed audio source")

	return nil
}

// RemoveSourceResult represents the result of attempting to remove a source
type RemoveSourceResult int

const (
	// RemoveSourceSuccess indicates the source was successfully removed
	RemoveSourceSuccess RemoveSourceResult = iota
	// RemoveSourceInUse indicates the source is still in use and cannot be removed
	RemoveSourceInUse
	// RemoveSourceNotFound indicates the source was not found in the registry
	RemoveSourceNotFound
)

// String returns a string representation of the result
func (r RemoveSourceResult) String() string {
	switch r {
	case RemoveSourceSuccess:
		return "success"
	case RemoveSourceInUse:
		return "in_use"
	case RemoveSourceNotFound:
		return "not_found"
	default:
		return "unknown"
	}
}

// BufferUsageChecker is a function type that checks if a source is still in use
// It should return true if the source is in use, false otherwise
type BufferUsageChecker func(sourceID string) bool

// RemoveSourceIfUnused atomically checks if a source is in use and removes it if not
// This prevents TOCTOU races between checking usage and removing the source
func (r *AudioSourceRegistry) RemoveSourceIfUnused(sourceID string, checkers ...BufferUsageChecker) (RemoveSourceResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	source, exists := r.sources[sourceID]
	if !exists {
		return RemoveSourceNotFound, fmt.Errorf("%w: %s", ErrSourceNotFound, sourceID)
	}

	// Check if source is in use by any buffer type
	for _, checker := range checkers {
		if checker(sourceID) {
			return RemoveSourceInUse, fmt.Errorf("source %s is still in use", sourceID)
		}
	}

	// Source is not in use, safe to remove
	delete(r.sources, sourceID)
	delete(r.connectionMap, source.connectionString)

	r.logger.With("id", sourceID).
		With("safe", source.SafeString).
		Info("Removed unused audio source")

	return RemoveSourceSuccess, nil
}

// RemoveSourceByConnection removes a source by its connection string
func (r *AudioSourceRegistry) RemoveSourceByConnection(connectionString string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sourceID, exists := r.connectionMap[connectionString]
	if !exists {
		// Sanitize connection string before including in error
		safeString := r.sanitizeConnectionString(connectionString, SourceTypeUnknown)
		return fmt.Errorf("%w: connection %s", ErrSourceNotFound, safeString)
	}

	source := r.sources[sourceID]

	// Remove from all maps
	delete(r.sources, sourceID)
	delete(r.connectionMap, connectionString)
	delete(r.refCounts, sourceID)

	r.logger.With("id", sourceID).
		With("safe", source.SafeString).
		Info("Removed audio source by connection")

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
			r.logger.With("id", id).
				With("safe", source.SafeString).
				With("last_seen", source.LastSeen).
				Info("Cleaned up inactive source")
		}
	}

	if removedCount > 0 {
		r.logger.With("count", removedCount).
			Info("Cleaned up inactive audio sources")
	}

	return removedCount
}

// validateConnectionString validates connection strings to prevent injection attacks
func (r *AudioSourceRegistry) validateConnectionString(connectionString string, sourceType SourceType) error {
	// Basic validation - non-empty
	if connectionString == "" {
		return fmt.Errorf("connection string cannot be empty")
	}

	// For audio devices, be more permissive since they're local
	// and can have various formats depending on the system
	if sourceType == SourceTypeAudioCard {
		return r.validateAudioDevice(connectionString)
	}

	// For other types (RTSP, files), check for shell injection attempts - be strict for security
	// Don't allow any shell metacharacters to prevent command injection
	if strings.ContainsAny(connectionString, ";&|`$\n\r") ||
		strings.Contains(connectionString, "$(") ||
		strings.Contains(connectionString, "${") ||
		strings.Contains(connectionString, "<(") ||
		strings.Contains(connectionString, ">(") {
		return fmt.Errorf("dangerous pattern detected in connection string")
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
		// Unknown types are allowed but logged
		r.logger.Warn("Unknown source type for validation", "type", sourceType)
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

	// Check scheme (allow test scheme for testing)
	if u.Scheme != "rtsp" && u.Scheme != "rtsps" && u.Scheme != "test" {
		return fmt.Errorf("invalid scheme '%s', expected rtsp, rtsps, or test", u.Scheme)
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
	// Use exact match or proper path segment prefix to avoid false positives
	systemDirs := []string{"/etc", "/sys", "/proc", "/dev", "/boot"}
	for _, dir := range systemDirs {
		// Check for exact match or true path segment prefix
		if cleanPath == dir || strings.HasPrefix(cleanPath, dir+string(filepath.Separator)) {
			return fmt.Errorf("access to system directory '%s' not allowed", dir)
		}
	}

	// Note: We don't check if file exists here as it might be created later

	return nil
}

// validateAudioDevice validates audio device identifiers
func (r *AudioSourceRegistry) validateAudioDevice(device string) error {
	// Just check that it's not empty
	// We can't predict all possible device names across different systems
	if device == "" {
		return fmt.Errorf("audio device identifier cannot be empty")
	}

	// Reject known invalid paths that are not audio devices
	if device == "/dev/null" || device == "/dev/zero" || device == "/dev/random" || device == "/dev/urandom" {
		return fmt.Errorf("invalid audio device: %s is not an audio device", device)
	}

	// Only check for the most dangerous shell injection patterns
	// Audio devices are local and users need flexibility
	if strings.Contains(device, "$(") ||
		strings.Contains(device, "${") ||
		strings.Contains(device, "`") ||
		strings.Contains(device, "&&") ||
		strings.Contains(device, "||") ||
		strings.Contains(device, ";") && strings.Contains(device, "|") {
		return fmt.Errorf("potentially dangerous pattern in audio device identifier")
	}

	return nil
}

// GetSourceStats returns summary statistics
func (r *AudioSourceRegistry) GetSourceStats() SourceStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := SourceStats{
		Total: len(r.sources),
	}

	for _, source := range r.sources {
		if source.IsActive {
			stats.Active++
		}

		switch source.Type {
		case SourceTypeRTSP:
			stats.RTSP++
		case SourceTypeAudioCard:
			stats.Device++
		case SourceTypeFile:
			stats.File++
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

// generateID generates a new unique source ID using UUID
// IMPORTANT: This method is not thread-safe and must be called with r.mu held
func (r *AudioSourceRegistry) generateID(sourceType SourceType) string {
	// Generate UUID with error handling
	u, err := uuid.NewRandom()
	if err != nil {
		// Fallback to timestamp-based ID if UUID generation fails
		// This is extremely rare but provides a safety net
		r.logger.Error("Failed to generate UUID, using timestamp fallback",
			"error", err,
			"source_type", sourceType)
		// Use nanosecond timestamp for uniqueness
		id := fmt.Sprintf("%d", time.Now().UnixNano())[:8]
		return fmt.Sprintf("%s_%s", sourceType, id)
	}
	// Take first 8 characters for brevity
	id := u.String()[:8]
	return fmt.Sprintf("%s_%s", sourceType, id)
}

func (r *AudioSourceRegistry) generateDisplayName(source *AudioSource) string {
	switch source.Type {
	case SourceTypeRTSP:
		// Use SafeString (sanitized URL) as display name
		return source.SafeString
	case SourceTypeAudioCard:
		// Parse device string based on OS
		return r.parseAudioDeviceName(source.SafeString)
	case SourceTypeFile:
		// Use filename without path
		if source.SafeString != "" {
			return fmt.Sprintf("Audio File: %s", filepath.Base(source.SafeString))
		}
		return "Audio File"
	default:
		return "Audio Source"
	}
}

// parseAudioDeviceName converts device strings to user-friendly names based on OS
func (r *AudioSourceRegistry) parseAudioDeviceName(deviceString string) string {
	switch runtime.GOOS {
	case "linux":
		return r.parseLinuxDeviceName(deviceString)
	case "darwin":
		return r.parseDarwinDeviceName(deviceString)
	case "windows":
		return r.parseWindowsDeviceName(deviceString)
	default:
		// Fallback for unknown OS
		return fmt.Sprintf("Audio Device (%s)", deviceString)
	}
}

// parseLinuxDeviceName converts ALSA device strings to user-friendly names
func (r *AudioSourceRegistry) parseLinuxDeviceName(deviceString string) string {
	// Handle common simple cases first
	switch deviceString {
	case "default":
		return "Default Audio Device"
	case "malgo":
		// Legacy malgo usage - use generic name
		return "Audio Device"
	}

	// Parse hw:CARD=Device,DEV=0 format
	if strings.HasPrefix(deviceString, "hw:") {
		return r.parseLinuxHWDeviceString(deviceString)
	}

	// Parse plughw:CARD,DEV format
	if strings.HasPrefix(deviceString, "plughw:") {
		return r.parseLinuxPlugHWDeviceString(deviceString)
	}

	// Fallback for unknown formats
	return fmt.Sprintf("Audio Device (%s)", deviceString)
}

// parseDarwinDeviceName converts macOS Core Audio device strings to user-friendly names
func (r *AudioSourceRegistry) parseDarwinDeviceName(deviceString string) string {
	// Common macOS audio device patterns
	switch deviceString {
	case "default":
		return "Default Audio Device"
	case "Built-in Microphone":
		return "Built-in Microphone"
	case "Built-in Output":
		return "Built-in Output"
	}

	// Check for common patterns
	if strings.Contains(deviceString, "USB") {
		return deviceString // USB devices usually have descriptive names
	}

	if strings.Contains(deviceString, "Aggregate") {
		return "Aggregate Device"
	}

	if strings.Contains(deviceString, "Multi-Output") {
		return "Multi-Output Device"
	}

	// Return as-is for other cases (Core Audio names are usually descriptive)
	return deviceString
}

// parseWindowsDeviceName converts Windows audio device strings to user-friendly names
func (r *AudioSourceRegistry) parseWindowsDeviceName(deviceString string) string {
	// Common Windows audio device patterns
	if deviceString == "default" {
		return "Default Audio Device"
	}

	// Windows WASAPI patterns
	if strings.HasPrefix(deviceString, "wasapi:") {
		// Remove wasapi: prefix and return the device name
		name := strings.TrimPrefix(deviceString, "wasapi:")
		if name != "" {
			return name
		}
	}

	// Windows DirectSound patterns
	if strings.HasPrefix(deviceString, "dsound:") {
		name := strings.TrimPrefix(deviceString, "dsound:")
		if name != "" {
			return fmt.Sprintf("DirectSound: %s", name)
		}
	}

	// Check for GUID patterns (Windows device IDs)
	if strings.Contains(deviceString, "{") && strings.Contains(deviceString, "}") {
		// Extract device name if present before the GUID
		if idx := strings.Index(deviceString, "{"); idx > 0 {
			return strings.TrimSpace(deviceString[:idx])
		}
		return "Audio Device"
	}

	// Return as-is for other cases
	return deviceString
}

// parseLinuxHWDeviceString parses hardware device strings like "hw:CARD=Device,DEV=0"
func (r *AudioSourceRegistry) parseLinuxHWDeviceString(deviceString string) string {
	// Remove "hw:" prefix
	params := strings.TrimPrefix(deviceString, "hw:")

	// Split by comma to get parameters
	parts := strings.Split(params, ",")

	var cardName string
	var devNum string

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "CARD=") {
			cardName = strings.TrimPrefix(part, "CARD=")
		} else if strings.HasPrefix(part, "DEV=") {
			devNum = strings.TrimPrefix(part, "DEV=")
		}
	}

	// If we extracted card name and device number
	if cardName != "" && devNum != "" {
		// Try to resolve the card name to a friendly name
		friendlyCardName := r.resolveFriendlyCardName(cardName)
		return fmt.Sprintf("%s #%s", friendlyCardName, devNum)
	}

	// Fallback if parsing failed
	return fmt.Sprintf("Audio Device (%s)", deviceString)
}

// parseLinuxPlugHWDeviceString parses plugin hardware strings like "plughw:0,0"
func (r *AudioSourceRegistry) parseLinuxPlugHWDeviceString(deviceString string) string {
	// Remove "plughw:" prefix
	params := strings.TrimPrefix(deviceString, "plughw:")

	// Split by comma to get card and device numbers
	parts := strings.Split(params, ",")

	if len(parts) >= 2 {
		cardNum := strings.TrimSpace(parts[0])
		devNum := strings.TrimSpace(parts[1])
		return fmt.Sprintf("Audio Card %s Device %s", cardNum, devNum)
	}

	// Fallback
	return fmt.Sprintf("Audio Device (%s)", deviceString)
}

// resolveFriendlyCardName maps ALSA card identifiers to friendly names
func (r *AudioSourceRegistry) resolveFriendlyCardName(cardID string) string {
	// Common ALSA card ID to friendly name mappings
	friendlyNames := map[string]string{
		"Device":     "USB Audio Device",
		"PCH":        "HDA Intel PCH",
		"HDMI":       "HDMI Audio",
		"USB":        "USB Audio",
		"Headset":    "USB Headset",
		"Webcam":     "USB Webcam",
		"Microphone": "USB Microphone",
		"Speaker":    "USB Speaker",
	}

	// Look for exact match first
	if friendlyName, exists := friendlyNames[cardID]; exists {
		return friendlyName
	}

	// Look for partial matches (case insensitive)
	cardIDLower := strings.ToLower(cardID)
	for key, value := range friendlyNames {
		if strings.Contains(cardIDLower, strings.ToLower(key)) {
			return value
		}
	}

	// If no friendly mapping found, use the card ID as-is
	// This handles cases where the card ID is already descriptive
	return cardID
}
