package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBirdNETBackendPreferenceField(t *testing.T) {
	t.Parallel()
	var s Settings
	s.BirdNET.Backend = BackendPrefOpenVINO
	s.BirdNET.OpenVINOPath = "/usr/lib/libopenvino_c.so"
	assert.Equal(t, "openvino", s.BirdNET.Backend)
	assert.Equal(t, "/usr/lib/libopenvino_c.so", s.BirdNET.OpenVINOPath)
}
