package spectrogram

// FrequencyProfile controls spectrogram frequency range and resampling per
// detection. The gate is the detection's model type: bat models keep the native
// sample rate with no resampling and no filtering, so the entire recorded
// frequency range is rendered; everything else gets bird defaults (resample to
// 24 kHz).
type FrequencyProfile struct {
	ResampleRate int    // Target sample rate in Hz; 0 means keep native rate
	HighPassHz   int    // High-pass filter cutoff in Hz; 0 means no filter
	suffix       string // Cache-filename token identifying the profile; "" for the default bird render
}

const (
	birdResampleHz  = 24000
	modelTypeBatStr = "bat"
)

// BirdProfile returns the default frequency profile for bird detections.
func BirdProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: birdResampleHz,
	}
}

// BatProfile returns the frequency profile for bat detections. Bat audio is
// captured at a high native sample rate; the spectrogram keeps that rate (no
// resampling) and applies no high-pass filter, so the entire recorded band -
// including low-frequency bat calls - is rendered.
func BatProfile() FrequencyProfile {
	return FrequencyProfile{
		suffix: modelTypeBatStr,
	}
}

// ProfileForModelType selects the appropriate frequency profile based on the
// AI model's type string (as stored in ai_models.model_type).
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
	return p.suffix
}
