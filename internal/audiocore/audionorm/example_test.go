package audionorm_test

import (
	"fmt"
	"math"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
)

// toneInt16 builds a mono 1 kHz sine at the given peak dBFS, 48 kHz.
func toneInt16(dbfs float64, seconds int) []int16 {
	amp := math.Pow(10, dbfs/20) * 32767
	n := 48000 * seconds
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(math.Round(amp * math.Sin(2*math.Pi*1000*float64(i)/48000)))
	}
	return pcm
}

// Normalize a 48 kHz mono 16-bit clip to the EBU R 128 defaults, in place.
func ExampleNormalizeInt16() {
	pcm := toneInt16(-10, 2) // ~-13 LUFS, needs attenuation to reach -23

	opts := audionorm.DefaultOptions() // 48 kHz mono, -23 LUFS, -1.0 dBTP
	res, err := audionorm.NormalizeInt16(pcm, opts)
	if err != nil {
		panic(err)
	}

	fmt.Printf("input:  %.1f LUFS, %.1f dBTP\n", res.Input.IntegratedLUFS, res.Input.TruePeakDBTP)
	fmt.Printf("gain:   %.1f dB (peak limited: %t)\n", res.GainDB, res.PeakLimited)
	fmt.Printf("output: %.1f LUFS\n", res.OutputLUFS)
	// Output:
	// input:  -13.0 LUFS, -10.0 dBTP
	// gain:   -10.0 dB (peak limited: false)
	// output: -23.0 LUFS
}

// Measure loudness and true peak without modifying the buffer.
func ExampleMeasureInt16() {
	pcm := toneInt16(-23, 2)

	m, err := audionorm.MeasureInt16(pcm, 48000, 1)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%.1f LUFS, %.1f dBTP\n", m.IntegratedLUFS, m.TruePeakDBTP)
	// Output: -26.0 LUFS, -23.0 dBTP
}
