// source_registry.go - Core audio source registry implementation
package myaudio

import (
	"fmt"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// OS name constants for runtime.GOOS comparisons.
const (
	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"
)

// Audio device constants.
const (
	deviceDefault            = "default"
	deviceDefaultDisplayName = "Default Audio Device"
)

// SourceStats provides structured statistics about registered sources
type SourceStats struct {
	Total  int `json:"total_sources"`
	Active int `json:"active_sources"`
	RTSP   int `json:"rtsp_sources"`
	HTTP   int `json:"http_sources"`
	HLS    int `json:"hls_sources"`
	RTMP   int `json:"rtmp_sources"`
	UDP    int `json:"udp_sources"`
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

	// Thread safety
	mu sync.RWMutex

	// Logger
	logger logger.Logger
}

var (
	registry     *AudioSourceRegistry
	registryOnce sync.Once

	// Sentinel errors for better error handling
	ErrSourceNotFound = errors.Newf("source not found").
				Component("myaudio").
				Category(errors.CategoryNotFound).
				Build()
)

// GetRegistry returns the singleton registry instance
func GetRegistry() *AudioSourceRegistry {
	registryOnce.Do(func() {
		log := GetLogger()
		registry = &AudioSourceRegistry{
			sources:       make(map[string]*AudioSource),
			connectionMap: make(map[string]string),
			refCounts:     make(map[string]*int32),
			logger:        log.With(logger.String("component", "registry")),
		}
	})
	return registry
}

// RegisterSource registers a new audio source or updates existing one
func (r *AudioSourceRegistry) RegisterSource(connectionString string, config SourceConfig) (*AudioSource, error) {
	// Validate connection string before acquiring lock
	if err := r.validateConnectionString(connectionString, config.Type); err != nil {
		return nil, errors.New(err).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "register_source").
			Context("source_type", config.Type).
			Context("validation_stage", "connection_string").
			Build()
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

	r.logger.Info("Registered audio source",
		logger.String("id", source.ID),
		logger.String("display_name", source.DisplayName),
		logger.String("safe", source.SafeString))

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
		r.logger.Error("Failed to register source", logger.Error(err))
		return nil
	}
	return source
}

// detectSourceTypeFromString determines source type from connection string
func detectSourceTypeFromString(connectionString string) SourceType {
	// RTSP/RTSPS streams (including test URLs for testing)
	if strings.HasPrefix(connectionString, "rtsp://") ||
		strings.HasPrefix(connectionString, "rtsps://") ||
		strings.HasPrefix(connectionString, "test://") {
		return SourceTypeRTSP
	}

	// RTMP/RTMPS streams
	if strings.HasPrefix(connectionString, "rtmp://") ||
		strings.HasPrefix(connectionString, "rtmps://") {
		return SourceTypeRTMP
	}

	// HLS streams (m3u8 playlists)
	if strings.HasSuffix(connectionString, ".m3u8") ||
		strings.Contains(connectionString, ".m3u8?") {
		return SourceTypeHLS
	}

	// HTTP/HTTPS audio streams (check after HLS to prioritize .m3u8 detection)
	if strings.HasPrefix(connectionString, "http://") ||
		strings.HasPrefix(connectionString, "https://") {
		return SourceTypeHTTP
	}

	// UDP/RTP streams
	if strings.HasPrefix(connectionString, "udp://") ||
		strings.HasPrefix(connectionString, "rtp://") {
		return SourceTypeUDP
	}

	// Audio device patterns
	if strings.HasPrefix(connectionString, "hw:") ||
		strings.HasPrefix(connectionString, "plughw:") ||
		strings.Contains(connectionString, "alsa") ||
		strings.Contains(connectionString, "pulse") ||
		strings.Contains(connectionString, "dsnoop") ||
		strings.Contains(connectionString, "sysdefault") ||
		connectionString == deviceDefault {
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

	// Default to unknown for unrecognized patterns
	return SourceTypeUnknown
}

// ListSources returns all registered sources (without connection strings) in deterministic order
func (r *AudioSourceRegistry) ListSources() []*AudioSource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]*AudioSource, 0, len(r.sources))

	// Collect all source IDs for sorting
	sourceIDs := slices.Collect(maps.Keys(r.sources))

	// Sort IDs for deterministic ordering
	slices.Sort(sourceIDs)

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

	// Handle reference counting - if no refCount entry exists, treat as 0 and remove immediately
	refCountPtr, refCountExists := r.refCounts[sourceID]
	var newCount int32

	if !refCountExists {
		// No refCount entry means this source was never acquired, treat as 0 and remove
		newCount = -1 // This will trigger removal below
		r.logger.Warn("Attempted to release reference for source without refCount entry",
			logger.String("id", sourceID),
			logger.String("safe", source.SafeString))
	} else {
		// Decrement reference count (no need for atomic since we hold the mutex)
		*refCountPtr--
		newCount = *refCountPtr
	}

	// Remove source if no more references (including the case where no refCount existed)
	if newCount <= 0 {
		delete(r.sources, sourceID)
		delete(r.connectionMap, source.connectionString)
		delete(r.refCounts, sourceID)

		r.logger.Info("Removed unreferenced audio source",
			logger.String("id", sourceID),
			logger.String("safe", source.SafeString))
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

	r.logger.Info("Removed audio source",
		logger.String("id", sourceID),
		logger.String("safe", source.SafeString))

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
			return RemoveSourceInUse, errors.Newf("source %s is still in use", sourceID).
				Component("myaudio").
				Category(errors.CategoryState).
				Context("operation", "remove_source_if_unused").
				Context("source_id", sourceID).
				Context("reason", "buffer_checker_reported_in_use").
				Build()
		}
	}

	// Source is not in use, safe to remove
	delete(r.sources, sourceID)
	delete(r.connectionMap, source.connectionString)
	delete(r.refCounts, sourceID)

	r.logger.Info("Removed unused audio source",
		logger.String("id", sourceID),
		logger.String("safe", source.SafeString))

	return RemoveSourceSuccess, nil
}

