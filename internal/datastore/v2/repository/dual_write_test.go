package repository

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

func TestConvertFilters_PreservesAllFields(t *testing.T) {
	t.Parallel()

	minConf := 0.8
	locked := true
	verified := true

	filters := &datastore.DetectionFilters{
		Query: "Turdus merula",
		Confidence: &datastore.ConfidenceRange{
			Operator: ">=",
			Value:    minConf,
		},
		Locked:   &locked,
		Verified: &verified,
		Limit:    25,
		Offset:   10,
	}

	dw := &DualWriteRepository{}
	sf := dw.convertFilters(filters)

	assert.Equal(t, "Turdus merula", sf.Query)
	require.NotNil(t, sf.MinConfidence)
	assert.InDelta(t, minConf, *sf.MinConfidence, 0.001)
	require.NotNil(t, sf.IsLocked)
	assert.True(t, *sf.IsLocked)
	require.NotNil(t, sf.IsReviewed)
	assert.True(t, *sf.IsReviewed)
	assert.Equal(t, 25, sf.Limit)
	assert.Equal(t, 10, sf.Offset)
	assert.True(t, sf.SortDesc)
}

func TestConvertFilters_VerifiedFalse(t *testing.T) {
	t.Parallel()

	verified := false
	filters := &datastore.DetectionFilters{
		Verified: &verified,
	}

	dw := &DualWriteRepository{}
	sf := dw.convertFilters(filters)

	require.NotNil(t, sf.IsReviewed)
	assert.False(t, *sf.IsReviewed)
}

func TestConvertFilters_DateRange(t *testing.T) {
	t.Parallel()

	filters := &datastore.DetectionFilters{
		StartDate: "2024-06-15",
		EndDate:   "2024-06-17",
	}

	dw := &DualWriteRepository{}
	sf := dw.convertFilters(filters)

	require.NotNil(t, sf.StartTime)
	require.NotNil(t, sf.EndTime)

	startExpected := time.Date(2024, 6, 15, 0, 0, 0, 0, time.Local).Unix()
	endExpected := time.Date(2024, 6, 18, 0, 0, 0, 0, time.Local).Unix()

	assert.Equal(t, startExpected, *sf.StartTime)
	assert.Equal(t, endExpected, *sf.EndTime)
}

func TestConvertFilters_SingleDate(t *testing.T) {
	t.Parallel()

	filters := &datastore.DetectionFilters{
		Date: "2024-06-15",
	}

	dw := &DualWriteRepository{}
	sf := dw.convertFilters(filters)

	require.NotNil(t, sf.StartTime)
	require.NotNil(t, sf.EndTime)

	startExpected := time.Date(2024, 6, 15, 0, 0, 0, 0, time.Local).Unix()
	assert.Equal(t, startExpected, *sf.StartTime)
	assert.Greater(t, *sf.EndTime, *sf.StartTime)
}

func TestConvertFilters_NoDates(t *testing.T) {
	t.Parallel()

	filters := &datastore.DetectionFilters{}

	dw := &DualWriteRepository{}
	sf := dw.convertFilters(filters)

	assert.Nil(t, sf.StartTime)
	assert.Nil(t, sf.EndTime)
}

func TestConvertFilters_ConfidenceOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		operator string
		wantMin  bool
		wantMax  bool
	}{
		{"greater-equal", ">=", true, false},
		{"greater", ">", true, false},
		{"less-equal", "<=", false, true},
		{"less", "<", false, true},
		{"equal", "=", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			val := 0.5
			filters := &datastore.DetectionFilters{
				Confidence: &datastore.ConfidenceRange{
					Operator: tt.operator,
					Value:    val,
				},
			}
			dw := &DualWriteRepository{}
			sf := dw.convertFilters(filters)

			if tt.wantMin {
				require.NotNil(t, sf.MinConfidence)
				assert.InDelta(t, val, *sf.MinConfidence, 0.001)
			} else {
				assert.Nil(t, sf.MinConfidence)
			}
			if tt.wantMax {
				require.NotNil(t, sf.MaxConfidence)
				assert.InDelta(t, val, *sf.MaxConfidence, 0.001)
			} else {
				assert.Nil(t, sf.MaxConfidence)
			}
		})
	}
}

func testLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil)
}

func TestReconciliation_ConcurrentStartAndShutdown(t *testing.T) {
	t.Parallel()

	dw := &DualWriteRepository{
		shutdownCh: make(chan struct{}),
		semaphore:  make(chan struct{}, defaultMaxConcurrentWrites),
		logger:     testLogger(),
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		dw.StartReconciliation()
	}()

	go func() {
		defer wg.Done()
		dw.Shutdown()
	}()

	wg.Wait()
}

func TestReconciliation_ShutdownWithoutStart(t *testing.T) {
	t.Parallel()

	dw := &DualWriteRepository{
		shutdownCh: make(chan struct{}),
		semaphore:  make(chan struct{}, defaultMaxConcurrentWrites),
		logger:     testLogger(),
	}

	assert.NotPanics(t, func() {
		dw.Shutdown()
	})
}
