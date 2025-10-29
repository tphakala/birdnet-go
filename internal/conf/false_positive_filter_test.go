package conf

import (
	"testing"
)

func TestFalsePositiveFilterSettings_Validate(t *testing.T) {
	tests := []struct {
		name      string
		level     int
		wantError bool
	}{
		{"level_0_off", 0, false},
		{"level_1_lenient", 1, false},
		{"level_2_moderate", 2, false},
		{"level_3_balanced", 3, false},
		{"level_4_strict", 4, false},
		{"level_5_maximum", 5, false},
		{"level_negative", -1, true},
		{"level_too_high", 6, true},
		{"level_way_too_high", 99, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FalsePositiveFilterSettings{
				Level: tt.level,
			}

			err := f.Validate()

			if tt.wantError && err == nil {
				t.Errorf("Validate() expected error for level %d, but got nil", tt.level)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Validate() unexpected error for level %d: %v", tt.level, err)
			}
		})
	}
}
