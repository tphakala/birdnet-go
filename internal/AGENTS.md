# Go Coding Standards

Applies to all Go code in the repository (`internal/`, `cmd/`, `pkg/`). Read
`TESTING.md` before writing tests and `internal/errors/AGENTS.md` before adding
error handling.

## Quick Reference

- Go version: **1.27** (`go.mod`). Use modern language and stdlib features.
- Errors: `github.com/tphakala/birdnet-go/internal/errors`, not the standard
  `errors` package (details in `internal/errors/AGENTS.md`)
- Logging: structured logging via `internal/logger`
- Tests: testify only, always run with `-race`
- No magic numbers or strings: use named constants
- Document every exported symbol (`// TypeName does ...`)
- **Zero linter tolerance**: `golangci-lint run -v` must report no issues

## Modern Go Idioms

Prefer the current idiom; the `modernize` linter flags many of the old forms.

- `any`, not `interface{}`; `for i := range n` for counted loops
- `new(expr)` (Go 1.26) instead of a generic `ptr()` helper:
  `FirstSeen: new(time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC))`
- `strings.Cut` instead of `strings.Index` plus slicing
- `errors.AsType[*fs.PathError](err)` (Go 1.26) instead of `errors.As` with a
  pre-declared target. `internal/errors` has no `AsType` passthrough, so files
  that need it import the standard package (alias it `stderrors` when the file
  also imports `internal/errors`)
- `sync.WaitGroup.Go(func() { ... })` instead of `Add(1)` plus `defer Done()`
- `for field := range t.Fields()` (Go 1.26 `reflect` iterators) instead of
  indexing with `NumField()`
- `os.Root` for filesystem sandboxing of untrusted paths
- Pre-compile regular expressions at package level
- Preallocate slices when the final size is known:
  `make([]T, 0, len(items))`

## Standard Library First

Never hand-parse these; use the stdlib:

- URLs: `url.Parse()`
- IPs: `net.ParseIP()`, `ip.IsPrivate()`
- Host and port: `net.JoinHostPort(host, strconv.Itoa(port))`. `fmt.Sprintf("%s:%d")`
  breaks IPv6 addresses.
- Paths: `filepath.Join()`, `filepath.Clean()`

### Path validation for untrusted input

`filepath.IsLocal()` cleans its input first, so `"path/../etc"` becomes `"etc"`
and passes. Use it to validate a final, already-cleaned filesystem path (it
also rejects Windows reserved names such as `COM1`/`NUL`, CVE-2023-45283 and
CVE-2023-45284). For untrusted URL paths, ALSO reject a literal `..` explicitly:

```go
cleanPath := filepath.Clean(userInput)
if !filepath.IsLocal(cleanPath) {
    return errors.Newf("invalid path").Category(errors.CategoryValidation).Build()
}

// URL path from a request: IsLocal alone is not enough
if strings.Contains(urlPath, "..") {
    return errors.Newf("path traversal attempt").Category(errors.CategoryValidation).Build()
}
```

## Design Patterns

- Accept interfaces, return concrete types; define minimal interfaces next to
  their consumer
- Inject dependencies (datastore, HTTP clients, filesystem, clock, settings)
  through constructors: `NewService(deps...) *Service`. Avoid global state and
  singletons. If you find code that cannot be tested because it instantiates
  its dependencies directly, flag it.
- Safe type assertions: `if v, ok := x.(Type); ok { ... }`
- Copy data out under a read lock (`sync.RWMutex`) rather than holding the lock
  while working
- Propagate `context.Context` down call chains; never store it in a struct
  (`fatcontext`)
- Avoid circular dependencies and side effects in `init()`
- Settings must hot-reload: read the current settings at the point of use, do
  not capture them once at startup

## Security

- Validate all user input at the boundary (see path validation above)
- Parameterized queries only; never build SQL from strings
- Validate UUIDs and IDs properly before use
- Never log credentials, tokens, or personal data; use the typed logger helpers
  (`logger.Password()`, `logger.Token()`, ...). `forbidigo` rejects
  `logger.String()` for sensitive field names.

## Testing

