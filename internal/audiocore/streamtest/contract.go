package streamtest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// Contract timing and shape constants. Timeouts are generous so the suite stays
// green on slow CI while still bounding each behaviour; the cases that record a
// baseline log the value they actually measured (grep BASELINE in test output).
const (
	// defaultTargetSampleRate is the analysis rate BirdNET-Go feeds its model.
	defaultTargetSampleRate = 48000

	// highSampleRate is the bat-path rate exercised by HighRatePassthrough.
	highSampleRate = 96000

	// s16BytesPerSample and monoChannels describe the dispatched PCM geometry.
	s16BytesPerSample = 2
	monoChannels      = 1
	targetBitDepth    = 16

	// maxFrameBytes is the largest frame the FFmpeg reader emits (its 32 KiB
	// stdout buffer); every producer must stay at or below it.
	maxFrameBytes = 32768

	// defaultHealthyThreshold keeps the healthy/unhealthy transition fast under
	// test instead of the 60 s production default.
	defaultHealthyThreshold = 8 * time.Second

	// silenceTimeoutUnderTest shortens the producer silence watchdog so
	// SilenceRestart completes in seconds.
	silenceTimeoutUnderTest = 5 * time.Second

	// Timing budgets for the individual cases.
	healthyWithinBudget    = 20 * time.Second
	firstFrameBudget       = 20 * time.Second
	fidelityCollectWindow  = 2 * time.Second
	dataRateWindow         = 10 * time.Second
	silenceRestartBudget   = 25 * time.Second
	serverGoneStopFor      = 20 * time.Second
	serverReconnectBudget  = 75 * time.Second
	errorClarityBudget     = 35 * time.Second
	receivingFalseBudget   = 12 * time.Second
	shutdownTimeout        = 15 * time.Second
	pollInterval           = 250 * time.Millisecond
	streamStartSettle      = 500 * time.Millisecond
	dominantFreqToleranceR = 0.02 // dominant frequency must be within 2% of the tone
	toneFrequencyHz        = 1000.0
	nonSilentFloorDBFS     = -40.0           // dispatched audio must be clearly above this
	dataRateTolerance      = 0.10            // dispatched byte rate within 10% of nominal
	healthRateTolerance    = 0.15            // health BytesPerSecond vs nominal (looser: sampled)
	g711SampleRate         = 8000            // G.711 is defined at 8 kHz mono
	healthyObserveWindow   = 3 * time.Second // steady-state observation window
)

// StreamSpec is the protocol-neutral per-stream configuration the contract suite
// hands to a producer's StartStream. It mirrors the fields the engine already
// builds for the FFmpeg producer today; Phase 1 promotes an equivalent type into
// the audiocore package. Producer-specific settings (FFmpeg binary path, log
// level) are the factory's concern, not this struct's.
type StreamSpec struct {
	SourceID             string
	SourceName           string
	URL                  string
	Type                 string // "rtsp", "http", "hls", "udp"
	SampleRate           int    // target output rate (48000, or the bat rate)
	SourceSampleRate     int    // probed source rate, 0 when unknown
	BitDepth             int    // 16
	Channels             int    // target channels (1)
	SourceChannels       int    // probed source channels, 0 when unknown
	ChannelMode          string // downmix, left, right
	MediaMode            string // auto, audio-only, full-stream
	Transport            string // tcp, udp (RTSP only)
	HealthyDataThreshold time.Duration
	Debug                bool
}

// Codec identifies the wire codec a Fixture should publish. The values are the
// FFmpeg encoder names so a MediaMTX-backed fixture can pass them straight
// through; a producer's contract run lists the subset it supports.
type Codec string

const (
	CodecOpus Codec = "opus"      // libopus, 48 kHz
	CodecPCMU Codec = "pcm_mulaw" // G.711 mu-law, 8 kHz
	CodecPCMA Codec = "pcm_alaw"  // G.711 A-law, 8 kHz
	CodecAAC  Codec = "aac"       // AAC-LC
	CodecL16  Codec = "pcm_s16be" // L16 big-endian PCM
)

