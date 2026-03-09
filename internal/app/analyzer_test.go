package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAnalyzer is a test helper implementing Analyzer.
type mockAnalyzer struct {
	name         string
	compatibleFn func(AudioSource) bool
}

func (m *mockAnalyzer) Name() string                    { return m.name }
func (m *mockAnalyzer) Start(_ context.Context) error   { return nil }
func (m *mockAnalyzer) Stop(_ context.Context) error    { return nil }
func (m *mockAnalyzer) Compatible(src AudioSource) bool { return m.compatibleFn(src) }

func TestRouter_AnalyzersFor(t *testing.T) {
	t.Parallel()

	bird := &mockAnalyzer{
		name:         "birdnet",
		compatibleFn: func(src AudioSource) bool { return src.Type != SourceTypeUltrasonic },
	}
	bat := &mockAnalyzer{
		name:         "bat",
		compatibleFn: func(src AudioSource) bool { return src.Type == SourceTypeUltrasonic },
	}

	router := NewRouter([]Analyzer{bird, bat})

	tests := []struct {
		name     string
		source   AudioSource
		expected []string
	}{
		{
			name:     "audio card routes to bird only",
			source:   AudioSource{Type: SourceTypeAudioCard},
			expected: []string{"birdnet"},
		},
		{
			name:     "RTSP routes to bird only",
			source:   AudioSource{Type: SourceTypeRTSP},
			expected: []string{"birdnet"},
		},
		{
			name:     "ultrasonic routes to bat only",
			source:   AudioSource{Type: SourceTypeUltrasonic},
			expected: []string{"bat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := router.AnalyzersFor(tt.source)
			require.Len(t, got, len(tt.expected))
			for i, a := range got {
				assert.Equal(t, tt.expected[i], a.Name())
			}
		})
	}
}

func TestRouter_NoCompatibleAnalyzers(t *testing.T) {
	t.Parallel()
	router := NewRouter([]Analyzer{})
	got := router.AnalyzersFor(AudioSource{Type: SourceTypeAudioCard})
	assert.Empty(t, got)
}

func TestSourceType_String(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "audio_card", SourceTypeAudioCard.String())
	assert.Equal(t, "rtsp", SourceTypeRTSP.String())
	assert.Equal(t, "ultrasonic", SourceTypeUltrasonic.String())
	assert.Equal(t, "unknown", SourceType(99).String())
}
