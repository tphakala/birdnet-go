// Package normbench holds an on-demand comparison harness that measures the
// native Go loudness normalization (audiocore/audionorm plus audiocore/pcmgain)
// against FFmpeg's loudnorm filter on the same PCM.
//
// The export path no longer normalises with FFmpeg at all: it plans a gain in Go
// and, for the formats FFmpeg still encodes, hands that gain over as a plain
// volume filter. So loudnorm now exists here only as a reference point, built by
// the harness itself (loudnormFilter in compare_test.go) rather than borrowed
// from production. Keeping the comparison is the point: it is what says the
// removal did not change what listeners hear.
//
// It has no production code. The harness lives behind the "normcompare" build
// tag so it never runs in CI, where FFmpeg availability would make the numbers
// meaningless. Run it deliberately:
//
//	go test -tags normcompare -v ./internal/audiocore/normbench/
//
// Two harnesses run under that tag:
//
//   - TestCompareNormalization compares gain planning through a lossless FLAC
//     output, isolating the normalisation decision from codec loss.
//   - TestCompareLossyFormats runs both paths end to end for MP3, AAC and Opus,
//     the formats FFmpeg still encodes, and reports the per-format loudness delta
//     between the old loudnorm path and the resolved-gain path that replaced it.
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
