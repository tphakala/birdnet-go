package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBirdNETLabelResolver_KnownSpecies(t *testing.T) {
	t.Parallel()

	labels := []string{
		"Abroscopus albogularis_Rufous-faced Warbler",
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
	}
	resolver := NewBirdNETLabelResolver(labels)

	assert.Equal(t, "Eurasian Blackbird", resolver.Resolve("Turdus merula", "en"))
	assert.Equal(t, "Great Tit", resolver.Resolve("Parus major", "en"))
	assert.Equal(t, "Rufous-faced Warbler", resolver.Resolve("Abroscopus albogularis", "en"))
}

func TestBirdNETLabelResolver_UnknownSpecies(t *testing.T) {
	t.Parallel()

	labels := []string{
		"Turdus merula_Eurasian Blackbird",
	}
	resolver := NewBirdNETLabelResolver(labels)

	assert.Empty(t, resolver.Resolve("Nonexistent species", "en"))
}

func TestBirdNETLabelResolver_CaseInsensitive(t *testing.T) {
	t.Parallel()

	labels := []string{
		"Turdus merula_Eurasian Blackbird",
	}
	resolver := NewBirdNETLabelResolver(labels)

	assert.Equal(t, "Eurasian Blackbird", resolver.Resolve("turdus merula", "en"))
	assert.Equal(t, "Eurasian Blackbird", resolver.Resolve("TURDUS MERULA", "en"))
}

func TestBirdNETLabelResolver_EmptyLabels(t *testing.T) {
	t.Parallel()

	resolver := NewBirdNETLabelResolver(nil)
	assert.Empty(t, resolver.Resolve("Turdus merula", "en"))
}

// Compile-time check that BirdNETLabelResolver implements NameResolver.
var _ NameResolver = (*BirdNETLabelResolver)(nil)
