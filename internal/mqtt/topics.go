// topics.go: MQTT state topic construction shared by publishers and HA discovery.
package mqtt

import "strings"

// Topic segments appended to the configured base topic.
const (
	// soundLevelTopicSegment names the sound level topic, both the shared one
	// under the base topic and the per-source one under a source's subtree.
	soundLevelTopicSegment = "soundlevel"

	// sourcesTopicSegment is the subtree holding one state topic per audio source.
	sourcesTopicSegment = "sources"
)

// trimBaseTopic strips trailing slashes so joined topics never contain an
// empty level ("birdnet//soundlevel").
func trimBaseTopic(baseTopic string) string {
	return strings.TrimRight(baseTopic, "/")
}

// SoundLevelTopic returns the shared sound level topic that carries every
// source's sound level data, e.g. "birdnet/soundlevel".
func SoundLevelTopic(baseTopic string) string {
	return trimBaseTopic(baseTopic) + "/" + soundLevelTopicSegment
}

// SourceDetectionTopic returns the per-source detection topic, e.g.
// "birdnet/sources/rtsp_abc123". It carries the same payload as the shared
// base topic, but only for detections from sourceID.
//
// Home Assistant discovery points each source's sensors here. A template on
// an HA MQTT sensor cannot ignore a message: rendering None sets the state to
// unknown. On a shared topic, every detection from one source would therefore
// clear the sensors of all other sources (GitHub #4349). A dedicated topic per
// source removes the need to filter at all.
//
// sourceID is the raw audio source ID (the payload's sourceId field), passed
// through SanitizeID so it is always a single, wildcard-free topic level.
func SourceDetectionTopic(baseTopic, sourceID string) string {
	return trimBaseTopic(baseTopic) + "/" + sourcesTopicSegment + "/" + SanitizeID(sourceID)
}

// SourceSoundLevelTopic returns the per-source sound level topic, e.g.
// "birdnet/sources/rtsp_abc123/soundlevel". See SourceDetectionTopic.
func SourceSoundLevelTopic(baseTopic, sourceID string) string {
	return SourceDetectionTopic(baseTopic, sourceID) + "/" + soundLevelTopicSegment
}
