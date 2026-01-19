package detection

// AudioSource describes where the audio came from.
// This allows safe separation of concerns: ID for buffer operations,
// SafeString for logging, DisplayName for UI.
type AudioSource struct {
	ID          string // Unique identifier (e.g., "rtsp_abc123", "alsa_card0")
	Type        string // "rtsp", "alsa", "pulseaudio", "file"
	DisplayName string // User-friendly name (e.g., "Front Yard Camera")
	SafeString  string // Connection string with credentials removed (for logging)
}

// NewAudioSource creates an AudioSource with the given ID.
// For simple cases where ID, DisplayName, and SafeString are the same.
func NewAudioSource(id string) AudioSource {
	return AudioSource{
		ID:          id,
		SafeString:  id,
		DisplayName: id,
	}
}

// NewAudioSourceWithDetails creates an AudioSource with full details.
func NewAudioSourceWithDetails(id, sourceType, displayName, safeString string) AudioSource {
	return AudioSource{
		ID:          id,
		Type:        sourceType,
		DisplayName: displayName,
		SafeString:  safeString,
	}
}
