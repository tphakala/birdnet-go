package spectrogram

// FrequencyProfile controls spectrogram frequency range and resampling per
// detection. The gate is the detection's model type: bat models are resampled
// to batResampleHz (Nyquist = 128 kHz) so the fixed 0–128 kHz axis in the UI
// is always accurate regardless of the original capture rate; everything else
// gets bird defaults (resample to birdResampleHz).
type FrequencyProfile struct {
	ResampleRate int    // Target sample rate in Hz; 0 means keep native rate
	suffix       string // Cache-filename token identifying the profile; "" for the default bird render
}

const (
	birdResampleHz  = 24000
	batResampleHz   = 256000 // Nyquist = 128 kHz; matches the fixed 0–128 kHz overlay axis
	modelTypeBatStr = "bat"
)

// BirdProfile returns the default frequency profile for bird detections.
func BirdProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: birdResampleHz,
	}
}

// BatProfile returns the frequency profile for bat detections. The audio is
// resampled to batResampleHz (256 kHz, Nyquist = 128 kHz) so the spectrogram
// always spans the full 0–128 kHz range that the UI axis hardcodes,
// regardless of the original capture rate (192/256/384 kHz are all supported).
func BatProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: batResampleHz,
		suffix:       modelTypeBatStr,
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
