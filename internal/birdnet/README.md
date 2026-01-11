# BirdNET Package

The BirdNET package forms the core of the BirdNET-Go application, providing functionality for bird species identification using machine learning techniques. This package interfaces with TensorFlow Lite models to analyze audio data and identify bird species based on their sounds.

## Package Overview

The package implements the following key functionalities:

- Loading and initialization of TensorFlow Lite models for bird species recognition
- Audio sample processing and analysis
- Species probability calculation and filtering based on geographic location and time of year
- Results queuing and processing for downstream consumption
- Taxonomy integration with eBird taxonomy codes

## Core Components

### BirdNET Struct

The main structure that encapsulates the TensorFlow Lite interpreters and configuration settings.

```go
type BirdNET struct {
    AnalysisInterpreter *tflite.Interpreter  // Interpreter for the main bird identification model
    RangeInterpreter    *tflite.Interpreter  // Interpreter for the range filter model
    Settings            *conf.Settings       // Application configuration settings
    ModelInfo           ModelInfo            // Information about the current model
    TaxonomyMap         TaxonomyMap          // Mapping of species codes to names and vice versa
    TaxonomyPath        string               // Path to custom taxonomy file, if used
    mu                  sync.Mutex           // Mutex for thread safety
}
```

### Analysis Functionality

The package provides methods for analyzing audio samples and producing detection results:

- `Predict()` - Performs inference on audio samples to detect bird species
- `ProcessChunk()` - Processes a chunk of audio data, returning structured observation notes
- `EnrichResultWithTaxonomy()` - Adds taxonomy information to detection results

### Model Registry

The package includes a model registry system to support different BirdNET models:

```go
// ModelInfo represents metadata about a BirdNET model
type ModelInfo struct {
    ID               string   // Unique identifier for the model
    Name             string   // User-friendly name
    Description      string   // Description of the model
    SupportedLocales []string // List of supported locale codes
    DefaultLocale    string   // Default locale if none is specified
    NumSpecies       int      // Number of species in the model
    CustomPath       string   // Path to custom model file, if any
}
```

Key functions:

- `DetermineModelInfo()` - Identifies model type from filepath or model identifier
- `IsLocaleSupported()` - Validates if a locale is supported by the model

### Label Files

The package exclusively uses the V2.4 format label files, which contain species names in the format "ScientificName_CommonName" and are available in multiple languages.

The language-specific files follow the naming pattern `BirdNET_GLOBAL_6K_V2.4_Labels_<locale>.txt`, where `<locale>` is the locale identifier (e.g., `en_uk`, `de`, `fr`).

The package handles two locale formats:

- Hyphenated format in the API (e.g., `en-uk`, `pt-br`): Used in settings and code
- Underscore format in filenames (e.g., `en_uk`, `pt_BR`): Used in label filenames

The system automatically converts between these formats, allowing users to specify locales with hyphens while the files use underscores. For locales with region codes (like `pt-br`), the region part is also capitalized in the filename format (e.g., `pt_BR`).

Label files are embedded directly in the binary using Go's `embed` package, eliminating the need for external files during deployment.

Key functions:

- `GetLabelFileData()` - Loads the appropriate label file based on model version and locale
- `loadEmbeddedLabels()` - Loads embedded label files based on the model and locale
- `loadExternalLabels()` - Allows loading custom label files from external sources

### Taxonomy Integration

The package provides integrated eBird taxonomy data to enhance species identification:

```go
// TaxonomyMap is a bidirectional mapping between eBird codes and species names
type TaxonomyMap map[string]string
```

Key functions:

- `LoadTaxonomyData()` - Loads taxonomy data from embedded or custom file
- `GetSpeciesCodeFromName()` - Gets eBird code for a species name
- `GetSpeciesNameFromCode()` - Gets species name for an eBird code
- `SplitSpeciesName()` - Splits species name into scientific and common parts
- `EnrichResultWithTaxonomy()` - Adds taxonomy information to detection results

Example usage of taxonomy enrichment:

```go
scientific, common, code := bn.EnrichResultWithTaxonomy(result.Species)
fmt.Printf("Species: %s (%s), eBird code: %s, Confidence: %.2f\n",
    common, scientific, code, result.Confidence)
```

### Range Filtering

The package implements a sophisticated range filtering system that uses location and seasonal data to filter bird species predictions:

