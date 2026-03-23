package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAudioLevelData_Fields(t *testing.T) {
	t.Parallel()
	data := AudioLevelData{
		Level:    75,
		Clipping: true,
		Source:   "rtsp://cam1",
		Name:     "Front Yard",
	}
	assert.Equal(t, 75, data.Level)
	assert.True(t, data.Clipping)
	assert.Equal(t, "rtsp://cam1", data.Source)
	assert.Equal(t, "Front Yard", data.Name)
}
