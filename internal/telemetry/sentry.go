// Package telemetry provides privacy-compliant error tracking and telemetry
package telemetry

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// DeferredMessage represents a message that was captured before Sentry initialization
type DeferredMessage struct {
	Message   string
	Level     sentry.Level
	Component string
	Timestamp time.Time
}

// sentryInitialized tracks whether Sentry has been initialized
var (
	sentryInitialized bool
	deferredMessages  []DeferredMessage
	deferredMutex     sync.Mutex
)

// PlatformInfo holds privacy-safe platform information for telemetry
type PlatformInfo struct {
	OS           string `json:"os"`
	Architecture string `json:"arch"`
	Container    bool   `json:"container"`
	BoardModel   string `json:"board_model,omitempty"`
	NumCPU       int    `json:"num_cpu"`
	GoVersion    string `json:"go_version"`
}

// collectPlatformInfo gathers privacy-safe platform information for telemetry
func collectPlatformInfo() PlatformInfo {
	info := PlatformInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		Container:    conf.RunningInContainer(),
		NumCPU:       runtime.NumCPU(),
		GoVersion:    runtime.Version(),
	}

	// Only collect board model for ARM64 Linux systems (SBCs like Raspberry Pi)
	// This helps understand deployment on edge devices without privacy concerns
	if conf.IsLinuxArm64() {
		if boardModel := conf.GetBoardModel(); boardModel != "" {
			info.BoardModel = boardModel
		}
	}

	return info
}

// InitSentry initializes Sentry SDK with privacy-compliant settings
// This function will only initialize Sentry if explicitly enabled by the user
func InitSentry(settings *conf.Settings) error {
	// Check if Sentry is explicitly enabled (opt-in)
	if !settings.Sentry.Enabled {
		log.Println("Sentry telemetry is disabled (opt-in required)")
		return nil
	}

	// Use hardcoded DSN for BirdNET-Go project
	const sentryDSN = "https://b9269b6c0f8fae154df65be5a97e0435@o4509553065525248.ingest.de.sentry.io/4509553112186960"

	// Initialize Sentry with privacy-compliant options
	err := sentry.Init(sentry.ClientOptions{
		Dsn:        sentryDSN,
		SampleRate: 1.0,   // Capture all errors by default
		Debug:      false, // Keep debug off for production

		// Privacy-compliant settings
		AttachStacktrace: false, // Don't attach stack traces by default
		Environment:      "production",
		ServerName:       "", // Explicitly clear server name to prevent hostname leakage

		// Set release version if available
		Release: fmt.Sprintf("birdnet-go@%s", settings.Version),

		// BeforeSend allows us to filter sensitive data
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Remove any potentially sensitive information
			event.User = sentry.User{} // Clear user data
			event.ServerName = ""      // Ensure server name is not included

			// Clear sensitive contexts while preserving our privacy-safe platform info
			if event.Contexts != nil {
				// Remove potentially sensitive default Sentry contexts
				delete(event.Contexts, "device")  // Contains detailed device info
				delete(event.Contexts, "os")      // Contains detailed OS info
				delete(event.Contexts, "runtime") // Contains detailed runtime info
				// Keep our custom "platform" and "application" contexts as they're privacy-safe
			}

			// Only keep essential error information
			for k := range event.Extra {
				// Remove any extra data that might contain sensitive info
				if k != "error_type" && k != "component" {
					delete(event.Extra, k)
				}
			}

			// Remove hostname from tags if present
			if event.Tags != nil {
				delete(event.Tags, "server_name")
				delete(event.Tags, "hostname")
			}

			return event
		},
	})

	if err != nil {
		return fmt.Errorf("sentry initialization failed: %w", err)
	}

	// Collect platform information for telemetry
	platformInfo := collectPlatformInfo()

	// Configure global scope with system ID and platform information
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		// Set system ID as a tag for all events
		scope.SetTag("system_id", settings.SystemID)

		// Set platform tags for easy filtering in Sentry
		scope.SetTag("os", platformInfo.OS)
		scope.SetTag("arch", platformInfo.Architecture)
		scope.SetTag("container", fmt.Sprintf("%t", platformInfo.Container))
		if platformInfo.BoardModel != "" {
			scope.SetTag("board_model", platformInfo.BoardModel)
		}

		// Set application context
		scope.SetContext("application", map[string]any{
			"name":      "BirdNET-Go",
			"version":   settings.Version,
			"system_id": settings.SystemID,
		})

		// Set platform context for detailed telemetry
		scope.SetContext("platform", map[string]any{
			"os":           platformInfo.OS,
			"architecture": platformInfo.Architecture,
			"container":    platformInfo.Container,
			"board_model":  platformInfo.BoardModel,
			"num_cpu":      platformInfo.NumCPU,
			"go_version":   platformInfo.GoVersion,
		})
	})

	// Mark Sentry as initialized and process any deferred messages
	deferredMutex.Lock()
	sentryInitialized = true
	messagesToProcess := make([]DeferredMessage, len(deferredMessages))
	copy(messagesToProcess, deferredMessages)
	deferredMessages = nil // Clear the deferred messages
	deferredMutex.Unlock()

	// Process any messages that were captured before Sentry was ready
	for _, msg := range messagesToProcess {
		CaptureMessage(msg.Message, msg.Level, msg.Component)
	}

	if len(messagesToProcess) > 0 {
		log.Printf("Sentry telemetry initialized successfully, processed %d deferred messages (System ID: %s)",
			len(messagesToProcess), settings.SystemID)
	} else {
		log.Printf("Sentry telemetry initialized successfully (opt-in enabled, System ID: %s)", settings.SystemID)
	}

	return nil
}

