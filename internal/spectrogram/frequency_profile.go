package spectrogram

// FrequencyProfile controls spectrogram frequency range and resampling
// per detection. The gate is the detection's model type: bat models get bat
// settings (no resample, high-pass at 15 kHz) and everything else gets bird
// defaults (resample to 24 kHz, full range).
type FrequencyProfile struct {
	ResampleRate int // Target sample rate in Hz; 0 means keep native rate
	HighPassHz   int // High-pass filter cutoff in Hz; 0 means no filter
}

const (
	// batHighPassHz removes content below the bat echolocation floor. Set to
	// 15 kHz (rather than ~18 kHz) so the low-frequency calls of Noctule bats
	// (Nyctalus noctula, ~16-20 kHz) are retained in the spectrogram.
	batHighPassHz   = 15000
	birdResampleHz  = 24000
	modelTypeBatStr = "bat"
)

// BirdProfile returns the default frequency profile for bird detections.
func BirdProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: birdResampleHz,
		HighPassHz:   0,
	}
}

// BatProfile returns the frequency profile for bat detections captured
// at 256 kHz. No resampling is applied (keeps native rate), and a
// high-pass filter at 15 kHz removes content below the bat echolocation
// floor.
func BatProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: 0,
		HighPassHz:   batHighPassHz,
	}
}

// ProfileForModelType selects the appropriate frequency profile based on
// the AI model's type string (as stored in ai_models.model_type).
// Bat models use the bat profile; everything else uses bird defaults.
func ProfileForModelType(modelType string) FrequencyProfile {
	if modelType == modelTypeBatStr {
		return BatProfile()
	}
	return BirdProfile()
}

// ProfileSuffix returns a short, stable token identifying the frequency profile
// for use in spectrogram cache filenames and queue keys, so renders made with
// different profiles do not collide on disk. The default bird profile returns ""
// for backward compatibility with existing cached spectrograms.
func ProfileSuffix(p FrequencyProfile) string {
	if p.HighPassHz > 0 {
		return modelTypeBatStr
	}
	return ""
}
