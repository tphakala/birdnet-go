package analysis

import (
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// toSoundLevel converts a myaudio.SoundLevelData to the audiocore soundlevel
// equivalent. This is a transitional helper used while the analysis package
// migrates from myaudio types to audiocore types. The two structs are
// structurally identical; this function copies fields one-to-one.
func toSoundLevel(src myaudio.SoundLevelData) soundlevel.SoundLevelData {
	bands := make(map[string]soundlevel.OctaveBandData, len(src.OctaveBands))
	for k, v := range src.OctaveBands {
		bands[k] = soundlevel.OctaveBandData{
			CenterFreq:  v.CenterFreq,
			Min:         v.Min,
			Max:         v.Max,
			Mean:        v.Mean,
			SampleCount: v.SampleCount,
		}
	}
	return soundlevel.SoundLevelData{
		Timestamp:   src.Timestamp,
		Source:      src.Source,
		Name:        src.Name,
		Duration:    src.Duration,
		OctaveBands: bands,
	}
}

// fromSoundLevel converts a soundlevel.SoundLevelData back to the myaudio
// equivalent. This is a transitional helper for boundaries where downstream
// code (e.g., the API v2 package) still expects myaudio types.
func fromSoundLevel(src soundlevel.SoundLevelData) myaudio.SoundLevelData {
	bands := make(map[string]myaudio.OctaveBandData, len(src.OctaveBands))
	for k, v := range src.OctaveBands {
		bands[k] = myaudio.OctaveBandData{
			CenterFreq:  v.CenterFreq,
			Min:         v.Min,
			Max:         v.Max,
			Mean:        v.Mean,
			SampleCount: v.SampleCount,
		}
	}
	return myaudio.SoundLevelData{
		Timestamp:   src.Timestamp,
		Source:      src.Source,
		Name:        src.Name,
		Duration:    src.Duration,
		OctaveBands: bands,
	}
}

// fromSoundLevelPtr is a convenience wrapper around fromSoundLevel that
// returns a pointer, matching the signature expected by BroadcastSoundLevel.
func fromSoundLevelPtr(src soundlevel.SoundLevelData) *myaudio.SoundLevelData {
	out := fromSoundLevel(src)
	return &out
}
