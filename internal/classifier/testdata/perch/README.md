# Perch score fixtures

Small derived numeric fixtures for the Perch scoring tests. No audio and no ONNX
models are stored here.

## full_logits.bin

Three windows of real full-model Perch v2 logits, 3 x 14795 float32 little-endian
(177540 bytes), used by `TestPerchPredict_FullModelScoresGolden` together with
`full_scores.golden.json` (top-K confidences as `math.Float32bits`).

- Source model: `perch_v2_no_dft.onnx` (fp32), SHA-256
  `4dcf71c18a147198545944bb5149697e89e3ad2e16637fa8f0edf6d13035a017`, run with
  ONNX Runtime 1.31.1 CPU on 32 kHz mono, 5 s windows.
- Window 0: digital silence (all-zero input).
- Window 1: a night background window (top logit between 6 and 8) from a held-out
  night recording of a private station corpus.
- Window 2: a bird window (top logit of at least 11) from a held-out daytime
  recording of the same corpus.
- Windows with a human-sound class among the five highest logits were excluded.
- Corpus fingerprint (SHA-256 over the sorted clip SHA-256 values, 219 clips):
  `2941fe42d6c515f373f2c3bf8fada0ab81853c302f00ccf312f974bea7e4b79c`.

The labels are synthesized in the test; the golden pins scores, not label parsing.
The golden was recorded on unchanged code: regenerate it only for a deliberate
change of the full-model scoring, with `UPDATE_PERCH_GOLDEN=1`.

## {nordic,north-america-east}_windows.{bin,json}

Real slice logits for `TestPerchPredict_SliceScoresTrackFullModel`. Per tile, 30
windows from the same held-out recordings (see above): `<tile>_windows.bin` holds
30 x `classes` float32 little-endian values, the full model's logits gathered on the
tile's kept classes in the tile's own index order (a slice's logits are a bit-exact
gather of the full model's); `<tile>_windows.json` holds the header and, per window,
the float64 log-sum-exp of the full model's 14795 logits (`full_lse`) so the test
can form the full-model score `exp(z - lse)` without the full logits. `windows_src`
names the source window by clip content hash and window index; the recordings are
private and not stored.

Selection rule: recordings of the day and night groups whose content hash puts them in
the evaluation split (not used to fit the calibration constants); windows whose
full-model top class the tile keeps and with no human-sound class among the five
highest logits; every k-th of those in clip-hash order so that 30 are taken
(nordic: 1622 candidates, north-america-east: 480). Each recording is cut into 5 s
windows at a 1 s hop.

Source model and corpus are those of `full_logits.bin`. The windows were exported
from the cached full-model logits of the calibration pipeline in the
acoustic-models repository (`scripts/perch-calibrate`, branch
`feat/perch-slice-score-calibration`, commit e78f520) with a one-off script that is not
part of this repository.

## mauritius_labels.txt

The published labels file of the mauritius tile (272 lines), the smallest tile.
`TestPerchScoreCalibration` uses it as real labels bytes whose SHA-256 is in the
built-in catalog; `TestPerchScoreCalibration_LabelsFixtureMatchesCatalog` checks it
still matches.
