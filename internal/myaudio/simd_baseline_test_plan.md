# SIMD Optimization Baseline Test Plan

This document outlines the unit tests and benchmarks needed to capture baseline functionality and performance before implementing SIMD optimizations.

## Summary of Coverage Gaps

| Function | File | Unit Tests | Benchmarks | Priority |
|----------|------|------------|------------|----------|
| `calculateRMS` | soundlevel.go:426 | **MISSING** | **MISSING** | HIGH |
| `convert16BitToFloat32` | process.go:209 | EXISTS | EXISTS | - |
| `convert24BitToFloat32` | process.go:232 | PARTIAL | **MISSING** | MEDIUM |
| `convert32BitToFloat32` | process.go:249 | PARTIAL | **MISSING** | MEDIUM |
| Min/Max/Sum stats | soundlevel.go:454 | **MISSING** | **MISSING** | LOW |
| `ResampleAudio` | resample.go:4 | **MISSING** | **MISSING** | MEDIUM |
| `Filter.ApplyBatch` | equalizer.go:103 | **MISSING** | **MISSING** | LOW |
| Clamp operation | audio_filters.go:357 | **MISSING** | **MISSING** | MEDIUM |

---

## 1. RMS Calculation Tests (`soundlevel_rms_test.go`)

### Unit Tests

```go
// TestCalculateRMS_BasicCases
- Empty slice returns 0.0
- Single sample (1.0) returns 1.0
- Single sample (-1.0) returns 1.0
- Two samples [1.0, -1.0] returns 1.0
- Known values: [0.5, 0.5, 0.5, 0.5] returns 0.5
- All zeros returns 0.0

// TestCalculateRMS_SineWave
- Full cycle sine wave at different amplitudes
- Verify RMS = amplitude / sqrt(2) for pure sine

// TestCalculateRMS_EdgeCases
- Very small values (1e-10) - precision test
- Very large values (1e10) - overflow test
- Mixed positive/negative values
- Subnormal floating point values

// TestCalculateRMS_AudioRealistic
- Silence (all zeros)
- White noise (random values in [-1, 1])
- Typical audio signal levels (-40dB to 0dB equivalent)
```

### Benchmarks

```go
// BenchmarkCalculateRMS_Sizes
- 1000 samples (small buffer)
- 48000 samples (1 second at 48kHz)
- 144000 samples (3 seconds - standard buffer)

// BenchmarkCalculateRMS_DataPatterns
- Zeros (best case for branch prediction)
- Sequential values
- Random values (realistic)
- Alternating signs

// BenchmarkCalculateRMS_MultipleBands
- Multiple octave bands RMS calculations
```

---

## 2. Statistics Functions Tests (`soundlevel_stats_test.go`)

### Unit Tests for generateSoundLevelData internals

```go
// TestMinMaxSum_BasicCases
- Single value
- All same values
- Ascending sequence
- Descending sequence
- Random order

// TestMinMaxSum_EdgeCases
- Contains NaN (should be skipped)
- Contains +Inf (should be skipped)
- Contains -Inf (should be skipped)
- All values non-finite (edge case)
- Empty slice
```

### Benchmarks

```go
// BenchmarkMinMaxSum_Sizes
- 5 values (minimum interval)
- 10 values (typical interval)
- 60 values (maximum reasonable interval)
```

---

## 3. Resampling Tests (`resample_test.go`)

### Unit Tests

```go
// TestResampleAudio_SameRate
- Same input/output rate returns original slice
- No allocation for same rate

// TestResampleAudio_Upsampling
- 44100 -> 48000 (common case)
- 16000 -> 48000 (3x upsampling)
- Verify output length is correct

// TestResampleAudio_Downsampling
- 48000 -> 44100
- 96000 -> 48000
- Verify output length is correct

// TestResampleAudio_Correctness
- DC signal (constant value) remains constant after resampling
- Known frequency preserved (simple sine wave)
- Boundary values (first/last samples)

// TestResampleAudio_EdgeCases
- Very short input (< 4 samples for cubic interp)
- Single sample
- Empty input
- Large ratio upsampling (8000 -> 48000)
```

### Benchmarks

