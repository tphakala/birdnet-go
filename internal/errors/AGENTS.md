# internal/errors Package Guidelines

`internal/errors` is a drop-in replacement for the standard `errors` package
that adds component and category metadata, privacy scrubbing, and meaningful
Sentry titles. Full documentation: `internal/errors/README.md`.

## Import Only This Package

```go
import "github.com/tphakala/birdnet-go/internal/errors"   // correct
import "errors"                                            // do not
```

It provides passthroughs for the standard functions: `errors.Is`, `errors.As`,
`errors.Unwrap`, `errors.Join`, and `errors.NewStd` (a plain `stderrors.New`).
Import the standard package only to break an import cycle or to use
`errors.AsType`, which has no passthrough; alias it `stderrors` if the file also
imports this package.

## Creating Errors

```go
// Sentinel errors: plain errors, named ErrXxx
var ErrNotFound = errors.NewStd("not found")

// Wrap an existing error with telemetry metadata
err := errors.New(originalErr).
    Component("datastore").
    Category(errors.CategoryDatabase).
    Context("operation", "save_detection").
    Build()

// Descriptive new error (preferred when there is no underlying error)
err := errors.Newf("Wikipedia API response missing 'query.pages'").
    Component("imageprovider").
    Category(errors.CategoryImageFetch).
    Context("operation", "parse_pages_from_response").
    Build()
```

Builder methods: `Component`, `Category`, `Priority`, `Context`, `ModelContext`,
`FileContext`, `NetworkContext`, `Timing`, `Build`. Convenience constructors
such as `ValidationError` and `NetworkError` exist for common cases.

`fmt.Errorf("...: %w", err)` is fine for adding local context inside a
component. Use the builder where an error leaves a component or is likely to
reach telemetry, so it carries the right component and category.

## Rules

- **Always set `.Component()` and `.Category()` explicitly.** Automatic
  component detection relies on the registry and can misattribute errors.
- **Always add `Context("operation", "<specific_action>")`.** Without it Sentry
  groups unrelated failures under generic titles.
- **Never put secrets or personal data in `Context` values.** Scrubbing is a
  safety net, not a licence.
- **Register new components.** A new package that reports errors must be
  added to the `RegisterComponent(...)` list in the `init()` in `errors.go`.
  Unregistered components get incorrect telemetry tags. Check that list for
  the current set rather than trusting a copy here.

## Categories

Pick the most specific category from the `Category*` constants in `errors.go`,
which is the authoritative list; the groups below are a map, not a copy to trust
over the code:

- Model and startup: `CategoryModelInit`, `CategoryModelLoad`, `CategoryLabelLoad`,
  `CategoryConfiguration`, `CategoryPolicyConfig`
- Data and files: `CategoryDatabase`, `CategoryFileIO`, `CategoryFileParsing`,
  `CategoryDiskUsage`, `CategoryDiskCleanup`
- Network and integrations: `CategoryNetwork`, `CategoryHTTP`, `CategoryRTSP`,
  `CategoryMQTTConnection`, `CategoryMQTTPublish`, `CategoryMQTTAuth`,
  `CategoryIntegration`, `CategoryImageFetch`, `CategoryImageCache`,
  `CategoryImageProvider`
- Audio and analysis: `CategoryAudio`, `CategoryAudioSource`,
  `CategoryAudioAnalysis`, `CategorySoundLevel`, `CategoryBuffer`,
  `CategoryThreshold`, `CategorySpeciesTracking`, `CategoryEventTracking`
- Control flow and runtime: `CategoryValidation`, `CategoryNotFound`,
  `CategoryConflict`, `CategoryState`, `CategoryLimit`, `CategoryResource`,
  `CategoryTimeout`, `CategoryCancellation`, `CategoryRetry`, `CategoryWorker`,
  `CategoryJobQueue`, `CategoryProcessing`, `CategoryCommandExecution`,
  `CategoryBroadcast`, `CategorySystem`, `CategoryGeneric`

## Troubleshooting

- **Wrong component in telemetry**: confirm the package is registered and that
  `.Component()` is set explicitly.
- **Generic Sentry titles**: add an `operation` context and use a descriptive
  `errors.Newf()` message. See "Troubleshooting" in `README.md`.
- **Performance**: when telemetry is disabled, `Build()` skips component
  detection and publishing entirely; when enabled, reporting is asynchronous
  through the event bus. The builder itself still allocates (2 allocations and about
  100 ns for a bare error, 4 allocations and a few hundred ns with two `Context`
  calls, measured on a desktop CPU), so on per-sample or per-buffer hot
  paths return a pre-declared sentinel and build the enhanced error once at the
  component boundary. Measure with
  `go test -run='^$' -bench=ErrorCreation -benchmem ./internal/errors/`.
