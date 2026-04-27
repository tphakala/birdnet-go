package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestProcessData_SkipsInferenceWhenSuspended(t *testing.T) {
	tracker := GetVolumeSuspendTracker()
	sourceID := "suspended-source"
	tracker.RemoveSource(sourceID)
	t.Cleanup(func() {
		tracker.RemoveSource(sourceID)
	})

	tracker.InitializeSource(sourceID, conf.LowNoiseAutoSuspendSettings{
		Enabled:          true,
		SuspendThreshold: 10,
		ResumeThreshold:  20,
		MinSuspendFrames: 1,
		MinResumeFrames:  1,
	})
	tracker.UpdateAudioLevel(sourceID, 0, 0)
	assert.True(t, tracker.IsSuspended(sourceID))

	before := len(classifier.ResultsQueue)
	err := ProcessData(context.Background(), nil, []byte{0, 0}, time.Now(), time.Now(), sourceID, "unused-model")
	after := len(classifier.ResultsQueue)

	assert.NoError(t, err)
	assert.Equal(t, before, after, "suspended processing should not enqueue inference results")
}
