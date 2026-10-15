package analysis

import (
	"context"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	labelTestSourceID    = "rtsp_label_test"
	labelTestDisplayName = "Backyard feeder"
	labelTestRenamed     = "Renamed feeder"
	labelNameKey         = "name"
	labelSourceKey       = "source"
)

// soundLevelLabelSets gathers the registry and returns the value of the name
// and source labels of every series that has a name label.
func soundLevelLabelSets(t *testing.T, reg *prometheus.Registry) (names, sources map[string]int) {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	names, sources = map[string]int{}, map[string]int{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, l := range metric.GetLabel() {
				switch l.GetName() {
				case labelNameKey:
					names[l.GetValue()]++
				case labelSourceKey:
					sources[l.GetValue()]++
				}
			}
		}
	}
	return names, sources
}

func newLabelTestProcessor(t *testing.T) (*processor.Processor, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	sl, err := metrics.NewSoundLevelMetrics(reg)
	require.NoError(t, err)
	return &processor.Processor{Settings: &conf.Settings{}, Metrics: &observability.Metrics{SoundLevel: sl}}, reg
}

// TestSoundLevelMetrics_NameLabelIsSourceID pins that the Prometheus series of
// the measurement and the MQTT publishers carry the stable source id in the name
// label and never the display name, which the unauthenticated metrics listener
// must not expose.
func TestSoundLevelMetrics_NameLabelIsSourceID(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	conftest.SetTestSettings(settings)

	tests := []struct {
		name       string
		publishErr error
	}{
		{name: "successful publish", publishErr: nil},
		{name: "failed publish", publishErr: errors.NewStd("broker rejected")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc, reg := newLabelTestProcessor(t)
			proc.Settings = settings
			proc.SetMQTTClient(&mockMQTTClient{
				connected:   true,
				publishFunc: func(context.Context, string, string) error { return tt.publishErr },
			})
			data := makeValidSoundLevelData()
			data.Source = labelTestSourceID
			data.Name = labelTestDisplayName

			updateSoundLevelMetrics(data, proc.Metrics)
			_ = publishSoundLevelToMQTT(data, proc) // the error path is covered by the metrics assertions

			names, sources := soundLevelLabelSets(t, reg)
			require.NotEmpty(t, names)
			assert.Zero(t, names[labelTestDisplayName], "no series may carry the display name")
			assert.Equal(t, sources[labelTestSourceID], names[labelTestSourceID], "name label equals source label on every series")
			assert.Len(t, names, 1)
		})
	}
}

// TestSoundLevelMetrics_RenameCreatesNoSecondSeries pins that renaming a source
// does not add a second set of series, because the series do not depend on the
// display name.
func TestSoundLevelMetrics_RenameCreatesNoSecondSeries(t *testing.T) {
	proc, reg := newLabelTestProcessor(t)
	data := makeValidSoundLevelData()
	data.Source = labelTestSourceID

	data.Name = labelTestDisplayName
	updateSoundLevelMetrics(data, proc.Metrics)
	before, _ := soundLevelLabelSets(t, reg)

	data.Name = labelTestRenamed
	updateSoundLevelMetrics(data, proc.Metrics)
	after, _ := soundLevelLabelSets(t, reg)

	assert.Len(t, after, 1)
	assert.Zero(t, after[labelTestRenamed])
	assert.Len(t, after, len(before))
}

// TestSoundLevelMetrics_ClientNotReadyLabelsSourceID pins that the publisher
// that drops data while the MQTT client is not ready records its error series
// under the source id.
func TestSoundLevelMetrics_ClientNotReadyLabelsSourceID(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	conftest.SetTestSettings(settings)

	proc, reg := newLabelTestProcessor(t)
	proc.Settings = settings
	data := makeValidSoundLevelData()
	data.Source = labelTestSourceID
	data.Name = labelTestDisplayName

	require.NoError(t, publishSoundLevelToMQTT(data, proc))

	names, _ := soundLevelLabelSets(t, reg)
	assert.Equal(t, map[string]int{labelTestSourceID: 1}, names)
}

// TestSoundLevelSSEPublisher_LabelsSourceID pins that the SSE publisher records
// its success and its broadcast failure under the source id, not the display
// name.
func TestSoundLevelSSEPublisher_LabelsSourceID(t *testing.T) {
	tests := []struct {
		name       string
		withSSE    bool
		wantSeries int
	}{
		{name: "broadcast succeeds", withSSE: true, wantSeries: 1},
		{name: "broadcast fails", withSSE: false, wantSeries: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc, reg := newLabelTestProcessor(t)
			core := &apicore.Core{Processor: proc}
			if tt.withSSE {
				core.SSEManager = apicore.NewSSEManager()
			}
			ch := make(chan soundlevel.SoundLevelData, 1)
			data := makeValidSoundLevelData()
			data.Source = labelTestSourceID
			data.Name = labelTestDisplayName
			ch <- data
			close(ch)

			var wg sync.WaitGroup
			startSoundLevelSSEPublisher(&wg, t.Context(), &apiv2.Controller{Core: core}, ch)
			wg.Wait()

			names, _ := soundLevelLabelSets(t, reg)
			assert.Equal(t, map[string]int{labelTestSourceID: tt.wantSeries}, names)
		})
	}
}
