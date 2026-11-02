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
