//go:build !windows

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindShell_Unix(t *testing.T) {
	t.Parallel()
	shell := findShell()
	assert.NotEmpty(t, shell)
	assert.Contains(t, []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"}, shell)
}
