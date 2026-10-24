# Classifier Package

The `classifier` package loads the acoustic models, runs inference on audio windows, filters species by location and season, and keeps the species identity data that the rest of the application reads. Several models can be loaded at once. The `Orchestrator` is the entry point, and nothing outside the package should talk to a model implementation directly.

## Main pieces

| Piece                     | Files                                                                           | Role                                                                                                                                       |
| ------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `Orchestrator`            | `orchestrator*.go`                                                              | Owns the loaded models, runs inference, owns the range filter, the species index and the taxonomy. Created by `NewOrchestrator(settings)`. |
| `ModelManager`            | `model_manager*.go`                                                             | Installs, replaces and uninstalls downloadable models, and hot-loads them into the orchestrator.                                           |
| Model registry            | `model_registry.go`                                                             | `ModelRegistry`: the single source of truth for which models exist and their audio requirements.                                           |
| Model catalog             | `model_catalog*.go`, `catalog_loader.go`                                        | Downloadable models and their hardware and regional variants, as shown in the model gallery.                                               |
| Range filter              | `range_filter*.go`, `mapped_range_filter.go`                                    | Location and week based species filtering.                                                                                                 |
| Labels and names          | `label_files.go`, `label_vocabulary.go`, `names.go`, `taxonomy*.go`, `genus.go` | Label loading and species name and taxonomy lookup.                                                                                        |
| Results queue and overlap | `queue.go`, `overlap.go`, `cadence/`                                            | Hand-off of results and the analysis window cadence.                                                                                       |
| Bat scheduler             | `nighttime_scheduler.go`                                                        | Runs the bat model only at night.                                                                                                          |

## Models

`ModelRegistry` in `model_registry.go` defines these models (registry ID, backend, input):

| Registry ID    | Name            | Backend                                                              | Input                                                         |
| -------------- | --------------- | -------------------------------------------------------------------- | ------------------------------------------------------------- |
| `BirdNET_V2.4` | BirdNET v2.4    | TFLite (ONNX INT8-ARM variant on arm64 and on builds without TFLite) | 48 kHz, 3 s                                                   |
| `BirdNET_V3.0` | BirdNET v3.0    | ONNX                                                                 | 32 kHz, 5 s                                                   |
| `Perch_V2`     | Google Perch v2 | ONNX                                                                 | 32 kHz, 5 s                                                   |
| `Bat`          | Bat classifier  | ONNX classifier head on BirdNET v2.4 embeddings                      | 3 s windows of raw ultrasonic audio (at least 96 kHz capture) |
| `BSG`          | BSG Finland     | ONNX                                                                 | 48 kHz, 3 s                                                   |

BirdNET v2.4 is the stock default and ships embedded in the binary (builds with the `noembed` tag expect external files). The other models are downloaded through the gallery. `models.enabled` in the configuration selects which models load. BSG is registered and has a catalog entry, but it is hidden in the gallery and has no loader in `modelLoaders` yet, so it cannot be loaded.

Every loaded model implements `ModelInstance` (`model.go`): `Predict`, `Spec`, `ModelID`, `Labels`, `RuntimeInfo` (device, live backend and precision) and `Close`. `BirdNET` (`birdnet.go`) is the TFLite and ONNX v2.4 implementation; `BirdNETV3`, `Perch` and `Bat` are the others. `ModelSpec` carries the sample rate and clip length, and `BufferDimensions` and `BufferInterval` derive buffer sizes from it.

## Orchestrator

- `NewOrchestrator(settings)` builds the taxonomy service, the OpenFauna name resolver, the species index and the range filter service, then loads every model in `models.enabled`. A model that fails to load is recorded (`LoadFailures`, `LoadErrors`) and does not stop startup; `AcousticModelsState` reports the degraded state.
- `PredictModel` and `PredictModelTimed` run one model by registry ID. Inference is serialized across models; each model also has its own lock, so reloading one model does not block lookups on another. The lock order is documented on the `Orchestrator` type and must be followed when adding code.
- `LoadModel`, `UnloadModel`, `ReconcileEnabledModels` and `ReloadSecondaryModels` change the loaded set at runtime. Reloads build the new instance first and swap it in afterwards.
- `DefaultTargets` lists the models a source analyzes when it names none: every loaded model that is not schedule gated, v2.4 first.
- Inference health is tracked per model (`orchestrator_inference_health.go`), and a model that fails every window raises a persistent notification.
- Diagnostic runtime snapshots (`orchestrator_runtime_snapshot.go`) describe what each loaded model was built as without waiting on inference.

## Inference backends

The runtimes live in `internal/inference` and are selected through its interfaces (`Classifier`, `EmbeddingExtractor`, `CustomClassifier`, range filters):

- `tflite`: TensorFlow Lite, used by the stock BirdNET v2.4 model and its embedded range filter.
- `onnx`: ONNX Runtime, used by BirdNET v3.0, Perch v2, the bat classifier and the ONNX range filters.
- `openvino`: an execution provider for ONNX models, chosen with the `birdnet.backend` setting. It appears as the live backend in `RuntimeInfo`, never as a model file type.
- `vad`: a Silero voice activity detector used by the speech privacy filter in `internal/analysis/processor`, not by the classifier.

## Model manager and catalog

