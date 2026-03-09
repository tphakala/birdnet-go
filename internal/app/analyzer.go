package app

// Analyzer processes audio frames from compatible sources.
// This is a stub interface for multi-model support (BirdNET, Perch v2, bat models).
// For now, only BirdNETAnalyzer will implement this.
type Analyzer interface {
	Service
	// Compatible returns true if this analyzer can process audio from the given source.
	Compatible(source AudioSource) bool
}

// AudioSource describes an audio input device or stream.
type AudioSource struct {
	ID         string
	Name       string
	Type       SourceType
	SampleRate int
	BitDepth   int
}

// SourceType enumerates audio source categories.
type SourceType int

const (
	// SourceTypeAudioCard is a local sound card input.
	SourceTypeAudioCard SourceType = iota
	// SourceTypeRTSP is an RTSP network stream.
	SourceTypeRTSP
	// SourceTypeUltrasonic is an ultrasonic-capable device (for bat detection).
	SourceTypeUltrasonic
)

// String returns the human-readable name of the source type.
func (s SourceType) String() string {
	switch s {
	case SourceTypeAudioCard:
		return "audio_card"
	case SourceTypeRTSP:
		return "rtsp"
	case SourceTypeUltrasonic:
		return "ultrasonic"
	default:
		return "unknown"
	}
}

// Router maps audio sources to compatible analyzers.
type Router struct {
	analyzers []Analyzer
}

// NewRouter creates a Router with the given analyzers.
func NewRouter(analyzers []Analyzer) *Router {
	return &Router{analyzers: analyzers}
}

// AnalyzersFor returns all analyzers compatible with the given source.
func (r *Router) AnalyzersFor(src AudioSource) []Analyzer {
	result := make([]Analyzer, 0, len(r.analyzers))
	for _, a := range r.analyzers {
		if a.Compatible(src) {
			result = append(result, a)
		}
	}
	return result
}