- `predictFilter()` - Applies TensorFlow model to predict species based on location and time
- `GetProbableSpecies()` - Filters and sorts bird species based on location-specific scores
- `BuildRangeFilter()` - Updates the range filter with current probable species

The range filtering system uses a specialized TensorFlow Lite model that takes three inputs:

- Latitude (float)
- Longitude (float)
- Week number (float, 1-52)

This helps reduce false positives by filtering out species that are unlikely to be present in a given location during a particular season.

### Results Management

The package includes a queuing system for handling results from audio analysis:

- `Results` struct - Contains detection results and related metadata
- `ResultsQueue` - Channel for sending analysis results to consuming components

## Embedded Resources

The package embeds several key resources:

- TensorFlow Lite models for bird species identification:
  - `BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite` - Primary model for species identification
  - `BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite` - Legacy range filter model
  - `BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite` - Updated range filter model

- Taxonomy and label data:
  - `eBird_taxonomy_codes_2021E.json` - eBird taxonomy mapping between species codes and names
  - `data/labels/V2.4/*.txt` - V2.4 label files in multiple languages

## Usage

A typical usage pattern involves:

```go
// Create new BirdNET instance
bn, err := birdnet.NewBirdNET(settings)
if err != nil {
    // Handle error
}

// Process audio chunk
results, err := bn.Predict(audioSample)
if err != nil {
    // Handle error
}

// Process results with taxonomy information
for _, result := range results {
    scientific, common, code := bn.EnrichResultWithTaxonomy(result.Species)
    fmt.Printf("Species: %s (%s), eBird code: %s, Confidence: %.2f\n",
        common, scientific, code, result.Confidence)
}
```

## Thread Safety

The package implements thread safety mechanisms to allow usage in concurrent contexts:

- A mutex protects the TensorFlow interpreters from concurrent access
- The results queue provides a thread-safe way to communicate results between components

## Performance Optimizations

The package includes several optimizations for performance:

- Automatic thread count determination based on available CPU cores
- Optional XNNPACK delegate support for accelerated inference
- Performance core optimization on supported hardware
- Efficient queue system to handle analysis results asynchronously
- Batch inference support for improved throughput

## Batch Inference

The package supports batch inference for improved throughput when processing multiple audio sources or using high overlap values.

### Automatic Batch Sizing

Batch inference is automatically enabled based on the overlap setting. No manual configuration is required.

| Overlap | Batch Size   | Rationale                                             |
|---------|--------------|-------------------------------------------------------|
| < 2.0   | 1 (disabled) | Chunks arrive slowly (>1s apart), no batching benefit |
| 2.0-2.5 | 4            | Moderate chunk rate (~0.5-1s apart)                   |
| >= 2.5  | 8            | High chunk rate (~0.5s apart), maximize throughput    |

### How It Works

1. Audio chunks are collected by the `BatchScheduler`
2. When the calculated batch size is reached, batch inference runs
3. Results are returned to each source asynchronously via channels
4. On shutdown, pending partial batches are discarded (stale realtime data)

### API

```go
// Submit a chunk for batch inference
err := bn.SubmitBatch(birdnet.BatchRequest{
    Sample:     audioSample,        // []float32 of length 144000 (3s at 48kHz)
    SourceID:   "rtsp-camera-1",    // Identifier for tracking
    ResultChan: make(chan birdnet.BatchResponse, 1),
})

// Receive results asynchronously
resp := <-resultChan
if resp.HasError() {
    // Handle error
}
for _, result := range resp.Results {
    fmt.Printf("%s: %.2f\n", result.Species, result.Confidence)
}
```

### Performance

Batch inference can provide 1.5-2x throughput improvement compared to sequential processing by:

- Reducing TensorFlow Lite interpreter context switching
- Better CPU cache utilization
- Amortizing model invocation overhead across multiple samples

Run benchmarks to measure performance on your hardware:

```bash
go test ./internal/birdnet/... -bench=. -benchtime=10s
```

## Cross-Platform Support

The package is designed to work on multiple platforms:

- Linux (including Raspberry Pi and other SBCs)
- macOS
- Windows

## Dependencies

The package depends on:

- `github.com/tphakala/go-tflite` - TensorFlow Lite bindings for Go
- `github.com/tphakala/go-tflite/delegates/xnnpack` - XNNPACK acceleration for TensorFlow Lite
- Internal packages including `conf`, `datastore`, `observation`, and `cpuspec`
