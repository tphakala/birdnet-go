package classifier

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longLabelTestBytes is a label-line length comfortably past bufio's default
// 64 KiB token cap, used by the label-scanner regression tests in this package.
const longLabelTestBytes = 70 * 1024

func TestParsePerchLabels(t *testing.T) {
	t.Parallel()

	input := "inat2024_fsd50k\nAbavorana luctuosa\nAbeillia abeillei\nAcanthis flammea\n"
	labels, err := ParsePerchLabels([]byte(input))
	require.NoError(t, err)
	assert.Len(t, labels, 3)
	assert.Equal(t, "Abavorana luctuosa", labels[0])
	assert.Equal(t, "Abeillia abeillei", labels[1])
	assert.Equal(t, "Acanthis flammea", labels[2])
}

func TestParsePerchLabels_SkipsEmptyLines(t *testing.T) {
	t.Parallel()

	input := "inat2024_fsd50k\nAbavorana luctuosa\n\nAbeillia abeillei\n"
	labels, err := ParsePerchLabels([]byte(input))
	require.NoError(t, err)
	assert.Len(t, labels, 2)
}

func TestParsePerchLabels_EmptyInput(t *testing.T) {
	t.Parallel()

	labels, err := ParsePerchLabels([]byte(""))
	require.NoError(t, err)
	assert.Empty(t, labels)
}

func TestParsePerchLabels_HeaderOnly(t *testing.T) {
	t.Parallel()

	labels, err := ParsePerchLabels([]byte(perchDatasetMarker + "\n"))
	require.NoError(t, err)
	assert.Empty(t, labels)
}

// TestParsePerchLabels_LongLine guards against the default bufio.Scanner 64 KiB
// token cap: a label file with a line longer than that (or effectively no line
// breaks) previously failed with "bufio.Scanner: token too long" (Sentry
// BIRDNET-GO-2FF). The scanner buffer is grown to labelScannerMaxLineBytes so a
// single line up to 1 MiB parses without error.
func TestParsePerchLabels_LongLine(t *testing.T) {
	t.Parallel()

	// A label line comfortably larger than bufio's default 64 KiB cap.
	longLabel := strings.Repeat("A", longLabelTestBytes)
	input := perchDatasetMarker + "\n" + longLabel + "\nAcanthis flammea\n"

	labels, err := ParsePerchLabels([]byte(input))
	require.NoError(t, err, "scanner buffer must accommodate lines beyond the 64 KiB default")
	require.Len(t, labels, 2)
	assert.Equal(t, longLabel, labels[0])
	assert.Equal(t, "Acanthis flammea", labels[1])
}

// TestParsePerchLabels_MaxLengthLine verifies a label of exactly the supported
// maxLabelLineBytes parses for every line terminator. bufio.Scanner needs its
// token cap strictly above the longest token, so labelScannerMaxLineBytes is
// maxLabelLineBytes+2; without that reserve a full-length line fails with
// "token too long" (regression guard for the scanner-limit boundary).
func TestParsePerchLabels_MaxLengthLine(t *testing.T) {
	t.Parallel()

	maxLabel := strings.Repeat("A", maxLabelLineBytes)
	for _, term := range []string{"", "\n", "\r\n"} {
		input := perchDatasetMarker + "\n" + maxLabel + term
		labels, err := ParsePerchLabels([]byte(input))
		require.NoError(t, err, "a maxLabelLineBytes line with terminator %q must parse", term)
		require.Len(t, labels, 1)
		assert.Equal(t, maxLabel, labels[0])
	}
}
