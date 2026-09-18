package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDivideThreads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		threads int
		models  []string
		want    map[string]int
	}{
		{
			name:    "equal split",
			threads: 8,
			models:  []string{"model-a", "model-b"},
			want:    map[string]int{"model-a": 4, "model-b": 4},
		},
		{
			name:    "remainder goes to the first model",
			threads: 7,
			models:  []string{"model-a", "model-b"},
			want:    map[string]int{"model-a": 4, "model-b": 3},
		},
		{
			name:    "minimum one thread per model",
			threads: 2,
			models:  []string{"a", "b", "c"},
			want:    map[string]int{"a": 1, "b": 1, "c": 1},
		},
		{
			name:    "single model gets all threads",
			threads: 4,
			models:  []string{"only"},
			want:    map[string]int{"only": 4},
		},
		{
			name:    "two models with real registry IDs",
			threads: 4,
			models:  []string{RegistryIDBirdNETV24, RegistryIDPerchV2},
			want:    map[string]int{RegistryIDBirdNETV24: 2, RegistryIDPerchV2: 2},
		},
		{
			name:    "single model with a real registry ID",
			threads: 4,
			models:  []string{RegistryIDBirdNETV24},
			want:    map[string]int{RegistryIDBirdNETV24: 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := divideThreads(tt.threads, tt.models)
			assert.Equal(t, tt.want, got)
		})
	}
}
