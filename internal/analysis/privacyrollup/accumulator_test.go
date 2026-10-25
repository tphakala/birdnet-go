package privacyrollup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testHour = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func testEvent(kind Kind, at time.Time, score float64, thresholdMilli int) Event {
	return Event{
		Kind: kind, At: at, SourceURI: "src", NodeName: "node",
		ModelID: "m", Trigger: "label", ThresholdMilli: thresholdMilli, Score: score,
	}
}

func TestAccumulator_EventKinds(t *testing.T) {
	t.Parallel()
	const thr = 50
	tests := []struct {
		name string
		ev   Event
		want counters
	}{
		{"counted hit", testEvent(KindCountedHit, testHour, 0.9, thr), counters{hits: 1, ge200: 1, maxScore: 0.9}},
		{"held hit", testEvent(KindHeldHit, testHour, 0.9, thr), counters{held: 1, ge200: 1, maxScore: 0.9}},
		{"confirmed held is not bucketed", testEvent(KindConfirmedHeld, testHour, 0.9, thr), counters{hits: 1, confirmed: 1}},
		{"ignored held", testEvent(KindIgnoredHeld, testHour, 0.9, thr), counters{ignored: 1}},
		{"discard", testEvent(KindDiscard, testHour, 0.9, thr), counters{discards: 1}},
		{"unscanned save", testEvent(KindUnscannedSave, testHour, 0, NoThresholdMilli), counters{unscanned: 1}},
		{"margin 49 is the first bucket", testEvent(KindCountedHit, testHour, 0.099, thr), counters{hits: 1, lt50: 1, maxScore: 0.099}},
		{"margin 50 is the second bucket", testEvent(KindCountedHit, testHour, 0.1, thr), counters{hits: 1, lt100: 1, maxScore: 0.1}},
		{"margin 99", testEvent(KindCountedHit, testHour, 0.149, thr), counters{hits: 1, lt100: 1, maxScore: 0.149}},
		{"margin 100", testEvent(KindCountedHit, testHour, 0.15, thr), counters{hits: 1, lt200: 1, maxScore: 0.15}},
		{"margin 199", testEvent(KindCountedHit, testHour, 0.249, thr), counters{hits: 1, lt200: 1, maxScore: 0.249}},
		{"margin 200", testEvent(KindCountedHit, testHour, 0.25, thr), counters{hits: 1, ge200: 1, maxScore: 0.25}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var a accumulator
			a.add(tt.ev)
			require.Len(t, a.rows, 1)
			for _, d := range a.rows {
				assert.Equal(t, tt.want, *d)
			}
		})
	}
}

func TestAccumulator_HourComesFromTheEventStamp(t *testing.T) {
	t.Parallel()
	var a accumulator
	a.add(testEvent(KindCountedHit, testHour.Add(59*time.Minute+59*time.Second+999*time.Millisecond), 0.9, 50))
	a.add(testEvent(KindCountedHit, testHour.Add(time.Hour), 0.9, 50))
	require.Len(t, a.rows, 2)
	hours := map[int64]bool{}
	for k := range a.rows {
		hours[k.hourStart] = true
	}
	assert.True(t, hours[testHour.Unix()])
	assert.True(t, hours[testHour.Add(time.Hour).Unix()])
}

func TestAccumulator_BucketsSumToHitsMinusConfirmedPlusHeld(t *testing.T) {
	t.Parallel()
	var a accumulator
	// held A, confirmed by B (counted), then a lone counted C, then a held D that is ignored.
	for _, ev := range []Event{
		testEvent(KindHeldHit, testHour, 0.6, 50),
		testEvent(KindConfirmedHeld, testHour, 0.6, 50),
		testEvent(KindCountedHit, testHour, 0.12, 50),
		testEvent(KindCountedHit, testHour, 0.9, 50),
		testEvent(KindHeldHit, testHour, 0.07, 50),
		testEvent(KindIgnoredHeld, testHour, 0.07, 50),
	} {
		a.add(ev)
	}
	require.Len(t, a.rows, 1)
	for _, d := range a.rows {
		c := *d
		assert.Equal(t, c.hits-c.confirmed+c.held, c.lt50+c.lt100+c.lt200+c.ge200)
		assert.Equal(t, int64(3), c.hits)
		assert.Equal(t, int64(2), c.held)
	}
}

func TestThresholdMilli_RoundsNotTruncates(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 700, ThresholdMilli(float64(float32(0.7))))
	assert.Equal(t, 290, ThresholdMilli(float64(float32(0.29))))
	assert.Equal(t, 50, ThresholdMilli(0.05))
}
