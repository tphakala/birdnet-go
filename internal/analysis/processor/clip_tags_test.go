package processor

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestBuildClipTags_FullFieldSetInOrder(t *testing.T) {
	t.Parallel()
	meta := clipTagMeta{
		commonName:     "Eurasian Wren",
		scientificName: "Troglodytes troglodytes",
		confidence:     0.95,
		detectedAt:     time.Date(2026, 5, 31, 8, 38, 28, 0, time.FixedZone("EEST", 3*3600)),
	}
	got := buildClipTags(meta, "BirdNET", "2.4", "BirdNET-Go 1.2.3")
	assert.Equal(t, []flac.Tag{
		{Name: "TITLE", Value: "Eurasian Wren (Troglodytes troglodytes)"},
		{Name: "DATE", Value: "2026-05-31T08:38:28+03:00"},
		{Name: "ENCODER", Value: "BirdNET-Go 1.2.3"},
		{Name: "BIRDNET_COMMON_NAME", Value: "Eurasian Wren"},
		{Name: "BIRDNET_SCIENTIFIC_NAME", Value: "Troglodytes troglodytes"},
		{Name: "BIRDNET_CONFIDENCE", Value: "0.95"},
		{Name: "BIRDNET_MODEL", Value: "BirdNET"},
		{Name: "BIRDNET_MODEL_VERSION", Value: "2.4"},
	}, got)
}

func TestBuildClipTags_OmitsEmptyFields(t *testing.T) {
	t.Parallel()
	got := buildClipTags(clipTagMeta{confidence: math.NaN()}, "", "", "BirdNET-Go unknown")
	assert.Equal(t, []flac.Tag{{Name: "ENCODER", Value: "BirdNET-Go unknown"}}, got)

	for _, tag := range buildClipTags(clipTagMeta{commonName: "Wren"}, "", "", "") {
		assert.NotEmpty(t, tag.Value, tag.Name)
	}
}

func TestBuildClipTags_Title(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, common, sci, want string
	}{
		{"both", "Wren", "T. troglodytes", "Wren (T. troglodytes)"},
		{"common only", "Wren", "", "Wren"},
		{"scientific only", "", "T. troglodytes", "T. troglodytes"},
		{"equal", "Same", "Same", "Same"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildClipTags(clipTagMeta{commonName: tt.common, scientificName: tt.sci}, "", "", "")
			require.NotEmpty(t, got)
			assert.Equal(t, flac.Tag{Name: "TITLE", Value: tt.want}, got[0])
		})
	}
}

func TestBuildClipTags_Confidence(t *testing.T) {
	t.Parallel()
	find := func(tags []flac.Tag) (string, bool) {
		for _, tag := range tags {
			if tag.Name == "BIRDNET_CONFIDENCE" {
				return tag.Value, true
			}
		}
		return "", false
	}
	for conf, want := range map[float64]string{0.95: "0.95", 1: "1.00", 0.5: "0.50"} {
		got, ok := find(buildClipTags(clipTagMeta{confidence: conf}, "", "", ""))
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1)} {
		_, ok := find(buildClipTags(clipTagMeta{confidence: bad}, "", "", ""))
		assert.False(t, ok, "confidence %v must be omitted", bad)
	}
}

func TestClipVendor(t *testing.T) {
	t.Parallel()
	assert.Equal(t, branding.Name()+" unknown", clipVendor(nil))
	assert.Equal(t, branding.Name()+" unknown", clipVendor(&conf.Settings{}))
	assert.Equal(t, branding.Name()+" 1.2.3", clipVendor(&conf.Settings{Version: "1.2.3"}))
}

// TestClipTags_ExcludeLocationSourceAndStation drives the real plumbing: values
// that must never reach a published clip are set on the detection, and none may
// appear in any tag.
func TestClipTags_ExcludeLocationSourceAndStation(t *testing.T) {
	const lat, lon = 60.123456, 24.654321
	const sourceID = "rtsp://user:pass@cam.example/stream"
	sentinels := []string{
		fmt.Sprintf("%v", lat), fmt.Sprintf("%.4f", lat),
		fmt.Sprintf("%v", lon), fmt.Sprintf("%.4f", lon),
		"station-node-sentinel", "cam.example", "user:pass",
		"Front Yard Camera", "safe-source-sentinel", "comment-sentinel",
	}

	newDetection := func(begin, end time.Time) *Detections {
		return &Detections{
			CorrelationID: "privacy-test",
			Result: detection.Result{
				Species:     detection.Species{CommonName: "Eurasian Wren", ScientificName: "Troglodytes troglodytes"},
				Confidence:  0.87,
				Timestamp:   begin,
				Model:       detection.ModelInfo{Name: "BirdNET", Version: "2.4"},
				ClipName:    "2026/07/clip.wav",
				BeginTime:   begin,
				EndTime:     end,
				Latitude:    lat,
				Longitude:   lon,
				SourceNode:  "station-node-sentinel",
				AudioSource: detection.AudioSource{ID: sourceID, SafeString: "safe-source-sentinel", DisplayName: "Front Yard Camera", SampleRate: conf.SampleRate},
				Comments:    []detection.Comment{{Entry: "comment-sentinel"}},
			},
		}
	}
	allowed := map[string]bool{
		clipTagTitle: true, clipTagDate: true, clipTagEncoder: true,
		clipTagCommonName: true, clipTagScientificName: true, clipTagConfidence: true,
		clipTagModel: true, clipTagModelVersion: true,
	}

	for _, tt := range saveAudioBranchCases(sourceID, newDetection) {
		t.Run(tt.name, func(t *testing.T) {
			p, det := tt.build(t)
			action := p.buildSaveAudioAction(det, &DetectionContext{})
			require.NotNil(t, action)
			tags := buildClipTags(action.clipTags, action.modelName, action.modelVersion, clipVendor(action.Settings))
			require.NotEmpty(t, tags)
			for _, tag := range tags {
				assert.True(t, allowed[tag.Name], "unexpected tag name %q", tag.Name)
				for _, s := range sentinels {
					assert.NotContains(t, tag.Value, s, "tag %s leaks a forbidden value", tag.Name)
				}
			}
		})
	}
}
