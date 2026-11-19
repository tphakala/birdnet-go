package openvino

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsGPUDevice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		device string
		want   bool
	}{
		{"bare alias", "GPU", true},
		{"first index", "GPU.0", true},
		{"multi digit index", "GPU.12", true},
		{"empty index", "GPU.", false},
		{"non numeric index", "GPU.x", false},
		{"signed index", "GPU.-1", false},
		{"nested index", "GPU.0.1", false},
		{"suffix without dot", "GPUX", false},
		{"lowercase", "gpu", false},
		{"cpu", "CPU", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsGPUDevice(tt.device))
		})
	}
}
