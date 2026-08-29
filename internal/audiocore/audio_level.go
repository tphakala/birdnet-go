// audio_level.go - Real-time audio level data types for the audiocore package.
package audiocore

// AudioLevelData represents real-time audio level information for a source.
// Used as the channel type for streaming audio levels to API consumers.
type AudioLevelData struct {
	Level    int    `json:"level"`    // 0-100 normalized level
	Clipping bool   `json:"clipping"` // true if clipping is detected
	Source   string `json:"source"`   // Source identifier
	Name     string `json:"name"`     // Human-readable name
	// Optional magnitude spectrum for the live spectrogram fallback, computed
	// from the same PCM as Level. See analysis.spectrumAnalyzer for how the bins
	// are produced and audio.filterSpectrum for when they reach a client.
	Spectrum           []byte  `json:"spectrum,omitempty"`           // 0-255 bins, log scale
	SpectrumSampleRate int     `json:"spectrumSampleRate,omitempty"` // source rate, not the browser's
	SpectrumTime       float64 `json:"spectrumTime,omitempty"`       // capture time, Unix seconds
}
