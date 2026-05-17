package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
)

func TestEngineAtomicLoadStore(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	// Initially nil.
	assert.Nil(t, c.engine.Load())

	// Store and load back.
	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()
	c.engine.Store(eng)
	require.Same(t, eng, c.engine.Load())
}

func TestWithAudioEngineOption(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()

	opt := WithAudioEngine(eng)
	opt(c)
	require.Same(t, eng, c.engine.Load())
}
