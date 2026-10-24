# sox reference renders

Reference images for `TestSoxReferenceParity` in `internal/spectrogram`.

- `clip.wav`: a 2 s, 24 kHz, mono, 16-bit clip made by `parityClipSamples` in
  `internal/spectrogram/native_parity_test.go` (fixed-seed integer noise, two
  tones, a chirp and a burst near the end). `TestParityClipMatchesGenerator`
  checks the file against the generator to within 1 LSB.
- `*.png`: renders of that clip by the sox binary, frozen goldens that no code in
  the repository regenerates. The command line, with `<width>`, `<rows>`,
  `<range>` and the style arguments of each case:
  `sox clip.wav -n rate 24000 spectrogram -x <width> -y <rows> -d 2 -z <range> -o <png> [-r] [style args]`.
  The 24 kHz input makes `rate 24000` a no-op, so the resampler is not part of
  the comparison.

Generated with `SoX v14.4.2` (Debian/Ubuntu package `14.4.2+git20190427-5build1`).

To regenerate by hand, install that sox version and run the command above for
each case in `parityCases` (`internal/spectrogram/native_parity_test.go`). If
`clip.wav` itself is ever needed again, write it with
`writeNativeWAV(t, dir, "clip.wav", parityClipSamples(), parityClipRate)` from a
throwaway test.
