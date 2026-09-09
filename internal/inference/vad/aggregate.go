package vad

// aggregate reduces per-frame speech probabilities to a single chunk-level
// probability: the maximum probability level that is sustained across at least
// minConsecutive consecutive frames.
//
// Taking a plain maximum would let a single 32 ms transient (a click, a sharp
// bird chip) trip the gate. Requiring the level to hold for minConsecutive
// frames (~minConsecutive * 32 ms) filters those out while still reporting a
// high probability for any speech that persists that long.
//
// Formally it returns max over all length-minConsecutive windows of that
// window's minimum probability. With minConsecutive <= 1 it degrades to a plain
// maximum. It returns 0 for empty input or when there are fewer frames than
// minConsecutive.
func aggregate(frameProbs []float32, minConsecutive int) float32 {
	if len(frameProbs) == 0 {
		return 0
	}
	minConsecutive = max(minConsecutive, 1)
	if len(frameProbs) < minConsecutive {
		return 0
	}

	var best float32
	for i := 0; i+minConsecutive <= len(frameProbs); i++ {
		windowMin := frameProbs[i]
		for j := 1; j < minConsecutive; j++ {
			if frameProbs[i+j] < windowMin {
				windowMin = frameProbs[i+j]
			}
		}
		if windowMin > best {
			best = windowMin
		}
	}
	return best
}
