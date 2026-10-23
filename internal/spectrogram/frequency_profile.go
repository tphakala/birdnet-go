package spectrogram

// FrequencyProfile controls spectrogram frequency range and resampling per
// detection. The gate is the detection's model type: bat models keep the clip's
// native capture rate, so the image spans 0 to half of that rate (a 384 kHz clip
// shows 0-192 kHz, a 192 kHz clip 0-96 kHz); everything else gets bird defaults
// (drawn for birdResampleHz, an axis of 0 to 12 kHz).
type FrequencyProfile struct {
	// ResampleRate is the rate in Hz the image is drawn for: the axis spans 0 to
	// half of it. 0 keeps the clip's native rate. The renderer analyses a clip at an
	// accepted multiple of it without resampling (see analysisSource) and resamples
	// any other clip.
	ResampleRate int
	suffix       string // Cache-filename token identifying the profile; "" for the default bird render
}

const (
	birdResampleHz  = 24000
	modelTypeBatStr = "bat" // ai_models.model_type value that selects the bat profile

	// batCacheSuffix is the cache-filename token for bat spectrograms (e.g.
	// "<clip>_1026px-bat-v3.png"). It is intentionally separate from
	// modelTypeBatStr (the stored model-type value) and carries a version marker:
	// bumping it changes the on-disk filename, so a corrected render lands at a new
	// path instead of colliding with a stale bat image cached by an older generator.
	// "-v3" marks the move from a fixed 256 kHz resample (0-128 kHz axis) to the
	// clip's native rate, so every earlier bat render has a different frequency
	// range and must not be served for a current request. Only bat files carry a
	// suffix, so this invalidates just bat spectrograms; bird renders (empty
	// suffix) are untouched. Earlier "-bat-v2" and "-bat" files are removed when a
	// fresh "-bat-v3" render is written (see SupersededSuffixesForToken) and are
	// otherwise reaped when the detection is deleted (the delete scan matches any
	// "<base>_<width>px-" prefix).
	batCacheSuffix = "bat-v3"

	// batCacheSuffixV2 and batCacheSuffixLegacy are the earlier bat tokens whose
	// renders batCacheSuffix supersedes. The legacy token predates versioning.
	batCacheSuffixV2     = "bat-v2"
	batCacheSuffixLegacy = "bat"
)

// SupersededSuffixesForToken returns the older cache tokens that the given
// token replaces, newest first, or nil when it replaces none. The caller may
// delete files carrying those tokens once a render with the current token exists.
func SupersededSuffixesForToken(token string) []string {
	if token == batCacheSuffix {
		return []string{batCacheSuffixV2, batCacheSuffixLegacy}
	}
	return nil
}

// BirdProfile returns the default frequency profile for bird detections.
func BirdProfile() FrequencyProfile {
	return FrequencyProfile{
		ResampleRate: birdResampleHz,
	}
}

// BatProfile returns the frequency profile for bat detections. The audio keeps
// the clip's native capture rate (ResampleRate 0), so the spectrogram spans 0 to
// half of that rate whatever it is (192/256/384 kHz are all supported).
func BatProfile() FrequencyProfile {
	return FrequencyProfile{
		suffix: batCacheSuffix,
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
