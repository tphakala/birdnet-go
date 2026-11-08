package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
)

// minStepPollsPerRead is how many monitor polls the smallest BSG-BAT step must
// span: the monitor reads at most one window per poll, so a step shorter than
// two polls leaves no slack for a late tick.
const minStepPollsPerRead = 2

// TestMinBSGBatStepCoversMonitorPoll pins the lower bound of bsgbat.stepframes to
// the analysis monitor's poll interval, because the monitor reads one window per
// tick and cannot keep up with a shorter step.
func TestMinBSGBatStepCoversMonitorPoll(t *testing.T) {
	t.Parallel()

	step := time.Duration(conf.MinBSGBatStepFrames*bsgbat.HopSize) * time.Second / time.Duration(bsgbat.SampleRate)
	assert.GreaterOrEqual(t, step, minStepPollsPerRead*bufferMonitorPollInterval,
		"the minimum BSG-BAT step must span at least %d monitor polls", minStepPollsPerRead)
}