```go
// BenchmarkResampleAudio_CommonRates
- 44100 -> 48000 (1 second)
- 16000 -> 48000 (1 second)
- 96000 -> 48000 (1 second)

// BenchmarkResampleAudio_Sizes
- 1 second audio at various rates
- 3 seconds audio (standard buffer)
- 10 seconds audio (file processing)
```

---

## 4. Audio Conversion Tests (extend existing)

### Additional Benchmarks (`audio_conversion_bench_test.go`)

```go
// BenchmarkConvert24BitToFloat32_Sizes
- Same size variations as 16-bit

// BenchmarkConvert32BitToFloat32_Sizes
- Same size variations as 16-bit

// BenchmarkConvertToFloat32_AllBitDepths
- Compare 16/24/32-bit conversion speeds
- Same byte count for fair comparison
```

---

## 5. Equalizer Filter Tests (`equalizer/equalizer_test.go`)

### Unit Tests

```go
// TestFilter_ApplyBatch_Correctness
- DC signal through lowpass (should pass)
- DC signal through highpass (should attenuate)
- Known impulse response verification

// TestFilter_ApplyBatch_InPlace
- Verify input slice is modified in place
- Original values are replaced

// TestFilterChain_ApplyBatch
- Empty chain (no modification)
- Single filter
- Multiple filters in sequence
```

### Benchmarks

```go
// BenchmarkFilter_ApplyBatch_Sizes
- 1000 samples
- 48000 samples (1 second)
- 144000 samples (3 seconds)

// BenchmarkFilter_ApplyBatch_Passes
- 1 pass (12dB/oct)
- 2 passes (24dB/oct)
- 4 passes (48dB/oct)

// BenchmarkFilterChain_ApplyBatch
- 1 filter
- 5 filters (typical EQ)
- 10 filters
```

---

## 6. Clamping Tests (`audio_filters_test.go`)

### Unit Tests

```go
// TestClampSamples_Correctness
- Values within range unchanged
- Values > 1.0 clamped to 1.0
- Values < -1.0 clamped to -1.0
- Boundary values (exactly 1.0, -1.0)

// TestClampSamples_Precision
- Values very close to boundaries (0.9999999, 1.0000001)
```

### Benchmarks

```go
// BenchmarkClampSamples_Sizes
- 1000 samples
- 48000 samples
- 144000 samples

// BenchmarkClampSamples_DataPatterns
- All within range (no clamping needed)
- All above range (all clamped)
- Mixed (realistic)
```

---

## Implementation Order

1. **Phase 1 - High Priority (RMS)**
   - Create `soundlevel_rms_test.go`
   - Add unit tests for `calculateRMS`
   - Add benchmarks for `calculateRMS`

2. **Phase 2 - Medium Priority (Resampling)**
   - Create `resample_test.go`
   - Add unit tests for `ResampleAudio`
   - Add benchmarks for `ResampleAudio`

3. **Phase 3 - Medium Priority (Conversion)**
   - Extend `audio_conversion_bench_test.go` for 24/32-bit
   - Add comparison benchmarks

4. **Phase 4 - Lower Priority (Filters)**
   - Create `equalizer/equalizer_test.go`
   - Add unit tests and benchmarks

5. **Phase 5 - Lower Priority (Clamping)**
   - Add to `audio_filters_test.go`
   - Extract clamp logic to testable function

---

## Running Baseline Benchmarks

```bash
# Run all SIMD-candidate benchmarks and save results
go test -bench='RMS|Resample|Convert.*Float32|ApplyBatch|Clamp' \
    -benchmem -count=10 ./internal/myaudio/... \
    | tee baseline_benchmarks.txt

# Generate comparison-friendly output
go test -bench='RMS|Resample|Convert.*Float32|ApplyBatch|Clamp' \
    -benchmem -count=10 ./internal/myaudio/... \
    > baseline_$(date +%Y%m%d).txt
```

## Post-SIMD Comparison

After implementing SIMD optimizations:

```bash
# Run benchmarks with SIMD implementation
go test -bench='RMS|Resample|Convert.*Float32|ApplyBatch|Clamp' \
    -benchmem -count=10 ./internal/myaudio/... \
    > simd_$(date +%Y%m%d).txt

# Compare with benchstat
benchstat baseline_*.txt simd_*.txt
```
