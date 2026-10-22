// mqtt_retry_pipeline_test.go - Verifies the MQTT publish of a detection is
// retried by the job queue using realtime.mqtt.retrysettings.
package processor

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

const (
	pipelineQueueTick    = 5 * time.Millisecond
	pipelineWaitTimeout  = 10 * time.Second
	pipelineWaitTick     = 10 * time.Millisecond
	pipelineMQTTRetries  = 2
	pipelineEventSpacing = time.Hour
)

// newMQTTPipelineProcessor builds a processor wired to a real job queue, a mock
// datastore, a mock MQTT client that fails its first publish, and an SSE counter.
func newMQTTPipelineProcessor(t *testing.T, databaseEnabled bool) (p *Processor, mockDs *ActionMockDatastore, mqttClient *MockMQTTClient, sseCalls *atomic.Int32, settings *conf.Settings) {
	t.Helper()

	settings = &conf.Settings{}
	settings.Output.SQLite.Enabled = databaseEnabled
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic
	settings.Realtime.MQTT.RetrySettings = conf.RetrySettings{
		Enabled:           true,
		MaxRetries:        pipelineMQTTRetries,
		BackoffMultiplier: 1,
	}

	mockDs = NewActionMockDatastore()
	mqttClient = NewMockMQTTClient()
	mqttClient.FailNextPublishes(1, fmt.Errorf("publish timeout"))
	sseCalls = &atomic.Int32{}

	queue := jobqueue.NewJobQueue()
	queue.SetProcessingInterval(pipelineQueueTick)
	queue.StartWithContext(t.Context())
	t.Cleanup(func() { _ = queue.Stop() })

	p = &Processor{
		JobQueue:     queue,
		Settings:     settings,
		EventTracker: NewEventTracker(pipelineEventSpacing),
		Repo:         datastore.NewDetectionRepository(mockDs, nil),
		Ds:           mockDs,
		MqttClient:   mqttClient,
		SSEBroadcaster: func(_ *datastore.Note, _ *imageprovider.BirdImage) error {
			sseCalls.Add(1)
			return nil
		},
	}
	return p, mockDs, mqttClient, sseCalls, settings
}

// enqueueDetectionActions mirrors processApprovedDetection: every default action
// of the detection becomes its own queue task.
func enqueueDetectionActions(t *testing.T, p *Processor, settings *conf.Settings) {
	t.Helper()
	det := testDetection()
	for _, action := range p.getDefaultActions(&det, settings) {
		if _, isRangeFilter := action.(*UpdateRangeFilterAction); isRangeFilter {
			continue
		}
		require.NoError(t, p.EnqueueTask(&Task{Type: TaskTypeAction, Detection: det, Action: action}))
	}
}

// TestDefaultActions_MQTTPublishRetriedThroughQueue verifies that a failed MQTT
// publish is retried by the queue under realtime.mqtt.retrysettings, with the
// database ID in the payload, one saved row and one SSE broadcast.
// Not parallel: it relies on the real getJobQueueRetryConfig, which
// testRetryConfigOverride (a package global) can alter.
func TestDefaultActions_MQTTPublishRetriedThroughQueue(t *testing.T) {
	t.Run("database_sse_mqtt", func(t *testing.T) {
		p, mockDs, mqttClient, sseCalls, settings := newMQTTPipelineProcessor(t, true)
		enqueueDetectionActions(t, p, settings)

		require.Eventually(t, func() bool {
			return len(mqttClient.GetPublishedMessages()) == 1
		}, pipelineWaitTimeout, pipelineWaitTick, "MQTT publish should succeed on the retry")

		saved := mockDs.GetSavedNotes()
		require.Len(t, saved, 1, "exactly one database row per detection")
		assert.Equal(t, 2, mqttClient.GetPublishCalls(), "one failed attempt and one retry")
		assert.EqualValues(t, 1, sseCalls.Load(), "exactly one SSE broadcast")

		msg := mqttClient.GetPublishedMessages()[0]
		assert.Equal(t, testMQTTTopic, msg.topic)
		assert.NotEqual(t, uint(0), saved[0].ID)
		assert.Contains(t, msg.payload, fmt.Sprintf(`"detectionId":%d`, saved[0].ID))
	})

	t.Run("sse_mqtt_without_database", func(t *testing.T) {
		p, mockDs, mqttClient, sseCalls, settings := newMQTTPipelineProcessor(t, false)
		enqueueDetectionActions(t, p, settings)

		require.Eventually(t, func() bool {
			return len(mqttClient.GetPublishedMessages()) == 1
		}, pipelineWaitTimeout, pipelineWaitTick, "MQTT publish should succeed on the retry")

		assert.Empty(t, mockDs.GetSavedNotes())
		assert.Equal(t, 2, mqttClient.GetPublishCalls())
		assert.EqualValues(t, 1, sseCalls.Load())
		assert.Contains(t, mqttClient.GetPublishedMessages()[0].payload, `"detectionId":0`)
	})

	t.Run("database_save_fails", func(t *testing.T) {
		p, mockDs, mqttClient, _, settings := newMQTTPipelineProcessor(t, true)
		mockDs.SetSaveError(fmt.Errorf("disk full"))
		enqueueDetectionActions(t, p, settings)

		// The composite is a single-attempt job; wait until it has failed.
		require.Eventually(t, func() bool {
			return p.JobQueue.GetStats().FailedJobs == 1
		}, pipelineWaitTimeout, pipelineWaitTick, "the composite job should fail")

		// The enqueue step runs inside the composite, so an MQTT job enqueued
		// despite the failed save would already be counted here.
		assert.Equal(t, 1, p.JobQueue.GetStats().TotalJobs, "only the composite job may be enqueued")
		assert.Zero(t, mqttClient.GetPublishCalls(), "no publish for a detection whose save failed")
	})
}

// TestEnqueueAction_EnqueueFailureDoesNotStopComposite verifies that a failed
// enqueue (queue full, stopped or context done) does not stop the composite, so
// the steps after the enqueue step still run for a detection that is saved.
func TestEnqueueAction_EnqueueFailureDoesNotStopComposite(t *testing.T) {
	t.Parallel()

	wrapped := &MockAction{}
	later := &MockAction{}
	var enqueued []*Task

	composite := &CompositeAction{
		Actions: []Action{
			&enqueueAction{
				action: wrapped,
				enqueue: func(_ context.Context, task *Task) error {
					enqueued = append(enqueued, task)
					return fmt.Errorf("job queue is full")
				},
			},
			later,
		},
	}

	require.NoError(t, composite.Execute(t.Context(), nil))

	require.Len(t, enqueued, 1)
	assert.Same(t, wrapped, enqueued[0].Action, "the wrapped action is handed to the queue")
	assert.Zero(t, wrapped.GetExecuteCount(), "the wrapped action is not run inline")
	assert.Equal(t, 1, later.GetExecuteCount(), "later composite steps still run")
}