// CaptureError captures an error with privacy-compliant context
func CaptureError(err error, component string) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	// Create a scrubbed error for privacy
	scrubbedErrorMsg := ScrubMessage(err.Error())

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetContext("error", map[string]any{
			"type":             fmt.Sprintf("%T", err),
			"scrubbed_message": scrubbedErrorMsg,
		})

		// Create a new error with scrubbed message to avoid exposing sensitive data
		scrubbedErr := fmt.Errorf("%s", scrubbedErrorMsg)
		sentry.CaptureException(scrubbedErr)
	})
}

// CaptureMessage captures a message with privacy-compliant context
func CaptureMessage(message string, level sentry.Level, component string) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	// Scrub sensitive information from the message
	scrubbedMessage := ScrubMessage(message)

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetLevel(level)
		sentry.CaptureMessage(scrubbedMessage)
	})
}

// CaptureMessageDeferred captures a message for later processing if Sentry is not yet initialized
// If Sentry is already initialized, it immediately sends the message
func CaptureMessageDeferred(message string, level sentry.Level, component string) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	deferredMutex.Lock()

	if sentryInitialized {
		// Sentry is already initialized, send immediately
		deferredMutex.Unlock() // Unlock before calling CaptureMessage to avoid deadlock
		CaptureMessage(message, level, component)
		return
	}
	defer deferredMutex.Unlock()

	// Sentry not yet initialized, store for later processing
	deferredMessage := DeferredMessage{
		Message:   message,
		Level:     level,
		Component: component,
		Timestamp: time.Now(),
	}

	deferredMessages = append(deferredMessages, deferredMessage)
}

// Flush ensures all buffered events are sent to Sentry
func Flush(timeout time.Duration) {
	settings := conf.GetSettings()
	if settings == nil || !settings.Sentry.Enabled {
		return
	}

	sentry.Flush(timeout)
}

