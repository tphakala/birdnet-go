package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

const (
	liveEventInterval = 15 * time.Second
	liveEventSpacing  = 12 * time.Second
	liveEventDrain    = 15 * time.Minute
	liveEventTick     = 100 * time.Millisecond
)

// outageRepository fails every save made before outageEnd and records the ids of
// the rows it stores.
type outageRepository struct {
	*MockDetectionRepository
	outageEnd time.Time
	terminal  bool

	mu   sync.Mutex
	rows []uint
}

func (r *outageRepository) Save(ctx context.Context, result *detection.Result, additional []detection.AdditionalResult) error {
	if time.Now().Before(r.outageEnd) {
		if r.terminal {
			return fmt.Errorf("disk full")
		}
		return refusedRetrySafeError()
	}
	if err := r.MockDetectionRepository.Save(ctx, result, additional); err != nil {
		return err
	}
	r.mu.Lock()
	r.rows = append(r.rows, result.ID)
	r.mu.Unlock()
	return nil
}

func sortedIDs(ids []uint) []uint {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// TestDefaultActions_LiveEventsCarryTheStoredDetection drives same-species
// detections through the real default actions and job queue while the database is
// down, and verifies that SSE and MQTT announce exactly the rows that were stored,
// each once and with its id. Not parallel, for the reason given in
// TestDefaultActions_MQTTPublishRetriedThroughQueue.
func TestDefaultActions_LiveEventsCarryTheStoredDetection(t *testing.T) {
	tests := []struct {
		name       string
		outage     time.Duration
		terminal   bool
		detections int
		wantRows   int
	}{
		{"outage_shorter_than_retry_window", 150 * time.Second, false, 30, 15},
		{"first_save_fails_terminally", time.Second, true, 20, 9},
		{"outage_longer_than_retry_window", 420 * time.Second, false, 50, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				repo := &outageRepository{
					MockDetectionRepository: NewMockDetectionRepository(),
					outageEnd:               time.Now().Add(tt.outage),
					terminal:                tt.terminal,
				}
				var sseMu sync.Mutex
				var sseIDs []uint
				mqttClient := NewMockMQTTClient()

				settings := &conf.Settings{}
				settings.Output.SQLite.Enabled = true
				settings.Realtime.MQTT.Enabled = true
				settings.Realtime.MQTT.Topic = testMQTTTopic

				queue := jobqueue.NewJobQueue()
				queue.SetProcessingInterval(liveEventTick)
				queue.StartWithContext(ctx)
				p := &Processor{
					JobQueue:     queue,
					Settings:     settings,
					EventTracker: NewEventTracker(liveEventInterval),
					Repo:         repo,
					MqttClient:   mqttClient,
					SSEBroadcaster: func(note *datastore.Note, _ *imageprovider.BirdImage) error {
						sseMu.Lock()
						sseIDs = append(sseIDs, note.ID)
						sseMu.Unlock()
						return nil
					},
				}

				for range tt.detections {
					det := testDetection()
					for _, action := range p.getDefaultActions(&det, settings) {
						if _, isRangeFilter := action.(*UpdateRangeFilterAction); isRangeFilter {
							continue
						}
						require.NoError(t, p.EnqueueTask(&Task{Type: TaskTypeAction, Detection: det, Action: action}))
					}
					time.Sleep(liveEventSpacing)
				}
				time.Sleep(liveEventDrain)
				require.NoError(t, queue.Stop())
				cancel()
				synctest.Wait()

				repo.mu.Lock()
				rows := sortedIDs(repo.rows)
				repo.mu.Unlock()
				sseMu.Lock()
				sse := sortedIDs(sseIDs)
				sseMu.Unlock()
				published := mqttClient.GetPublishedMessages()
				mqttIDs := make([]uint, 0, len(published))
				for _, msg := range published {
					var payload struct {
						DetectionID uint `json:"detectionId"`
					}
					require.NoError(t, json.Unmarshal([]byte(msg.payload), &payload))
					mqttIDs = append(mqttIDs, payload.DetectionID)
				}

				assert.Len(t, rows, tt.wantRows, "stored rows")
				assert.NotContains(t, rows, uint(0))
				assert.Equal(t, rows, sse, "SSE announces exactly the stored rows")
				assert.Equal(t, rows, sortedIDs(mqttIDs), "MQTT announces exactly the stored rows")
			})
		})
	}
}

// TestDefaultActions_SSEAndMQTTWithoutDatabaseKeepTheirOwnLimiters verifies that
// without a database, SSE and MQTT still rate limit per species: the database
// composite is the only place that bypasses their limiters.
func TestDefaultActions_SSEAndMQTTWithoutDatabaseKeepTheirOwnLimiters(t *testing.T) {
	t.Parallel()

	p, _, mqttClient, sseCalls, settings := newMQTTPipelineProcessor(t, false)
	for range 2 {
		det := testDetection()
		for _, action := range p.getDefaultActions(&det, settings) {
			switch action.(type) {
			case *SSEAction, *MqttAction:
				// The first MQTT publish fails by design of the helper; only the
				// number of attempts matters here.
				_ = action.Execute(t.Context(), nil)
			}
		}
	}

	assert.EqualValues(t, 1, sseCalls.Load(), "the second detection inside the interval is suppressed")
	assert.Equal(t, 1, mqttClient.GetPublishCalls(), "the second detection inside the interval is suppressed")
}
