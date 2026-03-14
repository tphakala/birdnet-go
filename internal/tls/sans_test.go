package tls

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectSANs_WithHostAndBaseURL(t *testing.T) {
	t.Parallel()

	sans := CollectSANs("birds.example.com", "https://birdnet.example.com:8443")

	assert.Contains(t, sans, "birds.example.com")
	assert.Contains(t, sans, "birdnet.example.com")
	assert.Contains(t, sans, "localhost")
	assert.Contains(t, sans, "127.0.0.1")
}

func TestCollectSANs_Deduplication(t *testing.T) {
	t.Parallel()

	sans := CollectSANs("localhost", "https://localhost")

	count := 0
	for _, s := range sans {
		if s == "localhost" {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func TestCollectSANs_EmptyInputs(t *testing.T) {
	t.Parallel()

	sans := CollectSANs("", "")

	assert.Contains(t, sans, "localhost")
	assert.Contains(t, sans, "127.0.0.1")
}

func TestCollectSANs_BaseURLHostnameExtraction(t *testing.T) {
	t.Parallel()

	sans := CollectSANs("", "https://mybird.example.com:5500/path")

	assert.Contains(t, sans, "mybird.example.com")
}