// Health is a read-only view of a producer's per-stream health snapshot. Both
// producers adapt their concrete health type to it so the suite reads the same
// fields regardless of implementation.
type Health interface {
	IsHealthy() bool
	IsReceivingData() bool
	RestartCount() int
	TotalBytesReceived() int64
	BytesPerSecond() float64
	LastDataReceived() time.Time
	SourceChannels() int
	// ProcessState is the legacy process_state string the health API and
	// frontend switch on; it must always be one of ContractConfig.ValidProcessStates.
	ProcessState() string
	// ErrorType is LastErrorContext.ErrorType, or "" when no error is recorded.
	ErrorType() string
	// StateHistoryLen reports how many lifecycle transitions the producer has
	// retained, so the suite can confirm transitions were recorded.
	StateHistoryLen() int
}

// Manager is the minimal producer contract the characterization suite drives. It
// is the de facto surface the engine calls on ffmpeg.Manager today (spec section
// 2.2), narrowed to what the suite needs. Producers satisfy it through a thin
// adapter so the SAME suite runs unchanged against stream.Manager later.
type Manager interface {
	// StartStream begins capturing spec.SourceID and dispatching its frames.
	StartStream(spec *StreamSpec) error
	// StopStream stops capture for sourceID and forgets it.
	StopStream(sourceID string) error
	// StreamHealth returns a point-in-time health snapshot for one stream.
	StreamHealth(sourceID string) (Health, error)
	// AllStreamHealth returns snapshots for every tracked stream, keyed by source ID.
	AllStreamHealth() map[string]Health
	// GetActiveStreamIDs lists the currently tracked source IDs.
	GetActiveStreamIDs() []string
	// SetOnStreamReset registers the callback invoked when a stream is (re)started.
	SetOnStreamReset(fn func(sourceID string))
	// Shutdown stops every stream with the producer's default timeout.
	Shutdown() error
	// ShutdownWithContext stops every stream, honouring ctx's deadline.
	ShutdownWithContext(ctx context.Context) error
}

// FactoryConfig carries the wiring a producer needs to build a Manager that
// dispatches into the suite's frame collector.
type FactoryConfig struct {
	// OnFrame receives every dispatched frame; the suite passes its collector.
	OnFrame func(frame audiocore.AudioFrame)
	// OnReset receives every stream reset notification; may be nil.
	OnReset func(sourceID string)
	// BufferManager, when non-nil, must make the producer attach a pooled
	// FrameRef to each frame; when nil, frames carry a nil Ref.
	BufferManager *buffer.Manager
	// SilenceTimeout overrides the producer silence watchdog; 0 keeps its default.
	SilenceTimeout time.Duration
}

// ManagerFactory builds a Manager from a FactoryConfig. Supplying a different
// factory is the only change needed to run the suite against another producer.
type ManagerFactory func(t *testing.T, cfg FactoryConfig) Manager

// PublishOptions describe a stream a Fixture should start publishing.
type PublishOptions struct {
	Path       string // stream path; empty means the fixture picks a unique one
	Codec      Codec
	SampleRate int
	Channels   int
	WithVideo  bool
}

// Publication is a live publisher the suite reads from. Stop halts just this
// publisher while the server stays up; Restart brings it back.
type Publication interface {
	URL() string
	Stop(t *testing.T)
	Restart(t *testing.T)
}

// Fixture provides live streams and server control for the contract suite. The
// MediaMTX-backed implementation lives alongside the suite behind the
// integration build tag; a fake-server implementation can back it later.
type Fixture interface {
	// Publish starts a publisher and returns once the stream is live.
	Publish(t *testing.T, opts PublishOptions) Publication
	// URLForPath returns a read URL for an arbitrary (possibly unpublished) path,
	// used to characterize the "path not found" error.
	URLForPath(path string) string
	// UnreachableHostURL returns a URL whose host is routable-but-dead so the
	// connect times out.
	UnreachableHostURL() string
	// RefusedPortURL returns a URL on a live host with a closed port so the
	// connection is refused.
	RefusedPortURL() string
	// BadAuthURL returns a URL with wrong credentials for an auth-protected path,
	// or "" when the fixture does not enforce authentication.
	BadAuthURL(t *testing.T) string
	// StopServer and StartServer stop and restart the whole media server.
	StopServer(t *testing.T)
	StartServer(t *testing.T)
	// SupportsVideo reports whether Publish can add a video track (for MediaModes).
	SupportsVideo() bool
}