// RemoveSourceByConnection removes a source by its connection string
func (r *AudioSourceRegistry) RemoveSourceByConnection(connectionString string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sourceID, exists := r.connectionMap[connectionString]
	if !exists {
		// Detect source type from connection string and sanitize using that type
		sourceType := detectSourceTypeFromString(connectionString)
		safeString := r.sanitizeConnectionString(connectionString, sourceType)
		baseErr := errors.Newf("source not found for connection: %s", safeString).
			Component("myaudio").
			Category(errors.CategoryNotFound).
			Context("operation", "remove_source_by_connection").
			Context("safe_connection", safeString).
			Build()
		return fmt.Errorf("%w: %w", ErrSourceNotFound, baseErr)
	}

	source := r.sources[sourceID]

	// Remove from all maps
	delete(r.sources, sourceID)
	delete(r.connectionMap, connectionString)
	delete(r.refCounts, sourceID)

	r.logger.Info("Removed audio source by connection",
		logger.String("id", sourceID),
		logger.String("safe", source.SafeString))

	return nil
}

// CleanupInactiveSources removes sources that haven't been seen for the specified duration
func (r *AudioSourceRegistry) CleanupInactiveSources(inactiveDuration time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoffTime := time.Now().Add(-inactiveDuration)
	removedCount := 0

	for id, source := range r.sources {
		if !source.LastSeen.Before(cutoffTime) || source.IsActive {
			continue
		}
		delete(r.sources, id)
		delete(r.connectionMap, source.connectionString)
		delete(r.refCounts, id)
		removedCount++
		r.logger.Info("Cleaned up inactive source",
			logger.String("id", id),
			logger.String("safe", source.SafeString),
			logger.Time("last_seen", source.LastSeen))
	}

	if removedCount > 0 {
		r.logger.Info("Cleaned up inactive audio sources",
			logger.Int("count", removedCount))
	}

	return removedCount
}

