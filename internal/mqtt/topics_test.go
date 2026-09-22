package mqtt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSoundLevelTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		baseTopic string
		want      string
	}{
		{name: "plain base", baseTopic: "birdnet", want: "birdnet/soundlevel"},
		{name: "nested base", baseTopic: "home/birdnet", want: "home/birdnet/soundlevel"},
		{name: "trailing slash", baseTopic: "birdnet/", want: "birdnet/soundlevel"},
		{name: "repeated trailing slashes", baseTopic: "birdnet//", want: "birdnet/soundlevel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SoundLevelTopic(tt.baseTopic))
		})
	}
}

func TestSourceDetectionTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		baseTopic string
		sourceID  string
		want      string
	}{
		{name: "rtsp source", baseTopic: "birdnet", sourceID: "rtsp_65c31a0b", want: "birdnet/sources/rtsp_65c31a0b"},
		{name: "trailing slash on base", baseTopic: "birdnet/", sourceID: "rtsp_65c31a0b", want: "birdnet/sources/rtsp_65c31a0b"},
		{name: "nested base", baseTopic: "home/birdnet", sourceID: "audiocard_1", want: "home/birdnet/sources/audiocard_1"},
		// A raw ALSA device ID must not add topic levels.
		{name: "slash in source ID", baseTopic: "birdnet", sourceID: "hw:0/0", want: "birdnet/sources/hw_0_0"},
		// MQTT wildcards are illegal in a publish topic and must be removed.
		{name: "wildcards in source ID", baseTopic: "birdnet", sourceID: "mic+#1", want: "birdnet/sources/mic_1"},
		{name: "empty source ID", baseTopic: "birdnet", sourceID: "", want: "birdnet/sources/unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SourceDetectionTopic(tt.baseTopic, tt.sourceID))
		})
	}
}

func TestSourceSoundLevelTopic(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "birdnet/sources/rtsp_65c31a0b/soundlevel",
		SourceSoundLevelTopic("birdnet/", "rtsp_65c31a0b"))
	assert.Equal(t, "birdnet/sources/hw_0_0/soundlevel",
		SourceSoundLevelTopic("birdnet", "hw:0,0"))
}

// TestSourceTopics_DistinctPerSource guards the core of GitHub #4349: two
// sources must never share a state topic, or one source's messages would reach
// the other source's Home Assistant sensors.
func TestSourceTopics_DistinctPerSource(t *testing.T) {
	t.Parallel()

	const base = "birdnet"
	a, b := "rtsp_aaaa1111", "rtsp_bbbb2222"

	assert.NotEqual(t, SourceDetectionTopic(base, a), SourceDetectionTopic(base, b))
	assert.NotEqual(t, SourceSoundLevelTopic(base, a), SourceSoundLevelTopic(base, b))
	assert.NotEqual(t, base, SourceDetectionTopic(base, a),
		"per-source topic must differ from the shared detection topic")
	assert.NotEqual(t, SoundLevelTopic(base), SourceSoundLevelTopic(base, a),
		"per-source sound level topic must differ from the shared one")
}
