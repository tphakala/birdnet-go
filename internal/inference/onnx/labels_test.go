package onnx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadLabelsText_LongLine guards against the default bufio.Scanner 64 KiB
// token cap: a plain-text label file with a line longer than that (or with no
// line breaks) previously failed with "bufio.Scanner: token too long" (Sentry
// BIRDNET-GO-2FF). The scanner buffer is grown to labelScannerMaxLineBytes so a
// single line up to 1 MiB parses without error.
func TestLoadLabelsText_LongLine(t *testing.T) {
	t.Parallel()

	longLabel := strings.Repeat("A", 70*1024) // comfortably past the 64 KiB default
	input := []byte(longLabel + "\nTurdus migratorius_American Robin\n")

	labels, err := loadLabelsText(input)
	require.NoError(t, err, "scanner buffer must accommodate lines beyond the 64 KiB default")
	require.Len(t, labels, 2)
	assert.Equal(t, longLabel, labels[0])
	assert.Equal(t, "Turdus migratorius_American Robin", labels[1])
}

// TestLoadLabelsText_SkipsBlankLines confirms normal parsing is unchanged.
func TestLoadLabelsText_SkipsBlankLines(t *testing.T) {
	t.Parallel()

	labels, err := loadLabelsText([]byte("Turdus migratorius_American Robin\n\nCyanocitta cristata_Blue Jay\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"Turdus migratorius_American Robin", "Cyanocitta cristata_Blue Jay"}, labels)
}
