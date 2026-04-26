package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindStreamByName(t *testing.T) {
	t.Parallel()

	r := &RTSPSettings{
		Streams: []StreamConfig{
			{Name: "Front Yard", URL: "rtsp://front"},
			{Name: "Back Garden", URL: "rtsp://back"},
		},
	}

	t.Run("match found", func(t *testing.T) {
		t.Parallel()
		s := r.FindStreamByName("Back Garden")
		require.NotNil(t, s)
		assert.Equal(t, "rtsp://back", s.URL)
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		s := r.FindStreamByName("nonexistent")
		assert.Nil(t, s)
	})

	t.Run("empty streams", func(t *testing.T) {
		t.Parallel()
		empty := &RTSPSettings{}
		assert.Nil(t, empty.FindStreamByName("anything"))
	})
}

func TestResolveEQOverride(t *testing.T) {
	t.Parallel()

	audioEQ := &EqualizerSettings{
		Enabled: true,
		Filters: []EqualizerFilter{{Type: "LowPass", Frequency: 8000}},
	}
	streamEQ := &EqualizerSettings{
		Enabled: true,
		Filters: []EqualizerFilter{{Type: "HighPass", Frequency: 200}},
	}

	s := &Settings{}
	s.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Front Mic", Equalizer: audioEQ},
		{Name: "Back Mic", Equalizer: nil},
	}
	s.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "Front Cam", Equalizer: streamEQ},
		{Name: "Back Cam", Equalizer: nil},
	}

	t.Run("audio source with override", func(t *testing.T) {
		t.Parallel()
		got := s.ResolveEQOverride("Front Mic")
		require.NotNil(t, got)
		assert.Equal(t, audioEQ, got)
	})

	t.Run("audio source without override", func(t *testing.T) {
		t.Parallel()
		got := s.ResolveEQOverride("Back Mic")
		assert.Nil(t, got)
	})

	t.Run("stream with override", func(t *testing.T) {
		t.Parallel()
		got := s.ResolveEQOverride("Front Cam")
		require.NotNil(t, got)
		assert.Equal(t, streamEQ, got)
	})

	t.Run("stream without override", func(t *testing.T) {
		t.Parallel()
		got := s.ResolveEQOverride("Back Cam")
		assert.Nil(t, got)
	})

	t.Run("no match returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, s.ResolveEQOverride("unknown-source"))
	})

	t.Run("audio source takes precedence over stream with same name", func(t *testing.T) {
		t.Parallel()
		conflict := &Settings{}
		conflict.Realtime.Audio.Sources = []AudioSourceConfig{
			{Name: "Shared Name", Equalizer: audioEQ},
		}
		conflict.Realtime.RTSP.Streams = []StreamConfig{
			{Name: "Shared Name", Equalizer: streamEQ},
		}
		got := conflict.ResolveEQOverride("Shared Name")
		require.NotNil(t, got)
		assert.Equal(t, audioEQ, got, "audio source EQ should take precedence")
	})
}
