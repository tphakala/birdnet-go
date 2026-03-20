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

func TestBuildSynonymIndexes_ViperLowercasedKeysOverrideBuiltIn(t *testing.T) {
	t.Parallel()

	// Viper lowercases map keys during YAML deserialization.
	// Built-in has Title Case "Bubulcus ibis", Viper provides "bubulcus ibis".
	// The override must still win despite different casing.
	overrides := map[string]string{
		"bubulcus ibis": "Ardea ibis", // Viper-style lowercase key
	}

	forward, reverse := buildSynonymIndexes(overrides)

	// Forward must return the override value, not the built-in.
	updated, found := forward["bubulcus ibis"]
	assert.True(t, found)
	assert.Equal(t, "Ardea ibis", updated, "override must replace built-in even with lowercase key")

	// Old built-in reverse entry must not exist.
	_, found = reverse["ardea coromanda"]
	assert.False(t, found, "stale reverse entry for overridden built-in should not exist")

	// New reverse entry must exist.
	original, found := reverse["ardea ibis"]
	assert.True(t, found)
	assert.Equal(t, "bubulcus ibis", original)
}

func TestBuildSynonymIndexes_OverrideRemovesStaleReverse(t *testing.T) {
	t.Parallel()

	// Built-in maps "Bubulcus ibis" → "Ardea coromanda".
	// Override changes it to "Bubulcus ibis" → "Ardea ibis".
	// The old reverse entry "ardea coromanda" → "Bubulcus ibis" must NOT exist.
	overrides := map[string]string{
		"Bubulcus ibis": "Ardea ibis",
	}

	_, reverse := buildSynonymIndexes(overrides)

	_, found := reverse["ardea coromanda"]
	assert.False(t, found, "stale reverse entry for overridden built-in should not exist")
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

func TestSetCustomSynonyms_IntegrationWithGetTaxonomySynonym(t *testing.T) {
	// Not parallel: mutates package-level cache.

	// Add a custom synonym and verify it's visible via GetTaxonomySynonym.
	SetCustomSynonyms(map[string]string{
		"Testus oldus": "Testus newus",
	}, nil)
	t.Cleanup(func() {
		// Restore to built-ins only.
		SetCustomSynonyms(nil, nil)
	})

	synonym, found := GetTaxonomySynonym("Testus oldus")
	assert.True(t, found)
	assert.Equal(t, "Testus newus", synonym)

	// Built-in should still work.
	synonym, found = GetTaxonomySynonym("Accipiter cooperii")
	assert.True(t, found)
	assert.Equal(t, "Astur cooperii", synonym)
}

func TestSetCustomSynonyms_WarnsOnUnknownLabel(t *testing.T) {
	// Not parallel: mutates package-level cache.

	// Known labels in BirdNET format: "ScientificName_CommonName"
	knownLabels := []string{
		"Turdus merula_Common Blackbird",
		"Accipiter cooperii_Cooper's Hawk",
	}

	// "Fakeus birdus" does not match any known label — should warn but still apply.
	overrides := map[string]string{
		"Fakeus birdus":      "Newus birdus",
		"Accipiter cooperii": "Astur cooperii",
	}

	SetCustomSynonyms(overrides, knownLabels)
	t.Cleanup(func() {
		SetCustomSynonyms(nil, nil)
	})

	// Both overrides should still be applied despite the warning.
	synonym, found := GetTaxonomySynonym("Fakeus birdus")
	assert.True(t, found)
	assert.Equal(t, "Newus birdus", synonym)

	synonym, found = GetTaxonomySynonym("Accipiter cooperii")
	assert.True(t, found)
	assert.Equal(t, "Astur cooperii", synonym)
}

func TestSetCustomSynonyms_LogsOverrideSummary(t *testing.T) {
	// Not parallel: mutates package-level cache.

	overrides := map[string]string{
		"Bubulcus ibis": "Ardea ibis",   // Replaces built-in
		"Testus oldus":  "Testus newus", // New custom entry
	}

	// Should not panic or error — logging is best-effort.
	SetCustomSynonyms(overrides, nil)
	t.Cleanup(func() {
		SetCustomSynonyms(nil, nil)
	})

	// Verify the overrides are applied correctly.
	synonym, found := GetTaxonomySynonym("Bubulcus ibis")
	assert.True(t, found)
	assert.Equal(t, "Ardea ibis", synonym)

	synonym, found = GetTaxonomySynonym("Testus oldus")
	assert.True(t, found)
	assert.Equal(t, "Testus newus", synonym)
}
