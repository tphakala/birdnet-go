package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
)

func TestWithAudioEngineOption(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()

	opt := WithAudioEngine(eng)
	opt(c)
	require.Same(t, eng, c.Engine.Load())
}
