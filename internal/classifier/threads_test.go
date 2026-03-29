package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDivideThreads_EqualSplit(t *testing.T) {
	t.Parallel()
	result := divideThreads(8, []string{"model-a", "model-b"}, "model-a")
	assert.Equal(t, 4, result["model-a"])
	assert.Equal(t, 4, result["model-b"])
}

func TestDivideThreads_RemainderToPrimary(t *testing.T) {
	t.Parallel()
	result := divideThreads(7, []string{"model-a", "model-b"}, "model-a")
	assert.Equal(t, 4, result["model-a"])
	assert.Equal(t, 3, result["model-b"])
}

func TestDivideThreads_MinimumOnePerModel(t *testing.T) {
	t.Parallel()
	result := divideThreads(2, []string{"a", "b", "c"}, "a")
	assert.Equal(t, 1, result["a"])
	assert.Equal(t, 1, result["b"])
	assert.Equal(t, 1, result["c"])
}

func TestDivideThreads_SingleModel(t *testing.T) {
	t.Parallel()
	result := divideThreads(4, []string{"only"}, "only")
	assert.Equal(t, 4, result["only"])
}
