package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHour(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "valid zero", input: "0", want: 0},
		{name: "valid single digit", input: "6", want: 6},
		{name: "valid zero-padded", input: "08", want: 8},
		{name: "valid max hour", input: "23", want: 23},
		{name: "valid noon", input: "12", want: 12},
		{name: "invalid negative", input: "-1", wantErr: true},
		{name: "invalid too large", input: "24", wantErr: true},
		{name: "invalid non-numeric", input: "abc", wantErr: true},
		{name: "invalid empty", input: "", wantErr: true},
		{name: "invalid float", input: "3.5", wantErr: true},
		{name: "invalid large number", input: "100", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseHour(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidHour)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
