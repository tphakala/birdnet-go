//go:build windows

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindShell_Windows(t *testing.T) {
	t.Parallel()
	shell := findShell()
	assert.NotEmpty(t, shell)
	assert.Regexp(t, `(?i)(pwsh|powershell|cmd)\.exe`, shell)
}
