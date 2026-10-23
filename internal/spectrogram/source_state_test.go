package spectrogram

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// sourceStateOldAge is how far back a test moves a clip's mtime.
	sourceStateOldAge = time.Hour
	// sourceStateTouch is how far a "touched" clip's mtime moves.
	sourceStateTouch = time.Minute
	// sourceStateClipBytes is the size of the clip when its state is captured, and
	// sourceStateGrownBytes its size after it grows.
	sourceStateClipBytes  = 64
	sourceStateGrownBytes = 128
)

// withBeforePublish runs fn after the render and before the source is compared
// and the PNG published, to change the file at the last moment.
func withBeforePublish(fn func()) GenerateOption {
	return func(o *generateOptions) { o.beforePublish = fn }
}

func statSourceState(t *testing.T, path string) SourceState {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return NewSourceState(info, 0)
}

func TestGenerateFromFile_RefusesToPublishWhenSourceChanged(t *testing.T) {

	appendBytes := func(t *testing.T, path string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		_, err = f.Write(make([]byte, 64))
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	replaceSameSizeAndTime := func(t *testing.T, path string) {
		t.Helper()
		info, err := os.Stat(path)
		require.NoError(t, err)
		data, err := os.ReadFile(path) //nolint:gosec // G304: test path
		require.NoError(t, err)
		other := path + ".new"
		require.NoError(t, os.WriteFile(other, data, 0o600))
		require.NoError(t, os.Chtimes(other, info.ModTime(), info.ModTime()))
		require.NoError(t, os.Rename(other, path))
	}
	touch := func(t *testing.T, path string) {
		t.Helper()
		later := time.Now().Add(sourceStateTouch)
		require.NoError(t, os.Chtimes(path, later, later))
	}
	remove := func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.Remove(path))
	}

	tests := []struct {
		name   string
		change func(t *testing.T, path string)
		// afterRender applies the change in the window after the render; otherwise
		// it is applied before GenerateFromFile is called.
		afterRender bool
		wantChanged bool
		// wantMissing marks a clip removed before the render, which the existing
		// access check reports as a missing file (a 404 upstream), not as a change.
		wantMissing bool
	}{
		{name: "unchanged publishes", change: func(*testing.T, string) {}},
		{name: "bytes appended before render", change: appendBytes, wantChanged: true},
		{name: "replaced by rename before render", change: replaceSameSizeAndTime, wantChanged: true},
		{name: "mtime touched before render", change: touch, wantChanged: true},
		{name: "removed before render", change: remove, wantMissing: true},
		{name: "removed after render", change: remove, afterRender: true, wantChanged: true},
		{name: "bytes appended after render", change: appendBytes, afterRender: true, wantChanged: true},
		{name: "replaced by rename after render", change: replaceSameSizeAndTime, afterRender: true, wantChanged: true},
		{name: "mtime touched after render", change: touch, afterRender: true, wantChanged: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, env := newNativeGenerator(t)
			clip := writeNativeWAV(t, env.TempDir, "clip.wav", nativeTestSamples(nativeCodecRate), nativeCodecRate)
			old := time.Now().Add(-sourceStateOldAge)
			require.NoError(t, os.Chtimes(clip, old, old))
			state := statSourceState(t, clip)
			out := filepath.Join(env.TempDir, "out.png")

			opts := []GenerateOption{WithSourceState(state)}
			if tt.afterRender {
				opts = append(opts, withBeforePublish(func() { tt.change(t, clip) }))
			} else {
				tt.change(t, clip)
			}
			err := g.GenerateFromFile(t.Context(), clip, out, nativeTestWidth, false, opts...)

			switch {
			case tt.wantMissing:
				require.ErrorIs(t, err, os.ErrNotExist)
				require.NotErrorIs(t, err, ErrSourceChanged)
			case tt.wantChanged:
				require.ErrorIs(t, err, ErrSourceChanged)
			default:
				require.NoError(t, err)
				requireNativePNG(t, out)
				return
			}
			assert.NoFileExists(t, out, "nothing is published")
			temps, globErr := filepath.Glob(filepath.Join(env.TempDir, "*.temp"))
			require.NoError(t, globErr)
			assert.Empty(t, temps, "no temp file is left behind")
		})
	}
}

func TestGenerateFromFile_ChangedSourceOutranksDecodeError(t *testing.T) {
	g, env := newNativeGenerator(t)

	// A clip cut short decodes with an error; because the file also changed since
	// readiness, the answer is the retryable one.
	clip := writeNativeWAV(t, env.TempDir, "clip.wav", nativeTestSamples(nativeCodecRate), nativeCodecRate)
	data, err := os.ReadFile(clip) //nolint:gosec // G304: test path
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(clip, data[:len(data)/2], 0o600))
	state := statSourceState(t, clip)
	out := filepath.Join(env.TempDir, "out.png")

	err = g.GenerateFromFile(t.Context(), clip, out, nativeTestWidth, false,
		WithSourceState(state),
		withBeforePublish(func() { require.NoError(t, os.WriteFile(clip, data, 0o600)) }))

	require.ErrorIs(t, err, ErrSourceChanged)
	assert.NoFileExists(t, out)
}

func TestCheckSourceUnchanged(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "clip.wav")
	require.NoError(t, os.WriteFile(path, make([]byte, sourceStateClipBytes), 0o600))
	state := statSourceState(t, path)
	require.NoError(t, os.WriteFile(path, make([]byte, sourceStateGrownBytes), 0o600)) // the clip grew

	live := t.Context()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	renderFailure := errors.NewStd("render failed")

	tests := []struct {
		name      string
		ctx       context.Context
		source    *SourceState
		renderErr error
		want      error
		wantNil   bool
	}{
		{name: "no state supplied", ctx: live, wantNil: true},
		{name: "changed file, render succeeded", ctx: live, source: &state, want: ErrSourceChanged},
		{name: "changed file outranks the render error", ctx: live, source: &state, renderErr: renderFailure, want: renderFailure},
		{name: "interrupted render passes through", ctx: canceled, source: &state, renderErr: context.Canceled, wantNil: true},
		{name: "changed file after a finished render is not excused by a done context", ctx: canceled, source: &state, want: ErrSourceChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkSourceUnchanged(tt.ctx, tt.source, path, tt.renderErr)
			if tt.wantNil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.want)
			if tt.renderErr != nil {
				require.ErrorIs(t, err, ErrSourceChanged)
			}
		})
	}
}
