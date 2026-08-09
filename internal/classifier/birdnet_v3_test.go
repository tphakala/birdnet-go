package classifier

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBirdNETV3Labels(t *testing.T) {
	t.Parallel()

	const bom = "\uFEFF"

	tests := []struct {
		name string
		data string
		want []string
	}{
		{
			name: "scientific and common name pairs",
			data: "Turdus merula_Eurasian Blackbird\nParus major_Great Tit\n",
			want: []string{"Turdus merula_Eurasian Blackbird", "Parus major_Great Tit"},
		},
		{
			name: "strips utf-8 bom on first line only",
			data: bom + "Turdus merula_Eurasian Blackbird\nParus major_Great Tit\n",
			want: []string{"Turdus merula_Eurasian Blackbird", "Parus major_Great Tit"},
		},
		{
			name: "skips blank and whitespace-only lines",
			data: "Turdus merula_Eurasian Blackbird\n\n   \nParus major_Great Tit\n",
			want: []string{"Turdus merula_Eurasian Blackbird", "Parus major_Great Tit"},
		},
		{
			name: "trims surrounding whitespace and CR",
			data: "  Turdus merula_Eurasian Blackbird  \r\nParus major_Great Tit\r\n",
			want: []string{"Turdus merula_Eurasian Blackbird", "Parus major_Great Tit"},
		},
		{
			name: "no trailing newline",
			data: "Turdus merula_Eurasian Blackbird",
			want: []string{"Turdus merula_Eurasian Blackbird"},
		},
		{
			name: "empty input yields no labels",
			data: "",
			want: nil,
		},
		{
			// A stray header is NOT skipped: it is returned as a label so the count
			// mismatch surfaces at model load instead of silently shifting labels.
			name: "does not skip a header line",
			data: "idx;id;sci_name;com_name\nTurdus merula_Eurasian Blackbird\n",
			want: []string{"idx;id;sci_name;com_name", "Turdus merula_Eurasian Blackbird"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBirdNETV3Labels([]byte(tt.data))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseBirdNETV3Labels_LargeFile checks the parser handles a full-size label
// file (11,560 classes) without truncation and preserves order.
func TestParseBirdNETV3Labels_LargeFile(t *testing.T) {
	t.Parallel()

	const n = 11560
	var sb strings.Builder
	for i := range n {
		fmt.Fprintf(&sb, "Genus%d_Common %d\n", i, i)
	}
	got, err := ParseBirdNETV3Labels([]byte(sb.String()))
	require.NoError(t, err)
	require.Len(t, got, n)
	// Order is preserved: spot-check first, middle, and last labels.
	assert.Equal(t, "Genus0_Common 0", got[0])
	assert.Equal(t, "Genus5780_Common 5780", got[5780])
	assert.Equal(t, "Genus11559_Common 11559", got[n-1])
}
