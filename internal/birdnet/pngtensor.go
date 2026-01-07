package birdnet

import (
    "fmt"
    "image"
    _ "image/png"
    "os"
)

// LoadPNGToTensor decodes a PNG image at path and returns a float32 slice
// laid out in NHWC order with shape (1, height, width, 3).
// It expects an image of size 512x128; returns an error otherwise.
// The returned floats are in the 0-255 range (no normalization).
func LoadPNGToTensor(path string) ([]float32, int, int, error) {
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
        return nil, w, h, fmt.Errorf("unexpected image size %dx%d, want 512x128", w, h)
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
