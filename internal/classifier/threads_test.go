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

func TestDivideThreads_TwoModels_RealIDs(t *testing.T) {
	t.Parallel()

	result := divideThreads(4, []string{"BirdNET_V2.4", "Perch_V2"}, "BirdNET_V2.4")

	assert.Equal(t, 2, result["BirdNET_V2.4"], "4/2 = 2 base threads each, 0 remainder")
	assert.Equal(t, 2, result["Perch_V2"], "4/2 = 2 base threads each")
}

func TestDivideThreads_SingleModel_RealID(t *testing.T) {
	t.Parallel()

	result := divideThreads(4, []string{"BirdNET_V2.4"}, "BirdNET_V2.4")

	assert.Equal(t, 4, result["BirdNET_V2.4"], "single model gets all threads")
}
