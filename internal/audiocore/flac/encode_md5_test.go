package flac

import (
	"bytes"
	"crypto/md5" //nolint:gosec // FLAC STREAMINFO uses MD5 as a content digest, not for security
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goflac "github.com/tphakala/go-flac/pcm"
)

// streamInfoMD5 decodes the STREAMINFO header of a FLAC stream and returns its MD5.
func streamInfoMD5(t *testing.T, flacBytes []byte) [md5.Size]byte {
	t.Helper()
	dec, err := goflac.NewDecoder(bytes.NewReader(flacBytes))
	require.NoError(t, err)
	return dec.Info().MD5
}

// TestEncodePCMToBuffer_LeavesMD5Unknown pins that the in-memory path writes the
// all-zero "unknown" MD5 sentinel.
func TestEncodePCMToBuffer_LeavesMD5Unknown(t *testing.T) {
	t.Parallel()
	buf, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(makeTestPCM(20000)))
	require.NoError(t, err)
	assert.Equal(t, [md5.Size]byte{}, streamInfoMD5(t, buf.Bytes()))
}

// TestEncodePCM_WritesRealMD5 pins that the file path records the real MD5 of
// the PCM, including when it reuses a pooled encoder that last served the
// in-memory path. Not parallel: it exercises the shared pool in sequence.
func TestEncodePCM_WritesRealMD5(t *testing.T) {
	pcm := makeTestPCM(20000)
	want := md5.Sum(pcm) //nolint:gosec // content digest, see import
	path := filepath.Join(t.TempDir(), "clip.flac")

	_, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(pcm))
	require.NoError(t, err)
	require.NoError(t, EncodePCM(t.Context(), baseOpts(path, pcm)))

	data, err := os.ReadFile(path) //nolint:gosec // test temp path
	require.NoError(t, err)
	assert.Equal(t, want, streamInfoMD5(t, data))
}
