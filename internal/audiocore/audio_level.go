// audio_level.go - Real-time audio level data types for the audiocore package.
package audiocore

// AudioLevelData represents real-time audio level information for a source.
// Used as the channel type for streaming audio levels to API consumers.
type AudioLevelData struct {
	Level    int    `json:"level"`    // 0-100 normalized level
	Clipping bool   `json:"clipping"` // true if clipping is detected
	Source   string `json:"source"`   // Source identifier
	Name     string `json:"name"`     // Human-readable name
}
