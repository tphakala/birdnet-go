package spectrogram

// FrequencyProfile controls spectrogram frequency range and resampling per
// detection. The gate is the detection's model type: bat models are resampled
// to batResampleHz (Nyquist = 128 kHz) so the fixed 0-128 kHz axis in the UI
// is always accurate regardless of the original capture rate; everything else
// gets bird defaults (resample to birdResampleHz).
type FrequencyProfile struct {
	ResampleRate int    // Target sample rate in Hz; 0 means keep native rate
	suffix       string // Cache-filename token identifying the profile; "" for the default bird render
}

const (
	birdResampleHz  = 24000
	batResampleHz   = 256000 // Nyquist = 128 kHz; matches the fixed 0-128 kHz overlay axis
	modelTypeBatStr = "bat"  // ai_models.model_type value that selects the bat profile

	// batCacheSuffix is the cache-filename token for bat spectrograms (e.g.
	// "<clip>_1026px-bat-v2.png"). It is intentionally separate from
	// modelTypeBatStr (the stored model-type value) and carries a version marker:
	// bumping it changes the on-disk filename, so a corrected render lands at a new
	// path instead of colliding with a stale bat image cached by an older generator.
	// Bumped to "-v2" alongside the FFmpeg-only fallback resample fix (#3689): bat
	// clips rendered via the Sox-failure fallback before that fix carry a frequency
	// axis that disagrees with the 0-128 kHz overlay, and would otherwise be served
	// from cache indefinitely. Only bat files carry a suffix, so this invalidates
	// just bat spectrograms; bird renders (empty suffix) are untouched. Pre-bump
	// "-bat.png" files are left on disk (lazily superseded by the new "-bat-v2"
	// render) and reaped when the detection is deleted (the delete scan matches any
	// "<base>_<width>px-" prefix), so the only cost is a tiny transient orphan.
	batCacheSuffix = "bat-v2"
)

// BirdProfile returns the default frequency profile for bird detections.
func BirdProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: birdResampleHz,
	}
}

// BatProfile returns the frequency profile for bat detections. The audio is
// resampled to batResampleHz (256 kHz, Nyquist = 128 kHz) so the spectrogram
// always spans the full 0-128 kHz range that the UI axis hardcodes,
// regardless of the original capture rate (192/256/384 kHz are all supported).
func BatProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: batResampleHz,
		suffix:       batCacheSuffix,
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
