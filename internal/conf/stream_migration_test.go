package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettings_MigrateRTSPConfig(t *testing.T) {
	tests := []struct {
		name           string
		settings       Settings
		expectMigrated bool
		expectedCount  int
		expectedNames  []string
	}{
		{
			name: "migrate legacy URLs to streams",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{"rtsp://192.168.1.10/stream", "rtsp://192.168.1.20/stream"},
						Transport: "tcp",
					},
				},
			},
			expectMigrated: true,
			expectedCount:  2,
			expectedNames:  []string{"Stream 1", "Stream 2"},
		},
		{
			name: "skip migration if streams already exist",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						Streams: []StreamConfig{
							{Name: "Existing", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
						},
						URLs: []string{"rtsp://old.url/stream"}, // Should be ignored
					},
				},
			},
			expectMigrated: false,
			expectedCount:  1,
			expectedNames:  []string{"Existing"},
		},
		{
			name: "skip migration if no legacy URLs",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs: []string{},
					},
				},
			},
			expectMigrated: false,
			expectedCount:  0,
		},
		{
			name: "use default transport if not specified",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{"rtsp://192.168.1.10/stream"},
						Transport: "", // Empty should default to tcp
					},
				},
			},
			expectMigrated: true,
			expectedCount:  1,
		},
		{
			name: "preserve udp transport from legacy",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{"rtsp://192.168.1.10/stream"},
						Transport: "udp",
					},
				},
			},
			expectMigrated: true,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			migrated := tt.settings.MigrateRTSPConfig()

			assert.Equal(t, tt.expectMigrated, migrated)
			assert.Len(t, tt.settings.Realtime.RTSP.Streams, tt.expectedCount)

			if tt.expectMigrated {
				// Verify legacy fields are cleared
				assert.Empty(t, tt.settings.Realtime.RTSP.URLs)
				assert.Empty(t, tt.settings.Realtime.RTSP.Transport)
			}

			// Verify names if expected
			for i, expectedName := range tt.expectedNames {
				require.Greater(t, len(tt.settings.Realtime.RTSP.Streams), i)
				assert.Equal(t, expectedName, tt.settings.Realtime.RTSP.Streams[i].Name)
			}
		})
	}
}

func TestSettings_MigrateRTSPConfig_StreamProperties(t *testing.T) {
	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				URLs:      []string{"rtsp://user:pass@192.168.1.10:554/stream1"},
				Transport: "udp",
			},
		},
	}

	migrated := settings.MigrateRTSPConfig()
	require.True(t, migrated)
	require.Len(t, settings.Realtime.RTSP.Streams, 1)

	stream := settings.Realtime.RTSP.Streams[0]
	assert.Equal(t, "Stream 1", stream.Name)
	assert.Equal(t, "rtsp://user:pass@192.168.1.10:554/stream1", stream.URL)
	assert.Equal(t, StreamTypeRTSP, stream.Type)
	assert.Equal(t, "udp", stream.Transport)
}