// validateConnectionString validates connection strings to prevent injection attacks
func (r *AudioSourceRegistry) validateConnectionString(connectionString string, sourceType SourceType) error {
	// Basic validation - non-empty
	if connectionString == "" {
		return errors.Newf("connection string cannot be empty").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_connection_string").
			Context("source_type", sourceType).
			Build()
	}

	// For audio devices, be more permissive since they're local
	// and can have various formats depending on the system
	if sourceType == SourceTypeAudioCard {
		return r.validateAudioDevice(connectionString)
	}

	// Check for shell injection attempts - customize patterns based on source type
	// Stream types (RTSP, HTTP, HLS, RTMP, UDP) allow query parameters with ampersands
	isStreamType := sourceType == SourceTypeRTSP || sourceType == SourceTypeHTTP ||
		sourceType == SourceTypeHLS || sourceType == SourceTypeRTMP || sourceType == SourceTypeUDP
	if isStreamType {
		// For stream URLs, allow query parameters with ampersands (e.g., ?channel=1&subtype=0)
		// but still block dangerous shell injection patterns
		if strings.ContainsAny(connectionString, ";\n\r`|") ||
			strings.Contains(connectionString, "$(") ||
			strings.Contains(connectionString, "${") ||
			strings.Contains(connectionString, "<(") ||
			strings.Contains(connectionString, ">(") {
			return errors.Newf("dangerous pattern detected in connection string").
				Component("myaudio").
				Category(errors.CategoryValidation).
				Context("operation", "validate_connection_string").
				Context("source_type", sourceType).
				Context("reason", "shell_injection_prevention").
				Build()
		}
	} else {
		// For other types (files), check for shell injection attempts - be strict for security
		// Don't allow any shell metacharacters to prevent command injection
		if strings.ContainsAny(connectionString, ";&|`$\n\r") ||
			strings.Contains(connectionString, "$(") ||
			strings.Contains(connectionString, "${") ||
			strings.Contains(connectionString, "<(") ||
			strings.Contains(connectionString, ">(") {
			return errors.Newf("dangerous pattern detected in connection string").
				Component("myaudio").
				Category(errors.CategoryValidation).
				Context("operation", "validate_connection_string").
				Context("source_type", sourceType).
				Context("reason", "shell_injection_prevention").
				Build()
		}
	}

	// Type-specific validation
	switch sourceType {
	case SourceTypeRTSP:
		return r.validateRTSPURL(connectionString)
	case SourceTypeHTTP, SourceTypeHLS:
		return r.validateHTTPURL(connectionString)
	case SourceTypeRTMP:
		return r.validateRTMPURL(connectionString)
	case SourceTypeUDP:
		return r.validateUDPURL(connectionString)
	case SourceTypeFile:
		return r.validateFilePath(connectionString)
	case SourceTypeAudioCard:
		return r.validateAudioDevice(connectionString)
	default:
		// Unknown types are allowed but logged
		r.logger.Warn("Unknown source type for validation",
			logger.String("type", string(sourceType)))
		return nil
	}
}

// validateRTSPURL validates RTSP URLs for security
func (r *AudioSourceRegistry) validateRTSPURL(rtspURL string) error {
	// Basic validation - check for RTSP scheme without using url.Parse()
	// This avoids breaking existing configurations with complex passwords
	// that may contain special characters like colons, which are valid in FFmpeg
	// but cause url.Parse() to fail due to Go's strict userinfo parsing

	// Check for empty URL
	if rtspURL == "" {
		return errors.Newf("RTSP URL cannot be empty").
			Component("myaudio").
			Category(errors.CategoryRTSP).
			Context("operation", "validate_rtsp_url").
			Context("reason", "empty_url").
			Build()
	}

	// Check scheme prefix (case-insensitive)
	lowerURL := strings.ToLower(rtspURL)
	if !strings.HasPrefix(lowerURL, "rtsp://") &&
		!strings.HasPrefix(lowerURL, "rtsps://") &&
		!strings.HasPrefix(lowerURL, "test://") {
		return errors.Newf("invalid scheme, expected rtsp://, rtsps://, or test://").
			Component("myaudio").
			Category(errors.CategoryRTSP).
			Context("operation", "validate_rtsp_url").
			Context("reason", "invalid_scheme").
			Build()
	}

	// Basic structure validation - ensure there's something after the scheme
	schemeEnd := strings.Index(lowerURL, "://") + 3
	if len(rtspURL) <= schemeEnd {
		return errors.Newf("RTSP URL must have content after scheme").
			Component("myaudio").
			Category(errors.CategoryRTSP).
			Context("operation", "validate_rtsp_url").
			Context("reason", "missing_content_after_scheme").
			Build()
	}

	// Note: We intentionally avoid url.Parse() here as it's too strict for
	// existing RTSP URLs with complex passwords. FFmpeg can handle URLs that
	// Go's url.Parse() rejects. The actual connection validation happens
	// when FFmpeg attempts to connect.

	return nil
}

