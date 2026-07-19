// Package normbench holds an on-demand comparison harness that measures the
// native Go loudness normalization (audiocore/audionorm plus audiocore/pcmgain)
// against FFmpeg's loudnorm filter on the same PCM.
//
// It has no production code. The harness lives behind the "normcompare" build
// tag so it never runs in CI, where FFmpeg availability would make the numbers
// meaningless. Run it deliberately:
//
//	go test -tags normcompare -v ./internal/audiocore/normbench/
//
// It needs an ffmpeg binary on PATH (or in BIRDNET_NORMCOMPARE_FFMPEG) and a
// corpus of real recordings. Synthetic tones are deliberately not used: the two
// implementations agree trivially on a steady tone and diverge only where EBU
// R128 gating and FFmpeg's linear/dynamic mode selection bite, which needs real
// material with a real noise floor.
//
// The corpus defaults to the WAV files in the repository root and can be
// pointed elsewhere with BIRDNET_NORMCOMPARE_CORPUS. Each source recording is
// decoded to the export PCM shape (48 kHz mono 16-bit), sliced into
// detection-length clips, and expanded into the loudness cases that matter for
// field recordings: as captured, attenuated, hot, and a loud transient over a
// quiet bed.
//
// For every case it reports the input loudness, the gain each implementation
// chose, and the measured integrated loudness, true peak and loudness range of
// both outputs, so the divergence can be read off directly rather than
// asserted.
package normbench
