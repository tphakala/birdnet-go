package csvutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeader_Col(t *testing.T) {
	t.Parallel()

	h := NewHeader([]string{"sci_name", " Com_Name ", "label"})

	assert.Equal(t, 0, h.Col("sci_name"))
	assert.Equal(t, 1, h.Col("com_name"), "match is case-insensitive and the header cell is whitespace-trimmed")
	assert.Equal(t, 2, h.Col("LABEL"))
	assert.Equal(t, 0, h.Col("  sci_name  "), "the queried name is also whitespace-trimmed")
	assert.Equal(t, -1, h.Col("missing"))
}

func TestHeader_Field(t *testing.T) {
	t.Parallel()

	h := NewHeader([]string{"scientific_name", "class", "order"})
	rec := []string{"Turdus merula", "Aves", "Passeriformes"}

	assert.Equal(t, "Aves", h.Field(rec, "class"))
	assert.Equal(t, "Passeriformes", h.Field(rec, "order"))
	assert.Empty(t, h.Field(rec, "family"), "absent column yields empty string")

	short := []string{"Turdus merula"} // record shorter than the header
	assert.Empty(t, h.Field(short, "class"), "missing field in short record yields empty string")
}

func TestHeader_DuplicateNames_FirstWins(t *testing.T) {
	t.Parallel()

	// Duplicate sits at a non-adjacent index so a last-wins bug would return 3.
	h := NewHeader([]string{"id", "name", "extra", "name"})
	assert.Equal(t, 1, h.Col("name"), "first occurrence wins over a later duplicate")

	// Duplicates are detected after case/whitespace folding.
	folded := NewHeader([]string{"Name", " name "})
	assert.Equal(t, 0, folded.Col("name"), "case/whitespace-folded duplicates collapse, first wins")
}

func TestHeader_BlankCellsSkipped(t *testing.T) {
	t.Parallel()

	h := NewHeader([]string{"a", "", "   ", "b"})
	assert.Equal(t, 0, h.Col("a"))
	assert.Equal(t, 3, h.Col("b"), "blank and whitespace-only header cells are skipped, not indexed")
	assert.Equal(t, -1, h.Col(""), "an empty name never resolves to a column")
}

func TestHeader_ZeroValue(t *testing.T) {
	t.Parallel()

	var h Header
	assert.Equal(t, -1, h.Col("anything"))
	assert.Empty(t, h.Field([]string{"x"}, "anything"))
}
