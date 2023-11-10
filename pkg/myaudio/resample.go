package myaudio

// ResampleAudio resamples the given audio slice from the original sample rate to the target sample rate using cubic interpolation.
func ResampleAudio(audio []float32, originalRate, targetRate int) ([]float32, error) {
	if originalRate == targetRate {
		return audio, nil
	}

	ratio := float64(targetRate) / float64(originalRate)
	newLength := int(float64(len(audio)) * ratio)
	resampled := make([]float32, newLength)

	// Pre-calculate common terms used in the loop
	audioLength := len(audio)
	lastIndex := audioLength - 3

	for i := 0; i < newLength; i++ {
		origPos := float64(i) / ratio
		index := int(origPos)

		// Clamp index to avoid out-of-bounds access
		if index < 1 {
			index = 1
		} else if index > lastIndex {
			index = lastIndex
		}

		frac := float32(origPos) - float32(index)

		// Inline cubic interpolation to avoid extra function calls
		y0, y1, y2, y3 := audio[index-1], audio[index], audio[index+1], audio[index+2]
		mu2 := frac * frac
		a0 := -0.5*y0 + 1.5*y1 - 1.5*y2 + 0.5*y3
		a1 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
		a2 := -0.5*y0 + 0.5*y2
		a3 := y1

		resampled[i] = a0*frac*mu2 + a1*mu2 + a2*frac + a3
	}

	return resampled, nil
}
