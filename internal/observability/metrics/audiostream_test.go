package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// AudioStreamMetrics must satisfy audiocore.StreamMetrics so it can be assigned
// to engine.Config.StreamMetrics and forwarded to both stream producers.
var _ audiocore.StreamMetrics = (*AudioStreamMetrics)(nil)

func TestAudioStreamMetrics(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m, err := NewAudioStreamMetrics(reg)
	require.NoError(t, err)

	const (
		src   = "source-1"
		delta = 1e-9
	)

	m.IncStreamErrors(src)
	m.IncStreamErrors(src)
	m.SetStreamHealth(src, true)
	m.RecordDataRate(src, 1234.5)
	m.RecordWireRate(src, 678.0)
	m.SetStreamEngine(src, audiocore.EngineNative)

	// The aggregate Collect must export exactly one series per vec (5). ToFloat64
	// below only exercises each vec in isolation, so this guards against a vec
	// being dropped from Collect.
	assert.Equal(t, 5, testutil.CollectAndCount(m), "Collect must export one series per vec")

	assert.InDelta(t, 2.0, testutil.ToFloat64(m.StreamErrors.WithLabelValues(src)), delta)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.StreamHealthy.WithLabelValues(src)), delta)
	assert.InDelta(t, 1234.5, testutil.ToFloat64(m.DataRate.WithLabelValues(src)), delta)
	assert.InDelta(t, 678.0, testutil.ToFloat64(m.WireRate.WithLabelValues(src)), delta)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.StreamEngine.WithLabelValues(src, audiocore.EngineNative)), delta)

	// An unhealthy transition flips the health gauge to 0 (still a live series).
	m.SetStreamHealth(src, false)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.StreamHealthy.WithLabelValues(src)), delta)

	// DeleteStream removes every series for the source, so a stopped stream stops
	// being exported.
	m.DeleteStream(src)
	assert.Equal(t, 0, testutil.CollectAndCount(m), "DeleteStream must remove all series for the source")
}
