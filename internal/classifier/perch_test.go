package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	labels, err := ParsePerchLabels([]byte("inat2024_fsd50k\n"))
	require.NoError(t, err)
	assert.Empty(t, labels)
}
