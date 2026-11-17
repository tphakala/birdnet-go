// Package bsgbat is the spectrogram front end of the BSG-BAT bat classifier. It
// turns 384 kHz mono audio into the normalized log-mel matrix the network takes,
// following the reference implementation (data384.py in the model's original
// code): a librosa-style mel spectrogram, a log10, and a per-window normalization.
//
// The package is pure Go and knows nothing about ONNX Runtime; the classifier
// package feeds the matrix to the model.
package bsgbat

import (
	"fmt"
	"math"
	"slices"

	"github.com/tphakala/go-audio-dsp/mel"
	"github.com/tphakala/go-audio-dsp/stft"
)

// Front end parameters, fixed by the model.
const (
	// SampleRate is the sample rate in Hz the model was trained at.
	SampleRate = 384000
	// FrameSize is the FFT length in samples.
	FrameSize = 1024
	// HopSize is the frame advance in samples.
	HopSize = 768
	// NumMels is the number of mel bands per frame.
	NumMels = 128
	// MinHz is the lowest mel filter edge in Hz.
	MinHz = 9000
	// MaxHz is the highest mel filter edge in Hz.
	MaxHz = 150000
	// LogOffset is added to the mel power before the log10.
	LogOffset = 1e-6
)

// Window geometry.
const (
	// NumFrames is the number of mel frames in one model input.
	NumFrames = 512
	// StepFrames is the frame advance between successive model inputs in the
	// reference implementation. It segments the reference in the parity test and is
	// deliberately independent of the default live step (conf.DefaultBSGBatStepFrames),
	// which is the bsgbat.stepframes setting.
	StepFrames = 250
	// ClipSamples is the number of samples that frame to exactly NumFrames frames
	// without padding.
	ClipSamples = (NumFrames-1)*HopSize + FrameSize
	// InputSize is the element count of one model input.
	InputSize = NumFrames * NumMels
)

// Normalization bounds applied after the z-score and median subtraction.
const (
	// ClipMin is the lower bound of a normalized value.
	ClipMin = 0
	// ClipMax is the upper bound of a normalized value.
	ClipMax = 6
)

// BackgroundLabel is the name of the network's non-species class.
const BackgroundLabel = "Background"

// FrontEnd computes the normalized model input for one window of audio. It reuses
// its buffers across calls and is not safe for concurrent use.
type FrontEnd struct {
	extractor *mel.Extractor
	matrix    mel.Matrix
}

// MelConfig returns the mel extractor configuration of the BSG-BAT front end: a
// 1024-point transform at hop 768, 128 Slaney mel bands from 9 to 150 kHz over the
// power spectrum, and log10(x + LogOffset). Callers that frame a whole recording
// (with stft.PadZero, as the reference does) use it to build their own extractor.
func MelConfig() mel.Config {
	return mel.Config{
		SampleRate: SampleRate,
		FrameSize:  FrameSize,
		HopSize:    HopSize,
		NumMels:    NumMels,
		MinHz:      MinHz,
		MaxHz:      MaxHz,
		Input:      mel.InputPower,
		Log:        mel.Log10,
		LogOffset:  LogOffset,
	}
}

// NewFrontEnd builds a front end with the BSG-BAT mel configuration.
func NewFrontEnd() (*FrontEnd, error) {
	extractor, err := mel.New(MelConfig())
	if err != nil {
		return nil, fmt.Errorf("bsgbat: failed to build mel extractor: %w", err)
	}
	return &FrontEnd{
		extractor: extractor,
		matrix:    mel.Matrix{Data: make([]float32, InputSize)},
	}, nil
}

// Window returns the normalized model input for exactly ClipSamples samples: a
// frame-major NumFrames x NumMels matrix, which is the layout of the model's
// [1, 1, 512, 128] input tensor. The returned slice is an internal buffer that
// the next call overwrites.
func (f *FrontEnd) Window(samples []float32) ([]float32, error) {
	if len(samples) != ClipSamples {
		return nil, fmt.Errorf("bsgbat: window has %d samples, expected %d", len(samples), ClipSamples)
	}
	frames, err := f.extractor.ComputeInto(&f.matrix, samples, stft.NoPad)
	if err != nil {
		return nil, fmt.Errorf("bsgbat: failed to compute mel spectrogram: %w", err)
	}
	if frames != NumFrames {
		return nil, fmt.Errorf("bsgbat: mel spectrogram has %d frames, expected %d", frames, NumFrames)
	}
	Normalize(f.matrix.Data, NumMels)
	return f.matrix.Data, nil
}

// Normalize applies the reference normalization in place to a frame-major matrix
// of mels columns per frame: a z-score over every value with the population
// standard deviation, then the per-mel-bin median over the frames subtracted, then
// a clip to [ClipMin, ClipMax]. The median of an even frame count is the mean of
// the two middle values, as numpy computes it. A constant matrix (digital
// silence) has a zero standard deviation, where the reference produces NaN; the
// result is all zeros, the limit the formula tends to. The mean and deviation are
// accumulated in float64.
func Normalize(data []float32, mels int) {
	if mels <= 0 || len(data) == 0 || len(data)%mels != 0 {
		return
	}
	frames := len(data) / mels

	var sum float64
	for _, v := range data {
		sum += float64(v)
	}
	mean := sum / float64(len(data))
	var squares float64
	for _, v := range data {
		d := float64(v) - mean
		squares += d * d
	}
	std := math.Sqrt(squares / float64(len(data)))
	if std == 0 {
		clear(data)
		return
	}

	mean32, std32 := float32(mean), float32(std)
	for i, v := range data {
		data[i] = (v - mean32) / std32
	}

	column := make([]float32, frames)
	for m := range mels {
		for t := range frames {
			column[t] = data[t*mels+m]
		}
		slices.Sort(column)
		median := column[frames/2]
		if frames%2 == 0 {
			median = (column[frames/2-1] + column[frames/2]) / 2
		}
		for t := range frames {
			i := t*mels + m
			data[i] = min(max(data[i]-median, ClipMin), ClipMax)
		}
	}
}