// ContractConfig bundles everything RunManagerContract needs.
type ContractConfig struct {
	// Factory builds the producer under test.
	Factory ManagerFactory
	// Fixture supplies live streams and server control.
	Fixture Fixture
	// Codecs is the PCMFidelity matrix the producer supports.
	Codecs []Codec
	// ValidProcessStates is the set of legacy process_state strings the producer
	// may report; the frontend must be able to render each.
	ValidProcessStates []string
	// TargetSampleRate is the analysis rate (defaults to 48000).
	TargetSampleRate int
	// ProducerName labels baseline log lines (e.g. "ffmpeg").
	ProducerName string
}

func (c *ContractConfig) applyDefaults() {
	if c.TargetSampleRate == 0 {
		c.TargetSampleRate = defaultTargetSampleRate
	}
	if c.ProducerName == "" {
		c.ProducerName = "producer"
	}
	if len(c.Codecs) == 0 {
		c.Codecs = []Codec{CodecOpus}
	}
}

// RunManagerContract runs the full characterization suite against the producer
// built by cfg.Factory, using cfg.Fixture for live streams. Each behaviour is a
// named subtest so later phases can report them individually. The subtests run
// sequentially because they share the fixture and some manipulate server state.
func RunManagerContract(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	require.NotNil(t, cfg, "ContractConfig is required")
	require.NotNil(t, cfg.Factory, "ContractConfig.Factory is required")
	require.NotNil(t, cfg.Fixture, "ContractConfig.Fixture is required")
	cfg.applyDefaults()

	t.Run("FrameShape", func(t *testing.T) { caseFrameShape(t, cfg) })
	t.Run("PCMFidelity", func(t *testing.T) { casePCMFidelity(t, cfg) })
	t.Run("DataRate", func(t *testing.T) { caseDataRate(t, cfg) })
	t.Run("Lifecycle", func(t *testing.T) { caseLifecycle(t, cfg) })
	t.Run("OnResetFires", func(t *testing.T) { caseOnResetFires(t, cfg) })
	t.Run("HealthTransitions", func(t *testing.T) { caseHealthTransitions(t, cfg) })
	t.Run("SilenceRestart", func(t *testing.T) { caseSilenceRestart(t, cfg) })
	t.Run("ServerGoneReconnect", func(t *testing.T) { caseServerGoneReconnect(t, cfg) })
	t.Run("MediaModes", func(t *testing.T) { caseMediaModes(t, cfg) })
	t.Run("MultiStream", func(t *testing.T) { caseMultiStream(t, cfg) })
	t.Run("HighRatePassthrough", func(t *testing.T) { caseHighRatePassthrough(t, cfg) })
	t.Run("ErrorClarity", func(t *testing.T) { caseErrorClarity(t, cfg) })
	t.Run("TimeToFirstFrame", func(t *testing.T) { caseTimeToFirstFrame(t, cfg) })
	// Cases 12 (EngineReconfigure) and 13 (LivenessChain) are engine-level and
	// live in internal/audiocore/engine/engine_contract_test.go, so the case
	// numbers in contract_cases.go jump from 11 to 14.
}

// capturedFrame is a copy of a dispatched frame's observable properties. Data is
// copied because a producer may recycle the underlying pooled slice as soon as
// the callback returns.
type capturedFrame struct {
	SourceID   string
	SourceName string
	Data       []byte
	SampleRate int
	BitDepth   int
	Channels   int
	Timestamp  time.Time
	HadRef     bool
	ReceivedAt time.Time
}

// frameCollector records dispatched frames and reset notifications from producer
// goroutines. It never calls t assertions itself (see TESTING.md): the test
// goroutine reads snapshots and asserts.
type frameCollector struct {
	mu     sync.Mutex
	frames []capturedFrame
	resets []string
}

