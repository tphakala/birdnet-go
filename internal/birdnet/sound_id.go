package birdnet

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	tflite "github.com/tphakala/go-tflite"
)

// GenerateSoundIdSpectrogram generates a spectrogram for the sound id model.
// The samples are expected to be 3 seconds of 22,050 Hz audio, normalized between -1.0 and 1.0.
// It returns the spectrogram data as a float32 slice in NHWC format (1, height, width, 3),
// along with the height and width of the spectrogram.
func (bn *BirdNET) GenerateSoundIdSpectrogram(ctx context.Context, sample []float32) ([]float32, int, int, error) {
	span, _ := StartSpan(ctx, "birdnet.soundid", "Sound id spectrogram generation")
	defer span.Finish()

	start := time.Now()

	hopSize := 128
	windowSize := 512

	width := 512
	height := 128

	inputTensor := bn.SpectrogramInterpreter.GetInputTensor(0)
	if inputTensor == nil {
		err := errors.New(fmt.Errorf("cannot get spectrogram input tensor")).
			Category(errors.CategoryModelInit).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("interpreter_state", "initialized").
			Build()

		// Record error in metrics via span finish
		span.SetTag("error", "true")
		span.SetData("error_type", "input_tensor_nil")

		return nil, height, width, err
	}

	// The spectrogram generator produces a single column of the spectrogram.
	// The generator expects 512 audio samples normalized between -1.0 and 1.0,
	// and returns values for the 128 frequency bins.
	//
	// We run the generator for each hop in the 3 second clip, and assemble
	// the results into a NHWC (1,128,512,3) for the sound id model.
	channels := 3
	spectrogramData := make([]float32, height*width*channels)
	currentWindow := inputTensor.Float32s()
	currentColumn := make([]byte, height)

	for i := 0; i < width; i++ {
		hopStart := i * hopSize
		for j := 0; j < windowSize; j++ {
			currentWindow[j] = sample[hopStart+j]
		}

		if status := bn.SpectrogramInterpreter.Invoke(); status != tflite.OK {
			err := errors.Newf("spectrogram tensor invoke failed (hop=%d): %v", i, status).
				Category(errors.CategoryAudio).
				Context("hop_index", i).
				Context("status_code", status).
				Timing("spectrogram-invoke", time.Since(start)).
				Build()

			span.SetTag("error", "true")
			span.SetData("error_type", "spectrogram_invoke_failed")
			span.SetData("status_code", status)

			return nil, height, width, err
		}

		outTensor := bn.SpectrogramInterpreter.GetOutputTensor(0)
		if outTensor == nil {
			return nil, height, width, errors.New(fmt.Errorf("spectrogram output tensor nil")).
				Category(errors.CategoryModelInit).
				Context("hop_index", i).
				Build()
		}

		output_size := outTensor.Dim(0)
		if len(currentColumn) != output_size {
			err := errors.Newf("Unexpected output size %d, want %d", len(currentColumn), output_size).
				Category(errors.CategoryModelInit).
				Context("hop_index", i).
				Build()
			return nil, height, width, err
		}
		outTensor.CopyToBuffer(&currentColumn[0])

		// Add the current column to the spectrogram.
		// Frequency values are normalized between 0.0 and 1.0 and duplicated into the RGB channels
		for y := 0; y < height; y++ {
			value := float32(currentColumn[y]) / 255.0
			base := ((y * width) + i) * channels

			spectrogramData[base+0] = value
			spectrogramData[base+1] = value
			spectrogramData[base+2] = value
		}
	}

	return spectrogramData, height, width, nil
}

// LoadSoundIdSpectrogram decodes a PNG image at path and returns a float32 slice
// laid out in NHWC order with shape (1, height, width, 3).
// It expects an image of size 512x128; returns an error otherwise.
// The returned floats are normalized to be between 0.0 and 1.0.
func LoadSoundIdSpectrogram(path string) ([]float32, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, 0, 0, err
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	if w != 512 || h != 128 {
		return nil, h, w, fmt.Errorf("unexpected image size %dx%d, want 512x128", w, h)
	}

	// NHWC with batch=1: length = 1 * h * w * 3
	out := make([]float32, 1*h*w*3)

	// iterate rows (y) then columns (x) so memory layout matches NHWC
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r32, g32, b32, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Convert 16-bit color to 8-bit
			r := float32(r32 >> 8)
			g := float32(g32 >> 8)
			b := float32(b32 >> 8)

			base := ((y * w) + x) * 3
			out[base+0] = r / 255.0
			out[base+1] = g / 255.0
			out[base+2] = b / 255.0
		}
	}

	return out, h, w, nil
}

// WriteSoundIdSpectrogram writes the spectrogram data to a PNG file.
func WriteSoundIdSpectrogram(pngPath string, spectrogramData []float32, height int, width int, channels int) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			base := ((y * width) + x) * channels
			var r, g, b uint8
			if base+2 < len(spectrogramData) {
				r = uint8(spectrogramData[base+0] * 255.0)
				g = uint8(spectrogramData[base+1] * 255.0)
				b = uint8(spectrogramData[base+2] * 255.0)
			}
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}
	if f, err := os.Create(pngPath); err == nil {
		_ = png.Encode(f, img)
		_ = f.Close()
		fmt.Printf("Wrote %s\n", pngPath)
	} else {
		fmt.Printf("Failed to create .png: %v\n", err)
	}
}
