package analysis

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

const (
	// mqttRuntimeBaseTopic is the MQTT base topic of the runtime toggle tests.
	mqttRuntimeBaseTopic = "birdnet/runtime"

	// mqttRuntimePublishWait bounds the wait for an expected publish.
	mqttRuntimePublishWait = 5 * time.Second

	// mqttRuntimeQuietWindow is how long the disabled test watches for a publish
	// that must not happen, and its poll interval.
	mqttRuntimeQuietWindow = 300 * time.Millisecond
	mqttRuntimePollEvery   = 10 * time.Millisecond

	// mqttRuntimeSourceDisabled and mqttRuntimeSourceEnabled tag intervals sent
	// while MQTT is off and on, so a publish identifies which interval it carried.
	mqttRuntimeSourceDisabled = "interval-while-disabled"
	mqttRuntimeSourceEnabled  = "interval-while-enabled"
)

// mqttRuntimeFixture starts the sound level publishers with MQTT in the given
// state and a processor whose mock client reports every publish on the sound
// level topic to published.
type mqttRuntimeFixture struct {
	published chan string
	source    chan soundlevel.SoundLevelData
}

func startMQTTRuntimeFixture(t *testing.T, mqttEnabled bool) *mqttRuntimeFixture {
	t.Helper()
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	setMQTTRuntimeEnabled(mqttEnabled)

	f := &mqttRuntimeFixture{
		published: make(chan string, 8),
		source:    make(chan soundlevel.SoundLevelData, soundLevelChanSize),
	}
	soundTopic := mqtt.SoundLevelTopic(mqttRuntimeBaseTopic)

	proc := &processor.Processor{Settings: &conf.Settings{}}
	proc.SetMQTTClient(&mockMQTTClient{
		connected: true,
		publishFunc: func(_ context.Context, topic, payload string) error {
			if topic == soundTopic {
				f.published <- payload
			}
			return nil
		},
	})

	var wg sync.WaitGroup
	done := make(chan struct{})
	startSoundLevelPublishers(&wg, done, proc, f.source, nil)
	t.Cleanup(func() {
		close(done)
		wg.Wait()
	})
	return f
}

// setMQTTRuntimeEnabled publishes settings with MQTT switched on or off, as the
// settings API does while the sound level publishers keep running.
func setMQTTRuntimeEnabled(enabled bool) {
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = enabled
	settings.Realtime.MQTT.Topic = mqttRuntimeBaseTopic
	conftest.SetTestSettings(settings)
}

func (f *mqttRuntimeFixture) sendInterval(sourceID string) {
	data := makeValidSoundLevelData()
	data.Source = sourceID
	f.source <- data
}

// TestSoundLevelPublishers_MQTTEnabledAfterStartPublishes pins that enabling MQTT
// after the publishers started makes the next interval reach the broker, without
// restarting sound level monitoring.
func TestSoundLevelPublishers_MQTTEnabledAfterStartPublishes(t *testing.T) {
	// Not parallel: the settings snapshot is global.
	f := startMQTTRuntimeFixture(t, false)

	setMQTTRuntimeEnabled(true)
	f.sendInterval(mqttRuntimeSourceEnabled)

	select {
	case payload := <-f.published:
		assert.Contains(t, payload, mqttRuntimeSourceEnabled, "the published payload must be the sound level data")
	case <-time.After(mqttRuntimePublishWait):
		require.FailNow(t, "no sound level message reached MQTT after it was enabled at runtime")
	}
}

// TestSoundLevelPublishers_MQTTDisabledAfterStartStopsPublishing pins the reverse:
// an interval that arrives after MQTT was disabled is not published, while the
// next one after re-enabling is.
func TestSoundLevelPublishers_MQTTDisabledAfterStartStopsPublishing(t *testing.T) {
	// Not parallel: the settings snapshot is global.
	f := startMQTTRuntimeFixture(t, true)

	setMQTTRuntimeEnabled(false)
	f.sendInterval(mqttRuntimeSourceDisabled)
	assert.Never(t, func() bool { return len(f.published) > 0 }, mqttRuntimeQuietWindow, mqttRuntimePollEvery,
		"an interval that arrives while MQTT is disabled must not be published")

	setMQTTRuntimeEnabled(true)
	f.sendInterval(mqttRuntimeSourceEnabled)

	select {
	case payload := <-f.published:
		assert.Contains(t, payload, mqttRuntimeSourceEnabled, "the first publish must be the interval sent while MQTT was enabled")
	case <-time.After(mqttRuntimePublishWait):
		require.FailNow(t, "no sound level message reached MQTT after it was re-enabled")
	}
}
