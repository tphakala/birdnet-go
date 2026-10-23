package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveCommonName(t *testing.T) {
	t.Parallel()

	m := map[string]string{
		"Turdus merula": "Eurasian Blackbird",
		"Parus major":   "Great Tit",
	}
	assert.Equal(t, "Eurasian Blackbird", ResolveCommonName(m, "Turdus merula"))
	// A miss falls back to the input verbatim.
	assert.Equal(t, "Unknown species", ResolveCommonName(m, "Unknown species"))
}
