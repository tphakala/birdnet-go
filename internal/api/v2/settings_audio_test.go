package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAudioBlockedFields(t *testing.T) {
	t.Parallel()

	blocked := getAudioBlockedFields()

	// FfmpegPath must be blocked to prevent ingress/proxy path contamination
	assert.Equal(t, true, blocked["FfmpegPath"], "FfmpegPath must be blocked from API updates")

	// SoxPath must be blocked for the same reason
	assert.Equal(t, true, blocked["SoxPath"], "SoxPath must be blocked from API updates")

	// SoxAudioTypes was already blocked
	assert.Equal(t, true, blocked["SoxAudioTypes"], "SoxAudioTypes must be blocked from API updates")
}