// anonymizeURL creates a consistent, privacy-safe identifier for URLs
// This allows tracking of the same URL across telemetry without exposing sensitive information
func anonymizeURL(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, create a hash of the raw string
		hash := sha256.Sum256([]byte(rawURL))
		return fmt.Sprintf("url-hash-%x", hash[:8])
	}

	// Create a normalized version for hashing
	// Include scheme, host pattern, and path structure but remove sensitive data
	var normalizedParts []string

	// Include scheme (rtsp, http, etc.)
	if parsedURL.Scheme != "" {
		normalizedParts = append(normalizedParts, parsedURL.Scheme)
	}

	// Anonymize hostname/IP
	host := parsedURL.Hostname()
	if host != "" {
		hostType := categorizeHost(host)
		normalizedParts = append(normalizedParts, hostType)
	}

	// Include port if present
	if parsedURL.Port() != "" {
		normalizedParts = append(normalizedParts, "port-"+parsedURL.Port())
	}

	// Include path structure (without sensitive details)
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		pathStructure := anonymizePath(parsedURL.Path)
		normalizedParts = append(normalizedParts, pathStructure)
	}

	// Create consistent hash
	normalized := strings.Join(normalizedParts, ":")
	hash := sha256.Sum256([]byte(normalized))

	return fmt.Sprintf("url-%x", hash[:12])
}

// categorizeHost anonymizes hostnames while preserving useful categorization
func categorizeHost(host string) string {
	// Check for localhost patterns
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "localhost"
	}

	// Check for private IP ranges
	if isPrivateIP(host) {
		return "private-ip"
	}

	// Check for public IP
	if isIPAddress(host) {
		return "public-ip"
	}

	// For domain names, preserve TLD only
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		tld := parts[len(parts)-1]
		return "domain-" + tld
	}

	return "unknown-host"
}

// anonymizePath creates a structure-preserving but privacy-safe path representation
func anonymizePath(path string) string {
	// Remove leading/trailing slashes for processing
	path = strings.Trim(path, "/")
	if path == "" {
		return "root"
	}

	// Split path into segments
	segments := strings.Split(path, "/")
	var anonymizedSegments []string

	for _, segment := range segments {
		if segment == "" {
			continue
		}

		// Check for common patterns that might be safe to preserve
		switch {
		case isCommonStreamName(segment):
			anonymizedSegments = append(anonymizedSegments, "stream")
		case isNumeric(segment):
			anonymizedSegments = append(anonymizedSegments, "numeric")
		default:
			// Hash individual segments to maintain path structure
			hash := sha256.Sum256([]byte(segment))
			anonymizedSegments = append(anonymizedSegments, fmt.Sprintf("seg-%x", hash[:4]))
		}
	}

	return "path-" + strings.Join(anonymizedSegments, "-")
}

// isPrivateIP checks if the host is a private IP address (both IPv4 and IPv6)
func isPrivateIP(host string) bool {
	privateRanges := []string{
		// IPv4 private ranges
		"10.", "172.16.", "172.17.", "172.18.", "172.19.", "172.20.", "172.21.", "172.22.", "172.23.",
		"172.24.", "172.25.", "172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"192.168.", "169.254.",
		// IPv6 private ranges
		"fc00:", "fd00:", // Unique local addresses
		"fe80:",                   // Link-local addresses
		"::1",                     // Loopback
		"ff00:", "ff01:", "ff02:", // Multicast
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(strings.ToLower(host), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// isIPAddress checks if the host looks like an IP address
func isIPAddress(host string) bool {
	// Simple regex for IPv4
	ipv4Regex := regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
	if ipv4Regex.MatchString(host) {
		return true
	}

	// Check for IPv6 (contains colons)
	return strings.Contains(host, ":")
}

// isCommonStreamName checks if a path segment is a common, non-sensitive stream name
func isCommonStreamName(segment string) bool {
	commonNames := []string{"stream", "live", "rtsp", "video", "audio", "feed", "cam", "camera"}
	segment = strings.ToLower(segment)

	for _, name := range commonNames {
		if strings.Contains(segment, name) {
			return true
		}
	}
	return false
}

// isNumeric checks if a string is purely numeric
func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// ScrubMessage removes or anonymizes sensitive information from telemetry messages
func ScrubMessage(message string) string {
	// Find URLs in the message and replace them with anonymized versions
	urlRegex := regexp.MustCompile(`\b(?:https?|rtsp|rtmp)://\S+`)

	return urlRegex.ReplaceAllStringFunc(message, func(foundURL string) string {
		anonymized := anonymizeURL(foundURL)
		return anonymized
	})
}
