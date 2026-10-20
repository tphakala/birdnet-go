package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/securefs"
)

func TestResolveClipRel(t *testing.T) {
	t.Parallel()

	sfs, err := securefs.New(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sfs.Close() })

	tests := []struct {
		name    string
		clip    string
		want    string
		wantErr error
	}{
		{"prefix stripped", "clips/2026/01/a.wav", "2026/01/a.wav", nil},
		{"already relative", "2026/01/a.wav", "2026/01/a.wav", nil},
		{"traversal", "../escape.wav", "", ErrClipNameNotRelative},
		{"absolute", "/etc/passwd", "", ErrClipNameNotRelative},
		{"empty after prefix", "clips/", "", ErrClipNameNotRelative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveClipRel(sfs, tt.clip, "clips/")
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