`ModelManager` scans the models directory (`ScanInstalled`), installs and replaces models (`Install`, `Reinstall`, `InstallOrReplace`) and uninstalls them (`Uninstall`). Downloads come from Hugging Face with SHA-256 verification and endpoint failover, progress is reported through `DownloadState`, and a failed variant swap is rolled back. After an install it writes the model paths into the configuration and hot-loads the model through the orchestrator.

The catalog is a list of `CatalogEntry` values (`model_catalog.go`), each pointing at a registry model through `RegistryID`. An entry can have `Variants` (for example `fp32`, `fp16`, `int8-arm`, or a regional slice), `DependsOn` entries for shared files, and `Component` entries for support files. The embedded catalog is seeded to `model-catalog.json` in the configuration directory on first run (`LoadCatalog`); a file the user edited is kept. The regional variants of BirdNET v3.0 and Perch v2 are generated into `model_catalog_regional_gen.go` by `go generate ./internal/classifier` from `gen/manifests/`. The `recommend` package ranks variants against the host hardware, and `region` maps station coordinates to a regional slice.

### Adding a model

The model families are wired by hand. The Perch v2 wiring shows what a new single-file ONNX model needs:

1. Add the registry ID constant and a `ModelRegistry` entry in `model_registry.go`: backend, `Spec`, config aliases, species count and `rangeFilterCompat`. Add its filename patterns to `filenamePatterns`.
2. Add the config model IDs in `internal/conf/consts.go` and register them in `ValidAudioModels` (`conf/validate.go`) and the alias migration (`conf/migrations.go`). Add a settings struct for its paths and threshold.
3. Implement `ModelInstance` (see `perch_onnx.go`), write `build<Model>` and `load<Model>` on the orchestrator (see `orchestrator_perch_onnx.go`), and add the loader to `modelLoaders`.
4. If it supports OpenVINO, add a builder to `openvinoCapableSecondaryBuilders` so a backend change reloads it.
5. Add a catalog entry with a `RegistryID`, files and checksums in `model_catalog.go`, and the family's settings fields to `familyFields` in `model_manager.go`.
6. If it has its own confidence threshold, handle it in `modelGlobalConfidenceThreshold` in `internal/analysis/processor/processor.go`.

## Range filter

The range filter (`rangeFilterService`) keeps species that are probable at the station location and week. Its backend is chosen from the configuration by `resolveRangeFilterBackend`:

- the embedded TFLite metadata model for BirdNET v2.4;
- a strict ONNX filter whose output must match the classifier labels one to one;
- the mapped geomodel, an ONNX model with its own label set whose scores are matched onto each classifier's labels by scientific name (`mappedRangeFilter`).

Which models take part is set per registry entry (`rangeFilterCompat`): v2.4 uses its metadata filter, v3.0 and Perch use the geomodel, and Bat and BSG do not participate. The service holds an immutable state snapshot that is rebuilt on model load, unload and reload (`ReloadRangeFilter`). If a configured backend cannot be built, it falls back to the embedded filter or runs without filtering. `GetProbableSpecies`, `GetSpeciesOccurrence` and `RangeFilterStatus` are the read entry points.

## Labels and species identity

- BirdNET v2.4 label files for every supported locale are embedded (`data/labels/V2.4`, named `BirdNET_GLOBAL_6K_V2.4_Labels_<locale>.txt`); `GetLabelFileData` falls back to English when a locale is missing. Labels use the `ScientificName_CommonName` form. The other models read the label file installed with them.
- The orchestrator is the only writer of the process-wide species name index (`internal/speciesindex`). It rebuilds the index from the union of all loaded models' labels on model load, unload, reload, range filter rebuild and locale change. Other packages read `SpeciesIndex().Snapshot()` without locks.
- `internal/openfauna` is the first name resolver in the chain: it maps scientific names to common names in many locales and carries taxonomy metadata. `taxonomy*.go` provides eBird species codes and multilingual names from the geomodel taxonomy.

## Results, overlap and cadence

- `ResultsQueue` carries `Results` from the analysis pipeline to the detection processor. Its default capacity is `DefaultQueueSize` (100) and `ResizeQueue` replaces it. A value sent on the queue belongs to the receiver, so the sender must not modify it.
- `birdnet.overlap` is defined on a 3 second window. `ResolveModelOverlap` scales it to each model's clip length so the overlap fraction stays the same, and fixes the bat model at 50 percent.
- The `cadence` package solves for the effective base overlap that keeps the inference load of all source and model pairs under a duty ceiling when the false positive filter is on. The orchestrator publishes the plan (`SetCadencePlan`) and probes model latency for it; `EffectiveBaseOverlap` returns the overlap the pipeline actually uses. The configured value is never changed.

## Nighttime scheduler

The bat model is schedule gated. `nighttimeScheduler` computes from civil dusk and dawn whether it should be active and refreshes that state every 60 seconds, so the hot path reads one atomic value. It is controlled by `bat.nighttimeonly`, needs a configured location and a `suncalc` instance (`SetSunCalc`), and fails open (the bat model stays active) when either is missing. `IsModelActive` and `ModelScheduleStatus` report the state.

## Testing

Go tests in this package need the TensorFlow Lite library and the build tags that `task test` supplies; run them with `task test` rather than a bare `go test`. Tests that need an ONNX runtime or OpenVINO skip themselves when it is absent.
