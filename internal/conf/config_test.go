package conf

import "testing"

func TestAudioSettings_NeedsFfprobeWorkaround(t *testing.T) {
	tests := []struct {
		name        string
		audio       AudioSettings
		wantWorkaround bool
	}{
		{
			name: "FFmpeg 5.x needs workaround",
			audio: AudioSettings{
				FfmpegVersion: "5.1.7-0+deb12u1+rpt1",
				FfmpegMajor:   5,
				FfmpegMinor:   1,
			},
			wantWorkaround: true,
		},
		{
			name: "FFmpeg 7.x does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "7.1.2-0+deb13u1",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
			},
			wantWorkaround: false,
		},
		{
			name: "FFmpeg 6.x does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "6.0",
				FfmpegMajor:   6,
				FfmpegMinor:   0,
			},
			wantWorkaround: false,
		},
		{
			name: "FFmpeg 4.x does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "4.4.2",
				FfmpegMajor:   4,
				FfmpegMinor:   4,
			},
			wantWorkaround: false,
		},
		{
			name: "Unknown version does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "",
				FfmpegMajor:   0,
				FfmpegMinor:   0,
			},
			wantWorkaround: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.audio.NeedsFfprobeWorkaround(); got != tt.wantWorkaround {
				t.Errorf("AudioSettings.NeedsFfprobeWorkaround() = %v, want %v", got, tt.wantWorkaround)
			}
		})
	}
}

func TestAudioSettings_HasFfmpegVersion(t *testing.T) {
	tests := []struct {
		name    string
		audio   AudioSettings
		wantHas bool
	}{
		{
			name: "Valid version detected",
			audio: AudioSettings{
				FfmpegVersion: "7.1.2",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
			},
			wantHas: true,
		},
		{
			name: "No version detected",
			audio: AudioSettings{
				FfmpegVersion: "",
				FfmpegMajor:   0,
				FfmpegMinor:   0,
			},
			wantHas: false,
		},
		{
			name: "Version string but no major version",
			audio: AudioSettings{
				FfmpegVersion: "unknown",
				FfmpegMajor:   0,
				FfmpegMinor:   0,
			},
			wantHas: false,
		},
		{
			name: "Major version but no version string",
			audio: AudioSettings{
				FfmpegVersion: "",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
			},
			wantHas: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.audio.HasFfmpegVersion(); got != tt.wantHas {
				t.Errorf("AudioSettings.HasFfmpegVersion() = %v, want %v", got, tt.wantHas)
			}
		})
	}
}
