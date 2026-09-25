# internal/errors Package Guidelines

`internal/errors` is a drop-in replacement for the standard `errors` package
that adds component and category metadata, privacy scrubbing, and meaningful
Sentry titles. Full documentation: `internal/errors/README.md`.

## Import Only This Package

```go
import "github.com/tphakala/birdnet-go/internal/errors"   // correct
import "errors"                                            // do not
```

It provides passthroughs for the standard functions `errors.Is`, `errors.As`,
`errors.AsType`, `errors.Unwrap`, `errors.Join` and `errors.NewStd` (the
standard `errors.New`), and for the standard sentinel `errors.ErrUnsupported`.
There are no exceptions: this package imports nothing else from the module, so
it cannot cause an import cycle, and the `depguard` linter rejects the standard
package everywhere except `errors.go` here. If you need a standard function that is missing, add a
passthrough here rather than importing the standard package.

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
- **Performance**: building an enhanced error is never free. With telemetry
  disabled (the default) it costs on the order of 100 ns and a few allocations
  on a desktop CPU, roughly three times that on a Raspberry Pi 5. With telemetry
  enabled, an error built without `.Component()` also walks the call stack to
  detect the component, which costs microseconds; another reason to always set
  `.Component()`. So on per-sample or per-buffer hot paths, return a
  pre-declared sentinel and build the enhanced error once at the component
  boundary. Measure with
  `go test -run='^$' -bench='ErrorCreation(NoTelemetry|WithContext)$' -benchmem -count=6 ./internal/errors/`
  (the `WithTelemetry` benchmark measures a synchronous fallback path, not
  production reporting).