// validateStreamURL is a generic validator for stream URLs with scheme validation.
// It checks for empty URLs, valid schemes, and content after the scheme.
func (r *AudioSourceRegistry) validateStreamURL(streamURL, urlType, operation string, validSchemes []string) error {
	if streamURL == "" {
		return errors.Newf("%s URL cannot be empty", urlType).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", operation).
			Context("reason", "empty_url").
			Build()
	}

	lowerURL := strings.ToLower(streamURL)
	schemeValid := false
	for _, scheme := range validSchemes {
		if strings.HasPrefix(lowerURL, scheme) {
			schemeValid = true
			break
		}
	}
	if !schemeValid {
		return errors.Newf("invalid scheme, expected one of: %v", validSchemes).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", operation).
			Context("reason", "invalid_scheme").
			Build()
	}

	schemeEnd := strings.Index(lowerURL, "://") + 3
	if len(streamURL) <= schemeEnd {
		return errors.Newf("%s URL must have content after scheme", urlType).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", operation).
			Context("reason", "missing_content_after_scheme").
			Build()
	}

	return nil
}

// validateHTTPURL validates HTTP/HTTPS URLs for audio streams
func (r *AudioSourceRegistry) validateHTTPURL(httpURL string) error {
	return r.validateStreamURL(httpURL, "HTTP", "validate_http_url", []string{"http://", "https://"})
}

// validateRTMPURL validates RTMP/RTMPS URLs for audio streams
func (r *AudioSourceRegistry) validateRTMPURL(rtmpURL string) error {
	return r.validateStreamURL(rtmpURL, "RTMP", "validate_rtmp_url", []string{"rtmp://", "rtmps://"})
}

// validateUDPURL validates UDP/RTP URLs for audio streams
func (r *AudioSourceRegistry) validateUDPURL(udpURL string) error {
	return r.validateStreamURL(udpURL, "UDP", "validate_udp_url", []string{"udp://", "rtp://"})
}