Full patterns and shared helpers are in `TESTING.md`. The rules that matter
most:

- testify `assert`/`require` for all assertions (`testifylint` enforces idioms,
  for example `assert.Equal` rather than `assert.True(a == b)`)
- Table-driven tests with `t.Run()`; `t.Helper()` in every helper (`thelper`)
- `testing/synctest` instead of `time.Sleep()` for timing and concurrency tests
- `t.TempDir()` for scratch space; `t.ArtifactDir()` (Go 1.26) for outputs worth
  keeping with `-artifacts`. Do not use `os.MkdirTemp()` in tests.
- `t.Cleanup()` instead of `defer` for restoring global state
- `t.Parallel()` only for truly independent tests. Never parallelize tests that
  mutate global state (for example `conftest.SetTestSettings()`) or share
  mutable data; clone shared maps per subtest with `maps.Clone`.
- Test-only helpers go in `*_test.go` files. Helpers shared across packages go
  in a dedicated test-support package (for example `internal/conf/conftest`,
  `internal/api/v2/apitest`) that production code never imports.
- Benchmarks: `b.ReportAllocs()` and `b.Loop()`

### Goroutine leak detection

Tests that start services or goroutines should verify nothing leaks:

```go
defer goleak.VerifyNone(t,
    goleak.IgnoreTopFunction("testing.(*T).Run"),
    goleak.IgnoreTopFunction("runtime.gopark"),
    goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
)
```

Always stop services you start, use local instances rather than global
singletons, and allow generous (500ms+) timeouts for async assertions so CI
does not flake.

### Mocks

Never hand-write mocks; generate them with mockery (`.mockery.yaml`). Never edit
files under `internal/datastore/mocks/` by hand.

```bash
go generate ./internal/datastore      # after changing internal/datastore/interfaces.go
```

```go
mockDS := mocks.NewMockInterface(t)
mockDS.EXPECT().Save(mock.Anything, mock.Anything).Return(nil).Once()
```

Use `.Maybe()` for calls that happen conditionally or from goroutines, so the
test does not fail when the call is legitimately skipped. Full guide:
`internal/datastore/mocks/README.md`.

## Linting

Config: `.golangci.yaml` (golangci-lint v2 format).

- Always lint the **whole module** (`golangci-lint run -v`), never single files
  or packages; partial runs miss cross-package issues. The run type-checks the
  whole module, so it doubles as compilation validation for the default build
  tags. Cross-platform and build-tag coverage is part of the preflight gate.
- A `//nolint` directive needs a specific linter name and a justification
  comment.

Enabled linters most likely to fire, and the usual fix:

| Linter                | Fix                                                           |
| --------------------- | ------------------------------------------------------------- |
| errorlint             | `errors.Is()` / `errors.As()`, never `==` on errors           |
| errname               | Sentinel errors are named `ErrXxx`                            |
| nilerr / nilnil       | Do not return a nil error with a failure, or `nil, nil`       |
| bodyclose             | `defer resp.Body.Close()` after checking the error            |
| gocognit / gocyclo    | Split functions (gocognit threshold is 50)                    |
| dupl / goconst        | Extract duplicated code; reuse an existing constant           |
| exhaustive            | Handle every enum case; a `default` case counts as exhaustive |
| prealloc              | `make([]T, 0, n)` when the size is known                      |
| testifylint / thelper | testify idioms; `t.Helper()` in helpers                       |
| fatcontext / iface    | No context in structs; no interface pollution                 |
| modernize             | Use the modern idiom it suggests                              |
| forbidigo             | Typed logger helpers for sensitive fields                     |

Also enabled: gocritic, staticcheck, revive, ineffassign, wastedassign,
unconvert, misspell, predeclared, copyloopvar, durationcheck. `gosec` is
configured but currently disabled. gocritic's `commentFormatting` and
`commentedOutCode` checks are disabled.

## Pre-Commit Checklist

- [ ] `golangci-lint run -v` on the whole module: zero issues
- [ ] `go test -race ./...` (or the affected packages while iterating, then the
      full run)
- [ ] No `//nolint` without a justification
- [ ] Every exported symbol documented; every error handled
