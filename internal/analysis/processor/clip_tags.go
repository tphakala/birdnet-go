package processor

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// VORBIS_COMMENT tags embedded in saved FLAC clips.
//
// TITLE, DATE and ENCODER are the standard fields players and tools such as
// ffprobe display. The BIRDNET_ fields carry the same facts in a stable,
// machine-readable form; the prefix is fixed (not branding.Name()) so tag names
// do not change for rebranded builds. DATE is the detection time
// (Result.Timestamp) in that timestamp's own location, normally local time,
// with its UTC offset.
//
// Clips are served by the media API and may be downloaded and shared, so the
// set is deliberately closed. Never add: latitude or longitude, the audio
// source ID, URI or display name, the station or node name, comments, or the
// description.
const (
	clipTagTitle          = "TITLE"
	clipTagDate           = "DATE"
	clipTagEncoder        = "ENCODER"
	clipTagCommonName     = "BIRDNET_COMMON_NAME"
	clipTagScientificName = "BIRDNET_SCIENTIFIC_NAME"
	clipTagConfidence     = "BIRDNET_CONFIDENCE"
	clipTagModel          = "BIRDNET_MODEL"
	clipTagModelVersion   = "BIRDNET_MODEL_VERSION"

	// clipTagConfidenceDecimals matches the 2-decimal rounding applied to the
	// stored confidence.
	clipTagConfidenceDecimals = 2
	// clipTagUnknownVersion is the vendor version when the build version is not set.
	clipTagUnknownVersion = "unknown"
)

// clipTagMeta is the display-ready detection metadata embedded as FLAC tags. It
// must only ever hold privacy-safe fields (see the constants above); a clip
// tag builder takes this struct instead of a detection.Result so that location,
// source and station data cannot reach a tag by accident.
type clipTagMeta struct {
	commonName     string
	scientificName string
	confidence     float64
	detectedAt     time.Time
}

// newClipTagMeta copies exactly the tag-safe fields out of a detection result.
func newClipTagMeta(r *detection.Result) clipTagMeta {
	return clipTagMeta{
		commonName:     r.Species.CommonName,
		scientificName: r.Species.ScientificName,
		confidence:     r.Confidence,
		detectedAt:     r.Timestamp,
	}
}

// clipVendor returns the VORBIS_COMMENT vendor string: the application name and
// the running version, or "unknown" when the version is empty or blank.
func clipVendor(settings *conf.Settings) string {
	version := clipTagUnknownVersion
	if settings != nil {
		if v := strings.TrimSpace(settings.Version); v != "" {
			version = v
		}
	}
	return branding.Name() + " " + version
}

// buildClipTags returns the tags for one clip in a fixed order, omitting every
// field whose value is empty; species names are trimmed first. It is a pure
// function of its arguments.
func buildClipTags(meta clipTagMeta, modelName, modelVersion, encoder string) []flac.Tag {
	var tags []flac.Tag
	add := func(name, value string) {
		if value != "" {
			tags = append(tags, flac.Tag{Name: name, Value: value})
		}
	}

	// Trim once so the title and the name tags agree on blank or padded names.
	common := strings.TrimSpace(meta.commonName)
	scientific := strings.TrimSpace(meta.scientificName)

	add(clipTagTitle, clipTitle(common, scientific))
	if !meta.detectedAt.IsZero() {
		add(clipTagDate, meta.detectedAt.Format(time.RFC3339))
	}
	add(clipTagEncoder, encoder)
	add(clipTagCommonName, common)
	add(clipTagScientificName, scientific)
	if !math.IsNaN(meta.confidence) && !math.IsInf(meta.confidence, 0) {
		add(clipTagConfidence, strconv.FormatFloat(meta.confidence, 'f', clipTagConfidenceDecimals, 64))
	}
	add(clipTagModel, modelName)
	add(clipTagModelVersion, modelVersion)
	return tags
}

// clipTitle composes "Common (Scientific)", or whichever name exists when the
// other is empty or both are equal.
func clipTitle(common, scientific string) string {
	switch {
	case common == "" || common == scientific:
		return scientific
	case scientific == "":
		return common
	default:
		return common + " (" + scientific + ")"
	}
}
