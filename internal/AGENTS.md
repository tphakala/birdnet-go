# Go Coding Standards

Applies to all Go code in the repository (`internal/`, `cmd/`, `main.go`, and the
ruleguard rules in `rules/`). Read
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
- **Zero linter tolerance**: `task lint` must report no issues

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

Path checks are lexical: `filepath.Clean()` and `filepath.IsLocal()` look at the
string only and never resolve symlinks, so a path that passes them can still
escape its base directory through a symlink when you open it. To actually open
or write files from untrusted input, go through `os.Root` or the project's
SecureFS (`internal/securefs`, exposed to API handlers as `c.SFS`), which
contain symlinks as well as `..`.

Use the lexical checks to reject bad input early:

- `filepath.IsLocal()` rejects absolute paths, empty paths and paths that
  escape upward (`../x`). On Windows it also rejects reserved names such as
  `COM1`/`NUL` (CVE-2023-45283, CVE-2023-45284); on Linux those are ordinary
  names. It cleans before judging, so `"a/../x"` is accepted (it stays inside
  the base).
- When a value must be rejected if it contains `..` at all (for example a URL
  path, or a function that must also accept absolute paths, which `IsLocal`
  rejects), match `..` as a whole path segment, not as a substring, so a name
  like `foo..bar.wav` is still allowed. Treat both `/` and `\` as separators.

```go
cleanPath := filepath.Clean(userInput)
if !filepath.IsLocal(cleanPath) {
    return errors.Newf("invalid path").Category(errors.CategoryValidation).Build()
}

