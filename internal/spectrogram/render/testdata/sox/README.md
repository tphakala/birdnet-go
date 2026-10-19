# sox reference renders

Reference images for `TestSoxReferenceParity` in `internal/spectrogram`.

- `clip.wav`: a 2 s, 24 kHz, mono, 16-bit clip made by `parityClipSamples` in
  `internal/spectrogram/native_parity_test.go` (fixed-seed integer noise, two
  tones, a chirp and a burst near the end). `TestParityClipMatchesGenerator`
  checks the file against the generator to within 1 LSB.
- `*.png`: renders of that clip by the sox binary through the generator with the
  native gate off, the same arguments `getSoxSpectrogramArgs` builds:
  `sox clip.wav -n rate 24000 spectrogram -x <width> -y <rows> -d 2 -z <range> -o <png> [-r] [style args]`.
  The 24 kHz input makes `rate 24000` a no-op, so the resampler is not part of
  the comparison.

Generated with `SoX v14.4.2` (Debian/Ubuntu package `14.4.2+git20190427-5build1`),
the version CI installs.

Regenerate with the installed sox:

```sh
SOX_REGEN=1 go test -run 'TestParityClipMatchesGenerator|TestSoxReferenceParity' ./internal/spectrogram/
```
