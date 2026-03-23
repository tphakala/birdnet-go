package analysis

import (
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// toSoundLevel converts a myaudio.SoundLevelData to the audiocore soundlevel
// equivalent. This is a transitional helper used while the analysis package
// migrates from myaudio types to audiocore types. The two structs are
// structurally identical; this function copies fields one-to-one.
//
// TODO: Remove once the myaudio package is fully replaced by audiocore.
// All callers should be migrated to use soundlevel.SoundLevelData directly.
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