isSep := func(r rune) bool { return r == '/' || r == '\\' }
if slices.Contains(strings.FieldsFunc(urlPath, isSep), "..") {
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
- Propagate `context.Context` down call chains as the first parameter; do not
  store a request context in a struct (no linter enforces this; long-lived
  services that own their lifecycle, such as `apicore.Core`, are the exception)
- Avoid circular dependencies and side effects in `init()`
- Settings must hot-reload: read the current settings at the point of use, do
  not capture them once at startup. `conf.GetSettings()` and
  `conf.CurrentOrFallback(fallback)` are lock-free atomic loads, so reading them
  per call is cheap even on hot paths; do not cache them or wrap them in a lock.
- Batch operations log a failing item and continue with the rest, then report
  the aggregate, instead of aborting on the first error

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
- Wait on channels with `testutil.WaitForChannel` (`internal/testutil`), which
  also provides test containers under `internal/testutil/containers`
- Table-driven tests with `t.Run()`; `t.Helper()` in every helper (`thelper`)
- `testing/synctest` instead of `time.Sleep()` for timing and concurrency
  tests. The API is `synctest.Test(t, func(t *testing.T) { ... })` plus
  `synctest.Wait()`; there is no `synctest.Run` in Go 1.27.
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

Prefer a package-wide check in `TestMain`, with only narrowly scoped ignores for
known process-lifetime goroutines (see `internal/imports/zz_goleak_test.go`):

```go
func TestMain(m *testing.M) {
    goleak.VerifyTestMain(m,
        goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
    )
}
```

For a per-test check, follow `verifyNoLeaks` in
`internal/api/v2/leakcheck_test.go`: call it at the START of the test (not with
`defer`). It snapshots existing goroutines with `goleak.IgnoreCurrent()` and runs
`goleak.VerifyNone` from `t.Cleanup`. Register it before the service's own
`t.Cleanup(svc.Stop)` so it runs after the stop (cleanups run last in, first out).
A `defer goleak.VerifyNone(t)` runs before any `t.Cleanup`, so it reports
services that are about to be stopped. Never combine a per-test leak check with
`t.Parallel()`.

Do not add blanket ignores such as `runtime.gopark` (it would hide parked
leaks) or `testing.(*T).Run` (goleak already filters test-runner stacks).

Always stop services you start, use local instances rather than global
singletons, and allow generous (500ms+) timeouts for async assertions so CI
does not flake.

### Mocks

Never hand-write new mocks; generate them with mockery. `.mockery.yaml` lists
the mocked packages (currently `internal/datastore`,
`internal/datastore/v2/repository`, `internal/notification`, `internal/events`),
each with a generated `mocks/` directory; never edit those files by hand. To mock
a new interface, add it to `.mockery.yaml`, then regenerate:

```bash
go generate ./internal/datastore      # runs mockery over the whole .mockery.yaml
```

```go
mockDS := mocks.NewMockInterface(t)
mockDS.EXPECT().Save(mock.Anything, mock.Anything).Return(nil).Once()
```

Use `.Maybe()` only for incidental calls that may or may not happen. When the
call IS the behaviour under test, including one made from a goroutine, keep the
expectation strict and wait for it (signal a channel from `.Run(...)` and wait
with `testutil.WaitForChannel`, or `assert.Eventually`), then stop the goroutine
before the test returns. A `.Maybe()` there lets the test pass when the behaviour
is gone. Full guide: `internal/datastore/mocks/README.md`.

## Linting

Config: `.golangci.yaml` (golangci-lint v2 format).

- Always lint the **whole module** (`task lint`, or `golangci-lint run -v` once
  the prerequisites in the root `AGENTS.md` are in place), never single files or
  packages; partial runs miss cross-package issues. The run type-checks the
  module, so it doubles as compilation validation, but only for the build tags
  and OS it runs with. Nothing in the preflight gate covers other tags or
  platforms: when you change tagged or OS-specific files, lint with those tags
  and build for that platform yourself (root `AGENTS.md`, "Mandatory: Pre-Push
  Quality Gate").
- A `//nolint` directive needs a specific linter name and a justification
  comment.
- `rules/*.go` holds the project's custom
  [go-ruleguard](https://github.com/quasilyte/go-ruleguard) rules (DSL files
  behind the `ruleguard` build tag). golangci-lint loads them through gocritic's
  `ruleguard` setting, so their findings are reported under `gocritic` and are
  suppressed with `//nolint:gocritic`.

Enabled linters most likely to fire, and the usual fix:

| Linter                | Fix                                                                                                                         |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| errorlint             | `errors.Is()` / `errors.AsType()`, never `==` on errors                                                                     |
| errname               | Sentinel errors are named `ErrXxx`                                                                                          |
| nilerr / nilnil       | Do not return a nil error with a failure, or `nil, nil`                                                                     |
| bodyclose             | `defer resp.Body.Close()` after checking the error                                                                          |
| gocognit / gocyclo    | Split functions (gocognit threshold is 50)                                                                                  |
| dupl / goconst        | Extract duplicated code; reuse an existing constant                                                                         |
| exhaustive            | Handle every enum case; a `default` case counts as exhaustive                                                               |
| prealloc              | `make([]T, 0, n)` when the size is known                                                                                    |
| testifylint / thelper | testify idioms; `t.Helper()` in helpers                                                                                     |
| fatcontext / iface    | No contexts nested in loops/closures; no interface pollution                                                                |
| gocritic              | Its `performance` tag is on: pass large structs by pointer (`hugeParam`), range by index over large values (`rangeValCopy`) |
| modernize             | Use the modern idiom it suggests                                                                                            |
| forbidigo             | Typed logger helpers for sensitive fields                                                                                   |

Also enabled: staticcheck, revive, ineffassign, wastedassign,
unconvert, misspell, predeclared, copyloopvar, durationcheck. `gosec` is
configured but currently disabled. gocritic's `commentFormatting` and
`commentedOutCode` checks are disabled.

## Pre-Commit Checklist

- [ ] `task lint` on the whole module: zero issues
- [ ] `task test` (or the affected packages while iterating, then the
      full run)
- [ ] No `//nolint` without a justification
- [ ] Every exported symbol documented; every error handled