// validateFilePath validates file paths for security
func (r *AudioSourceRegistry) validateFilePath(filePath string) error {
	// Clean the path to prevent directory traversal
	cleanPath := filepath.Clean(filePath)

	// Use filepath.IsLocal for comprehensive path validation (prevents CVE-2023-45284, CVE-2023-45283)
	if !filepath.IsLocal(cleanPath) {
		return errors.Newf("directory traversal or invalid path detected").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_file_path").
			Context("reason", "security_violation").
			Build()
	}

	// Check for absolute paths trying to access system directories
	// Use exact match or proper path segment prefix to avoid false positives
	systemDirs := []string{"/etc", "/sys", "/proc", "/dev", "/boot"}
	for _, dir := range systemDirs {
		// Check for exact match or true path segment prefix
		if cleanPath == dir || strings.HasPrefix(cleanPath, dir+string(filepath.Separator)) {
			return errors.Newf("access to system directory '%s' not allowed", dir).
				Component("myaudio").
				Category(errors.CategoryValidation).
				Context("operation", "validate_file_path").
				Context("system_dir", dir).
				Context("reason", "security_restriction").
				Build()
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
		return errors.Newf("audio device identifier cannot be empty").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_audio_device").
			Build()
	}

	// Reject known invalid paths that are not audio devices
	if device == "/dev/null" || device == "/dev/zero" || device == "/dev/random" || device == "/dev/urandom" {
		return errors.Newf("invalid audio device: %s is not an audio device", device).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_audio_device").
			Context("device", device).
			Context("reason", "not_audio_device").
			Build()
	}

	// Only check for the most dangerous shell injection patterns
	// Audio devices are local and users need flexibility
	if strings.Contains(device, "$(") ||
		strings.Contains(device, "${") ||
		strings.Contains(device, "`") ||
		strings.Contains(device, "&&") ||
		strings.Contains(device, "||") ||
		strings.Contains(device, ";") && strings.Contains(device, "|") {
		return errors.Newf("potentially dangerous pattern in audio device identifier").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_audio_device").
			Context("reason", "shell_injection_prevention").
			Build()
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
		case SourceTypeHTTP:
			stats.HTTP++
		case SourceTypeHLS:
			stats.HLS++
		case SourceTypeRTMP:
			stats.RTMP++
		case SourceTypeUDP:
			stats.UDP++
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
	case SourceTypeRTSP, SourceTypeHTTP, SourceTypeHLS, SourceTypeRTMP, SourceTypeUDP:
		// All stream types may contain credentials and should be sanitized
		return privacy.SanitizeStreamUrl(conn)
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
			logger.Error(err),
			logger.String("source_type", string(sourceType)))
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
	case osLinux:
		return r.parseLinuxDeviceName(deviceString)
	case osDarwin:
		return r.parseDarwinDeviceName(deviceString)
	case osWindows:
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
	case deviceDefault:
		return deviceDefaultDisplayName
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
	case deviceDefault:
		return deviceDefaultDisplayName
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
	if deviceString == deviceDefault {
		return deviceDefaultDisplayName
	}

	// Windows WASAPI patterns
	if after, ok := strings.CutPrefix(deviceString, "wasapi:"); ok {
		// Remove wasapi: prefix and return the device name
		name := after
		if name != "" {
			return name
		}
	}

	// Windows DirectSound patterns
	if after, ok := strings.CutPrefix(deviceString, "dsound:"); ok {
		name := after
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

// parseLinuxDeviceParams parses Linux audio device parameters after prefix removal
// It handles both "CARD=Name,DEV=0" format and "0,0" format
func (r *AudioSourceRegistry) parseLinuxDeviceParams(params, deviceString string) string {
	// Split by comma to get parameters
	parts := strings.Split(params, ",")

	// Check if it's CARD=...,DEV=... format
	var cardName, devNum string
	hasCardFormat := false

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, "CARD="); ok {
			cardName = after
			hasCardFormat = true
		} else if after, ok := strings.CutPrefix(part, "DEV="); ok {
			devNum = after
			hasCardFormat = true
		}
	}

	// Handle CARD=...,DEV=... format
	if hasCardFormat && cardName != "" && devNum != "" {
		friendlyCardName := r.resolveFriendlyCardName(cardName)
		return fmt.Sprintf("%s #%s", friendlyCardName, devNum)
	}

	// Handle simple numeric format like "0,0"
	if !hasCardFormat && len(parts) >= 2 {
		cardNum := strings.TrimSpace(parts[0])
		devNum := strings.TrimSpace(parts[1])
		return fmt.Sprintf("Audio Card %s Device %s", cardNum, devNum)
	}

	// Fallback
	return fmt.Sprintf("Audio Device (%s)", deviceString)
}

// parseLinuxHWDeviceString parses hardware device strings like "hw:CARD=Device,DEV=0"
func (r *AudioSourceRegistry) parseLinuxHWDeviceString(deviceString string) string {
	// Remove "hw:" prefix and use shared parser
	params := strings.TrimPrefix(deviceString, "hw:")
	return r.parseLinuxDeviceParams(params, deviceString)
}

// parseLinuxPlugHWDeviceString parses plugin hardware strings like "plughw:0,0"
func (r *AudioSourceRegistry) parseLinuxPlugHWDeviceString(deviceString string) string {
	// Remove "plughw:" prefix and use shared parser
	params := strings.TrimPrefix(deviceString, "plughw:")
	return r.parseLinuxDeviceParams(params, deviceString)
}

// StreamTypeToSourceType converts a config StreamType string to myaudio SourceType.
// This provides a centralized mapping between the conf and myaudio type systems.
func StreamTypeToSourceType(streamType string) SourceType {
	switch streamType {
	case "rtsp":
		return SourceTypeRTSP
	case "http":
		return SourceTypeHTTP
	case "hls":
		return SourceTypeHLS
	case "rtmp":
		return SourceTypeRTMP
	case "udp":
		return SourceTypeUDP
	default:
		return SourceTypeUnknown
	}
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
