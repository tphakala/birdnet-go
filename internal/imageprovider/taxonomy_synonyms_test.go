package imageprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetTaxonomySynonym(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantSynonym string
		wantFound   bool
	}{
		{
			name:        "BirdNET name to updated name - Cooper's Hawk",
			input:       "Accipiter cooperii",
			wantSynonym: "Astur cooperii",
			wantFound:   true,
		},
		{
			name:        "updated name to BirdNET name - Cooper's Hawk reverse",
			input:       "Astur cooperii",
			wantSynonym: "Accipiter cooperii",
			wantFound:   true,
		},
		{
			name:        "case insensitive lookup",
			input:       "accipiter cooperii",
			wantSynonym: "Astur cooperii",
			wantFound:   true,
		},
		{
			name:        "Jackdaw - Corvus to Coloeus",
			input:       "Corvus monedula",
			wantSynonym: "Coloeus monedula",
			wantFound:   true,
		},
		{
			name:        "Jackdaw reverse - Coloeus to Corvus",
			input:       "Coloeus monedula",
			wantSynonym: "Corvus monedula",
			wantFound:   true,
		},
		{
			name:        "no synonym exists",
			input:       "Turdus merula",
			wantSynonym: "",
			wantFound:   false,
		},
		{
			name:        "empty string",
			input:       "",
			wantSynonym: "",
			wantFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synonym, found := GetTaxonomySynonym(tt.input)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantSynonym, synonym)
		})
	}
}

func TestTaxonomySynonymsCompleteness(t *testing.T) {
	t.Parallel()

	// Verify that every forward mapping has a working reverse mapping
	for old, updated := range builtInTaxonomySynonyms {
		t.Run(old+" forward", func(t *testing.T) {
			t.Parallel()
			synonym, found := GetTaxonomySynonym(old)
			assert.True(t, found, "forward lookup should find synonym for %s", old)
			assert.Equal(t, updated, synonym)
		})

		t.Run(updated+" reverse", func(t *testing.T) {
			t.Parallel()
			synonym, found := GetTaxonomySynonym(updated)
			assert.True(t, found, "reverse lookup should find synonym for %s", updated)
			assert.Equal(t, old, synonym)
		})
	}
}

func TestBuildSynonymIndexes_ConfigOverridesBuiltIn(t *testing.T) {
	t.Parallel()

	overrides := map[string]string{
		"Bubulcus ibis": "Ardea ibis", // Override built-in value.
	}

	forward, reverse := buildSynonymIndexes(overrides)

	updated, found := forward["bubulcus ibis"]
	assert.True(t, found)
	assert.Equal(t, "Ardea ibis", updated)

	original, found := reverse["ardea ibis"]
	assert.True(t, found)
	assert.Equal(t, "Bubulcus ibis", original)
}

func TestBuildSynonymIndexes_ConfigAddsCustomEntry(t *testing.T) {
	t.Parallel()

	overrides := map[string]string{
		"Oldus nameus": "Newus nameus",
	}

	forward, reverse := buildSynonymIndexes(overrides)

	updated, found := forward["oldus nameus"]
	assert.True(t, found)
	assert.Equal(t, "Newus nameus", updated)

	original, found := reverse["newus nameus"]
	assert.True(t, found)
	assert.Equal(t, "Oldus nameus", original)
}

func TestBuildSynonymIndexes_IgnoresBlankEntries(t *testing.T) {
	t.Parallel()

	overrides := map[string]string{
		"":              "Astur cooperii",
		"  ":            "Astur cooperii",
		"Turdus merula": "",
	}

	forward, _ := buildSynonymIndexes(overrides)

	_, found := forward[""]
	assert.False(t, found)

	_, found = forward["turdus merula"]
	assert.False(t, found)
}
