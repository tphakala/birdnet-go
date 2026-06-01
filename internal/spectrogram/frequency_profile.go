package spectrogram

// FrequencyProfile controls spectrogram frequency range and resampling
// per detection. The gate is the detection's model type: bat models get
// bat settings (no resample, high-pass at 18 kHz), everything else gets
// bird defaults (resample to 24 kHz, full range).
type FrequencyProfile struct {
	MinFreqHz    int // Lower bound of visible frequency range
	MaxFreqHz    int // Upper bound of visible frequency range
	ResampleRate int // Target sample rate in Hz; 0 means keep native rate
	HighPassHz   int // High-pass filter cutoff in Hz; 0 means no filter
}

const (
	batHighPassHz   = 18000
	batMaxFreqHz    = 128000
	birdMaxFreqHz   = 12000
	birdResampleHz  = 24000
	modelTypeBatStr = "bat"
)

// BirdProfile returns the default frequency profile for bird detections.
func BirdProfile() FrequencyProfile {
	return FrequencyProfile{
		MinFreqHz:    0,
		MaxFreqHz:    birdMaxFreqHz,
		ResampleRate: birdResampleHz,
		HighPassHz:   0,
	}
}

// BatProfile returns the frequency profile for bat detections captured
// at 256 kHz. No resampling is applied (keeps native rate), and a
// high-pass filter at 18 kHz removes content below the bat echolocation
// floor.
func BatProfile() FrequencyProfile {
	return FrequencyProfile{
		MinFreqHz:    batHighPassHz,
		MaxFreqHz:    batMaxFreqHz,
		ResampleRate: 0,
		HighPassHz:   batHighPassHz,
	}
}

// ProfileForModelType selects the appropriate frequency profile based on
// the AI model's type string (as stored in ai_models.model_type).
func ProfileForModelType(modelType string) FrequencyProfile {
	if modelType == modelTypeBatStr {
		return BatProfile()
	}
	return BirdProfile()
}

// IsBird returns true if the profile uses bird-default settings.
func (fp FrequencyProfile) IsBird() bool {
	return fp.ResampleRate > 0
}
