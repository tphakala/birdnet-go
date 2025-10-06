package conf

import (
	"testing"
)

func TestParseFfmpegVersion(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		wantVersion   string
		wantMajor     int
		wantMinor     int
	}{
		{
			name: "FFmpeg 7.1.2 Debian",
			output: `ffmpeg version 7.1.2-0+deb13u1 Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 14 (Debian 14.2.0-19)`,
			wantVersion: "7.1.2-0+deb13u1",
			wantMajor:   7,
			wantMinor:   1,
		},
		{
			name: "FFmpeg 5.1.7 Raspberry Pi",
			output: `ffmpeg version 5.1.7-0+deb12u1+rpt1 Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 12 (Debian 12.2.0-14+deb12u1)`,
			wantVersion: "5.1.7-0+deb12u1+rpt1",
			wantMajor:   5,
			wantMinor:   1,
		},
		{
			name: "FFmpeg 6.0",
			output: `ffmpeg version 6.0 Copyright (c) 2000-2023 the FFmpeg developers
built with gcc 11.3.0`,
			wantVersion: "6.0",
			wantMajor:   6,
			wantMinor:   0,
		},
		{
			name: "FFmpeg 4.4.2",
			output: `ffmpeg version 4.4.2-2ubuntu1 Copyright (c) 2000-2022 the FFmpeg developers
built with gcc 11 (Ubuntu 11.2.0-19ubuntu1)`,
			wantVersion: "4.4.2-2ubuntu1",
			wantMajor:   4,
			wantMinor:   4,
		},
		{
			name:          "Empty output",
			output:        "",
			wantVersion:   "",
			wantMajor:     0,
			wantMinor:     0,
		},
		{
			name:          "Invalid format",
			output:        "some random text",
			wantVersion:   "",
			wantMajor:     0,
			wantMinor:     0,
		},
		{
			name: "FFmpeg git build with libavutil",
			output: `ffmpeg version N-121000-g7321e4b950 Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 11.4.0 (Ubuntu 11.4.0-1ubuntu1~22.04)
configuration: --prefix=/usr/local
libavutil      59.  8.100 / 59.  8.100
libavcodec     61.  3.100 / 61.  3.100
libavformat    61.  1.100 / 61.  1.100`,
			wantVersion: "N-121000-g7321e4b950",
			wantMajor:   7,
			wantMinor:   8,
		},
		{
			name: "FFmpeg 8.0 Windows (gyan.dev build)",
			output: `ffmpeg version 8.0-essentials_build-www.gyan.dev Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 15.2.0 (Rev8, Built by MSYS2 project)
configuration: --enable-gpl --enable-version3
libavutil      60.  8.100 / 60.  8.100
libavcodec     62. 11.100 / 62. 11.100
libavformat    62.  3.100 / 62.  3.100`,
			wantVersion: "8.0-essentials_build-www.gyan.dev",
			wantMajor:   8,
			wantMinor:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotMajor, gotMinor := ParseFfmpegVersion(tt.output)

			if gotVersion != tt.wantVersion {
				t.Errorf("ParseFfmpegVersion() version = %v, want %v", gotVersion, tt.wantVersion)
			}
			if gotMajor != tt.wantMajor {
				t.Errorf("ParseFfmpegVersion() major = %v, want %v", gotMajor, tt.wantMajor)
			}
			if gotMinor != tt.wantMinor {
				t.Errorf("ParseFfmpegVersion() minor = %v, want %v", gotMinor, tt.wantMinor)
			}
		})
	}
}

func TestGetFfmpegVersion(t *testing.T) {
	// This test will only work if ffmpeg is installed on the system
	version, major, minor := GetFfmpegVersion()

	// If ffmpeg is not available, the function should return empty values
	if version == "" {
		t.Skip("ffmpeg not available on system, skipping integration test")
	}

	// If we got a version, validate it has sensible values
	// Note: For git builds, major version is derived from libavutil, so it should be valid
	if major < 3 || major > 10 {
		t.Errorf("GetFfmpegVersion() major version %d seems out of reasonable range (3-10 expected)", major)
	}

	if minor < 0 || minor > 99 {
		t.Errorf("GetFfmpegVersion() minor version %d seems out of reasonable range (0-99 expected)", minor)
	}

	// Additional validation: if major is 0, something went wrong
	if major == 0 {
		t.Errorf("GetFfmpegVersion() failed to detect major version, got: version=%s, major=%d, minor=%d", version, major, minor)
	}

	t.Logf("Detected FFmpeg version: %s (major: %d, minor: %d)", version, major, minor)
}