func (c *frameCollector) onFrame(f audiocore.AudioFrame) { //nolint:gocritic // hugeParam: matches FrameCallback/AudioConsumer contract
	data := make([]byte, len(f.Data))
	copy(data, f.Data)
	c.mu.Lock()
	c.frames = append(c.frames, capturedFrame{
		SourceID:   f.SourceID,
		SourceName: f.SourceName,
		Data:       data,
		SampleRate: f.SampleRate,
		BitDepth:   f.BitDepth,
		Channels:   f.Channels,
		Timestamp:  f.Timestamp,
		HadRef:     f.Ref != nil,
		ReceivedAt: time.Now(),
	})
	c.mu.Unlock()
}

func (c *frameCollector) onReset(sourceID string) {
	c.mu.Lock()
	c.resets = append(c.resets, sourceID)
	c.mu.Unlock()
}

func (c *frameCollector) snapshot() []capturedFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedFrame, len(c.frames))
	copy(out, c.frames)
	return out
}

func (c *frameCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

func (c *frameCollector) resetCount(sourceID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, id := range c.resets {
		if id == sourceID {
			n++
		}
	}
	return n
}

// reset clears recorded frames and resets so a case can measure a fresh window.
func (c *frameCollector) reset() {
	c.mu.Lock()
	c.frames = c.frames[:0]
	c.resets = c.resets[:0]
	c.mu.Unlock()
}

// concatData returns all dispatched PCM bytes in receipt order.
func (c *frameCollector) concatData() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int
	for i := range c.frames {
		total += len(c.frames[i].Data)
	}
	out := make([]byte, 0, total)
	//nolint:gocritic // ruleguard false positive: appends distinct per-frame PCM, not a repetition of one slice
	for i := range c.frames {
		out = append(out, c.frames[i].Data...)
	}
	return out
}

// rtspSpec builds a standard RTSP StreamSpec for the given source ID and URL.
func (c *ContractConfig) rtspSpec(sourceID, url string) *StreamSpec {
	return &StreamSpec{
		SourceID:             sourceID,
		SourceName:           sourceID + " display",
		URL:                  url,
		Type:                 "rtsp",
		SampleRate:           c.TargetSampleRate,
		BitDepth:             targetBitDepth,
		Channels:             monoChannels,
		ChannelMode:          "downmix",
		MediaMode:            "auto",
		Transport:            "tcp",
		HealthyDataThreshold: defaultHealthyThreshold,
	}
}

// harness bundles a manager with the collector feeding it, plus cleanup.
type harness struct {
	mgr       Manager
	collector *frameCollector
}

// newHarness builds a manager wired to a fresh collector and registers Shutdown
// as test cleanup.
func newHarness(t *testing.T, cfg *ContractConfig, bufMgr *buffer.Manager, silence time.Duration) *harness {
	t.Helper()
	coll := &frameCollector{}
	mgr := cfg.Factory(t, FactoryConfig{
		OnFrame:        coll.onFrame,
		OnReset:        coll.onReset,
		BufferManager:  bufMgr,
		SilenceTimeout: silence,
	})
	require.NotNil(t, mgr, "factory must return a manager")
	t.Cleanup(func() {
		//nolint:gocritic // t.Context() is already cancelled when Cleanup runs; shutdown needs a live context
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		assert.NoError(t, mgr.ShutdownWithContext(ctx), "manager should shut down cleanly")
	})
	return &harness{mgr: mgr, collector: coll}
}

// bufferManagerFor builds a real buffer.Manager for cases that assert pooled Ref.
func bufferManagerFor(t *testing.T) *buffer.Manager {
	t.Helper()
	return buffer.NewManager(nil)
}

// waitForFrames blocks until at least n frames have been collected or the budget
// elapses, returning whether the target was reached.
func waitForFrames(coll *frameCollector, n int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if coll.count() >= n {
			return true
		}
		time.Sleep(pollInterval)
	}
	return coll.count() >= n
}
