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

Reuse the project's helpers instead of writing a new check, and pick the one for
the job:

- **Files** named by untrusted input: go through SecureFS (`internal/securefs`,
  `c.SFS` in API handlers). Its relative-path methods (`StatRel`,
  `ReadDirRel`, `ServeRelativeFile`) give containment: they resolve inside an
  `os.Root`, so neither `..` nor a symlink can escape. `ValidateRelativePath`
  is only a lexical check (it cleans the path and rejects absolute and upward
  paths) and does not stop a symlink, so pass its result to those methods and
  never join it onto a directory for an `os.*` call. Some existing handlers in
  `internal/api/v2/media` still do that join; do not copy them. SecureFS does
  not reject odd-looking names, so do not treat it as a string validator.
- **Clip paths** from API requests: `apicore.NormalizeClipPathStrict`.
- **Redirect targets** (which may carry a query string):
  `security.IsValidRedirect`.
- **Other internal URL paths** (a bare path starting with `/`):
  `security.IsSafePath`, which rejects traversal and other unsafe forms,
  including encoded ones (its doc comment lists exactly what it checks).

For a new check, follow the ruleguard rule in `rules/net.go`: use
`filepath.IsLocal` for file paths, and keep a `strings.Contains(p, "..")`
substring check only for URL paths and for file paths that may legitimately be
absolute, which `IsLocal` rejects (see the `..` check in `validateExportPath`,
`internal/conf/validate_audio.go`). The rule reports every such substring
check, so each one needs a `//nolint:gocritic` comment giving the reason (see
`internal/api/v2/apicore/clip_path.go`).

`filepath.Clean()` and `filepath.IsLocal()` are lexical: they never resolve
symlinks. Pass the raw input to `IsLocal` (after any URL decoding) rather than
cleaning it first: `filepath.Clean("")` is `"."`, which `IsLocal` accepts,
while `IsLocal("")` is false.

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
  `conf.CurrentOrFallback(fallback)` are lock-free atomic loads, cheap even on
  hot paths, so never wrap them in a lock or keep the result across
  operations. Load once per operation (`s := conf.CurrentOrFallback(...)`) and
  read related fields from that one snapshot, so a reload in between cannot mix
  two versions. Treat the snapshot as read-only; `GetSettings()` can return nil
  before settings are loaded; `CurrentOrFallback` then returns the fallback you
  pass, so pass a non-nil one.
- Batches of independent items (per-file imports, per-species lookups) log a
  failing item and continue, then report the aggregate. All-or-nothing work
  (a database transaction, a migration, a config write, a backup restore)
  aborts and rolls back on the first error instead.

## Security

- Validate all user input at the boundary (see path validation above)
- Parameterized queries only; never build SQL from strings
- Validate UUIDs and IDs properly before use
- Never log credentials, tokens, or personal data; use the typed logger helpers
  (`logger.Password()`, `logger.Token()`, ...). `forbidigo` rejects
  `logger.String()` for sensitive field names.

## Testing

`TESTING.md` is the full reference for Go tests: testify usage, table-driven
tests, `testing/synctest`, goroutine-leak checks, mocks (mockery and `.Maybe()`),
cleanup and parallelism. Read it before writing or changing a test. The rules
most often missed:

- testify `assert`/`require` for all assertions (`testifylint` enforces idioms)
- `testing/synctest` (`synctest.Test`) instead of `time.Sleep()` for timing and
  concurrency; there is no `synctest.Run` in Go 1.27
- Isolation, temporary directories and `t.Parallel()` rules are in
  `TESTING.md` ("Isolation and Parallelism"); the one most often broken: never
  `t.Parallel()` a test that mutates global state such as
  `conftest.SetTestSettings()`
- Generated mocks only (`.mockery.yaml` plus `go generate ./internal/datastore`);
  `.Maybe()` only for incidental calls, never for the behaviour under test
- Test-only helpers go in `*_test.go` files; helpers shared across packages go in
  a test-support package (`internal/conf/conftest`, `internal/api/v2/apitest`,
  `internal/testutil`) that production code never imports

## Linting

Config: `.golangci.yaml` (golangci-lint v2 format).

- Always lint the **whole module** (`task lint`; see the root `AGENTS.md` for
  running a step by hand), never single files or packages; partial runs miss
  cross-package issues. The run type-checks the module, so it doubles as
  compilation validation, but only for the build tags and OS it runs with. No
  automated step in the preflight gate covers other tags or platforms; its
  certification checklist asks you to handle them, as described in the root
  `AGENTS.md` ("Mandatory: Pre-Push Quality Gate").
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
