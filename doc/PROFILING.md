# BirdNET-Go Profiling Guide

This guide explains how to use the built-in profiling capabilities in BirdNET-Go
to diagnose performance issues and memory usage.

## Prerequisites

- BirdNET-Go running with `diagnostics.profiling.enabled` set to `true`
- `curl`, or any HTTP client, to collect profiles
- The `go` tool, to analyse them (not needed to collect them)

> **Availability.** The `diagnostics.profiling` section and the endpoints on the
> web server are newer than the `20260716` release. On an older build there is no
> such config section, `/debug/pprof/` on the web server port answers `404` no
> matter what you set, and the endpoints are still on the telemetry listener. If
> you set the key, restart, and still get `404`, your build predates the feature.

## Migrating from the old telemetry-port workflow

If you were profiling before this changed, four things moved:

1. **The port.** Profiling is on the web server port (default 8080), not the
   telemetry listener (default 8090). The old location answers `410 Gone` if the
   telemetry listener is still enabled, and refuses the connection if it is not,
   since telemetry is off by default. Both mean the same thing.
2. **The gate.** `debug: true` never enabled the endpoints, despite what this
   guide used to say. Set `diagnostics.profiling.enabled: true` and restart.
3. **Block and mutex sampling.** `debug: true` used to switch both on at their
   most aggressive setting as a side effect. It no longer does, so
   `/debug/pprof/block` and `/debug/pprof/mutex` come back empty until you set
   `blockrate` and `mutexfraction` explicitly. See below for why the old
   behaviour was worth removing.
4. **Credentials.** The endpoints are now behind the web server's authentication,
   or behind a generated token where none is configured.

One thing you can now turn off: if `realtime.telemetry.enabled` was on only to
reach pprof, it no longer serves any profiling purpose. Turning it off closes an
unauthenticated listener, which is the point of the change.

## Enabling profiling

Profiling is off by default. It is not related to `debug: true`, which controls
logging verbosity only.

```yaml
diagnostics:
  profiling:
    enabled: true
```

Edited in `config.yaml` this applies at the next restart, because the config file
is not watched. Changed through the settings API it applies immediately, with no
restart:

```bash
curl -X PATCH http://localhost:8080/api/v2/settings/diagnostics \
  -H 'Content-Type: application/json' \
  -d '{"profiling":{"enabled":true}}'
```

That request needs whatever authentication and CSRF token the settings API
already requires on your instance. Use `PATCH /api/v2/settings/diagnostics`, as
above: there is no section-level `PUT`, and the whole-document
`PUT /api/v2/settings` zeroes every field the request body omits.

There is no switch for any of this in the settings UI; the config file and the
settings API are the only two ways in.

The endpoints are served by **the main web server, on the same port as the web
UI** (default 8080), behind whatever authentication you have configured. They are
not on the Prometheus telemetry listener, which is where they used to live. The
old location on the telemetry port answers `410 Gone` with a pointer to the
config section above.

### Getting the token, on an instance with no authentication

This is the step most people miss, and it is the one that makes every command
below fail with `403` if you skip it.

If no authentication provider is configured, which is the common home-LAN
default and exactly the case the token exists for, there is no session to
authenticate with. A token is generated into `diagnostics.profiling.token` the
first time profiling is switched on, and every request must carry it as a
`token=` query parameter.

**The token cannot be read back through the API.** `GET /api/v2/settings` and
`GET /api/v2/settings/diagnostics` both return it as `**********`, and it is
never written to the log or included in a support dump. That is deliberate, but
it means switching profiling on through the settings API mints a credential that
same API will never show you. The only place to read it is `config.yaml`.

Read it from the config file. The command has to stop at the end of the
`diagnostics` section, because other sections have `token:` keys of their own; a
search that just runs forward from `diagnostics:` will happily print a
notification provider's secret instead:

```bash
awk '/^diagnostics:/{f=1;next} /^[^[:space:]#]/{f=0} f&&/^[[:space:]]*token:/{sub(/^[[:space:]]*token:[[:space:]]*/,"");gsub(/^"|"$/,"");print;exit}' config.yaml
```

For a container installation the config lives inside the config volume, so read
it through the container:

```bash
docker exec birdnet-go awk '/^diagnostics:/{f=1;next} /^[^[:space:]#]/{f=0} f&&/^[[:space:]]*token:/{sub(/^[[:space:]]*token:[[:space:]]*/,"");gsub(/^"|"$/,"");print;exit}' /config/config.yaml
```

The host-side copy of that file is under the directory bind-mounted at `/config`
(`~/birdnet-go-app/config/config.yaml` for a default `install.sh` deployment), so
reading it there works too.

If that prints nothing, or prints an empty string, no token has been minted yet.
Check that `diagnostics.profiling.enabled` is `true`, that the instance has been
restarted since, and that it has no authentication provider configured (with one,
no token is generated because none is needed). If all three hold and there is
still no `token:` line, generation or persistence failed: the startup log carries
the reason, and a config file the process cannot write is the usual cause.

Every example below assumes the token is in a shell variable. This is the same
name the collection scripts in `scripts/` read, so exporting it once serves both:

```bash
export BIRDNET_PROFILING_TOKEN="$(awk '/^diagnostics:/{f=1;next} /^[^[:space:]#]/{f=0} f&&/^[[:space:]]*token:/{sub(/^[[:space:]]*token:[[:space:]]*/,"");gsub(/^"|"$/,"");print;exit}' config.yaml)"
```

`go tool pprof` has no flag for custom headers, but it does preserve query
parameters it did not set and merges its own into them, so the token survives and
the one-command workflow is intact:

```bash
go tool pprof "http://localhost:8080/debug/pprof/heap?token=$BIRDNET_PROFILING_TOKEN"
```

### On an instance that does have authentication configured

No token is generated and none is accepted: the web server's own auth middleware
is the gate, and it runs instead of the token check. Passing `?token=...` there
gets you a `401`, not access.

**`go tool pprof` cannot authenticate against such an instance.** It has no flag
for headers and no cookie jar, and BirdNET-Go's `security.basicauth` is a form
login that mints a session cookie, not HTTP Basic. Credentials embedded in the
URL (`http://user:pass@host/...`) are therefore rejected with a `401`, because
the middleware accepts only a `Bearer` token or a session cookie. Presenting them
makes the request fail harder than sending nothing.

Log in with `curl`, keep the session cookie, fetch the profile to a file, and
analyse the file locally:

```bash
curl -s -c jar.txt http://localhost:8080/ -o /dev/null
CSRF=$(awk '$6=="csrf"{print $7}' jar.txt)
REDIRECT=$(curl -s -b jar.txt -c jar.txt -X POST http://localhost:8080/api/v2/auth/login \
  -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" \
  -d '{"username":"admin","password":"YOUR_PASSWORD"}' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["redirectUrl"])')
curl -s -b jar.txt -c jar.txt -L -o /dev/null "http://localhost:8080$REDIRECT"

curl -s -b jar.txt -o heap.pprof "http://localhost:8080/debug/pprof/heap"
go tool pprof heap.pprof
```

The login response carries an OAuth2 callback URL that has to be followed before
the session cookie is valid, which is what the third command does. Everything
after that is the ordinary two-step fetch-then-analyse workflow, and it applies to
every endpoint in the table below.

## Available profiling endpoints

All of these sit under `/debug/pprof/` on the web server port.

| Path                        | What it returns                                     |
| --------------------------- | --------------------------------------------------- |
| `/debug/pprof/`             | Index page listing the available profiles           |
| `/debug/pprof/heap`         | Heap memory profile (live objects)                  |
| `/debug/pprof/allocs`       | All allocations since process start                 |
| `/debug/pprof/goroutine`    | Current goroutines                                  |
| `/debug/pprof/threadcreate` | OS thread creation profile                          |
| `/debug/pprof/block`        | Blocking profile; needs `blockrate` set             |
| `/debug/pprof/mutex`        | Mutex contention profile; needs `mutexfraction` set |
| `/debug/pprof/profile`      | CPU profile, 30 seconds unless `seconds=N` is given |
| `/debug/pprof/trace`        | Execution trace                                     |
| `/debug/pprof/cmdline`      | The process command line                            |
| `/debug/pprof/symbol`       | Address-to-symbol lookup, used by `go tool pprof`   |

Requesting `/debug/pprof` without the trailing slash redirects to
`/debug/pprof/`, carrying the query string across so the token is not lost.

The response codes are worth knowing, because they distinguish the ways this goes
wrong, and which one you get depends on whether authentication is configured:

| Code  | Meaning                                                                         |
| ----- | ------------------------------------------------------------------------------- |
| `200` | Success                                                                         |
| `302` | Auth configured, and you look like a browser. Redirected to the login page      |
| `401` | Auth configured, and you sent no valid session or `Bearer` token                |
| `403` | No auth provider configured, and the `token=` parameter is missing or wrong     |
| `404` | Profiling is disabled. The feature does not announce that it exists but is shut |
| `410` | You are on the old telemetry-listener location. Use the web server port         |

`403` and `401` are mutually exclusive: the token is only consulted when no
authentication provider is configured, so an instance never returns both. The
`enabled` check runs before either, which is why a correct token still gets `404`
when profiling is off.

## Common profiling tasks

### 1. Analysing memory usage

```bash
# Analyse directly
go tool pprof "http://localhost:8080/debug/pprof/heap?token=$BIRDNET_PROFILING_TOKEN"

# Or save it first
curl -o heap.pprof "http://localhost:8080/debug/pprof/heap?token=$BIRDNET_PROFILING_TOKEN"
go tool pprof heap.pprof
```

Useful commands in pprof interactive mode:

- `top` - show the largest consumers
- `web` - open an interactive graph in a browser (requires graphviz)
- `list <function>` - show the annotated source. This one needs the matching
  source available at the path recorded in the profile. Against a release binary
  that path is module-relative (`-trimpath`), so a plain checkout is not enough
  and it reports `could not find file ... on path`; point pprof at your checkout
  with `-trim_path=github.com/tphakala/birdnet-go` or `-source_path=<dir>`.
  `top` and `web` need nothing but the profile.

### 2. CPU profiling

```bash
# 30 seconds by default
go tool pprof "http://localhost:8080/debug/pprof/profile?token=$BIRDNET_PROFILING_TOKEN"

# Or a specific duration, saved to a file
curl -o cpu.pprof "http://localhost:8080/debug/pprof/profile?token=$BIRDNET_PROFILING_TOKEN&seconds=30"
go tool pprof cpu.pprof
```

Query-parameter order does not matter. Long profiles need no special handling
either: `net/http/pprof` extends the response write deadline itself for the
requested sampling duration, so the default 30-second profile completes against
the server's 30-second write timeout.

### 3. Analysing goroutine leaks

```bash
# Human-readable count and stacks
curl "http://localhost:8080/debug/pprof/goroutine?token=$BIRDNET_PROFILING_TOKEN&debug=1" | head -1

# Or analyse with pprof
go tool pprof "http://localhost:8080/debug/pprof/goroutine?token=$BIRDNET_PROFILING_TOKEN"
```

### 4. Finding lock contention

Requires `mutexfraction` to be non-zero; see below. An empty profile means
sampling was never switched on, not that nothing contended.

```bash
go tool pprof "http://localhost:8080/debug/pprof/mutex?token=$BIRDNET_PROFILING_TOKEN"
```

### 5. Analysing blocking operations

Requires `blockrate` to be non-zero; see below. Same caveat about empty profiles.

```bash
go tool pprof "http://localhost:8080/debug/pprof/block?token=$BIRDNET_PROFILING_TOKEN"
```

### 6. Execution traces

```bash
curl -o trace.out "http://localhost:8080/debug/pprof/trace?token=$BIRDNET_PROFILING_TOKEN&seconds=5"
go tool trace trace.out
```

## Block and mutex sampling

The heap, goroutine, CPU and trace profiles need nothing beyond
`diagnostics.profiling.enabled`. The block and mutex profiles are different: they
require the Go runtime to be sampling continuously, which costs CPU on the audio
path whether or not anyone ever fetches a profile. They are therefore off by
default and configured separately:

```yaml
diagnostics:
  profiling:
    enabled: true
    blockrate: 10000 # ns of blocked time per sample; 0 disables
    mutexfraction: 100 # report 1 in N contention events; 0 disables
```

Both keys hot-reload through the settings API, so you can switch sampling on for
the duration of an investigation and off again without a restart:

```bash
curl -X PATCH http://localhost:8080/api/v2/settings/diagnostics \
  -H 'Content-Type: application/json' \
  -d '{"profiling":{"blockRate":10000,"mutexFraction":100}}'
```

The two have different units, on entirely different scales, though both sample
less as the number grows. `blockrate` is nanoseconds of blocked time per sample.
`mutexfraction` reports one sampled event per that many contention events. Go
documents rate 1 for the block profiler as the way to include every blocking
event, and since the mutex fraction reports one in N, rate 1 there records every
contention event too. That suits a microbenchmark. The values above are the
recommended starting points for a machine doing real-time audio around the clock.

Use whole positive numbers. There is no validation on this section, so a negative
value is accepted by the API and written to `config.yaml`; it is then treated as
0, meaning off, at the point where the rate is handed to the runtime. The
behaviour is safe but the config file will not read the way you intended.

### Two things about these two profiles that will mislead you

**Zero is the only free setting for the block profiler.** The cost is dominated
by the on/off decision, not by the rate. Any non-zero rate arms an unconditional
timestamp read at every channel send, channel receive, select and semaphore
acquire. Measured on a Raspberry Pi 5, a blocking channel round-trip costs about
55% more at rate 1 and still about 28% more at rate 100000, both against rate 0.
So picking a very coarse rate to be safe buys you most of the cost and almost
none of the signal. Either sample at a rate that will tell you something, or set
it to 0.

The mutex fraction is far cheaper. Fraction 1 against fraction 100 was not
resolvable above measurement noise on either arm64 or x86, so the two knobs are
not comparable and should not be reasoned about as a pair.

**Block and mutex profiles are cumulative for the life of the process and cannot
be reset.** There is no endpoint and no setting that clears them. Two
consequences, both of which have caused real confusion:

- A profile fetched after you lower or zero a rate still contains everything
  collected at the old rate. Setting `blockrate` back to 0 does not empty
  `/debug/pprof/block`; it only stops adding to it.
- Two profiles fetched from one process are not independent. The second includes
  everything in the first, so diffing them with `go tool pprof -base` does not
  isolate the interval between them.

To compare two states cleanly, restart the process between them. This does not
apply to CPU profiles, which sample only the window you requested, nor to the
default `inuse_space` view of `heap`, which reports live objects at the moment you
ask. Note that a heap profile also carries `alloc_space` and `alloc_objects`
views, and those two are cumulative since process start in exactly the way block
and mutex profiles are, as is `/debug/pprof/allocs`.

## Environment variable CPU profiling

For startup performance issues, where the web server is not up yet, enable CPU
profiling for the whole run with an environment variable:

```bash
BIRDNET_GO_PROFILE=1 ./birdnet-go serve
```

This writes `profile_YYYYMMDD_HHMMSS.pprof` in the working directory.

Three caveats:

- It covers the entire run and cannot be narrowed to a window.
- The file is written to the process working directory, which is not
  configurable.
- The profile is only flushed when the process shuts down cleanly. After a hard
  kill (`SIGKILL`, or the OOM killer) the file still exists but is **0 bytes**,
  and `go tool pprof` rejects it with `failed to fetch any source profiles`. That
  is worse than losing it outright, because an empty file looks like a profile
  until you try to open it. It matters, because running out of memory is one of
  the cases you would most want a profile of.

For anything after startup, prefer the HTTP endpoints.

## Seeing inside the inference library

A CPU profile of a running instance shows most of its time inside a single opaque
native frame belonging to the inference backend. On a TFLite build the top of the
profile looks like this, and this is normal rather than a symptom:

```
      flat  flat%   sum%        cum   cum%
    13.59s 85.96% 85.96%     13.59s 85.96%  [libtensorflowlite_c.so.2.17.1]
     0.93s  5.88% 91.84%      0.93s  5.88%  runtime.cgocall
```

That is expected: Go's profiler symbolizes Go frames, and the backend is C or C++.
The number is also the answer to "where is the CPU going", it just is not a very
actionable one.

`perf` is the tool for native stacks, and it needs no code shipped in the
product:

```bash
perf record -g -p "$(pgrep -n birdnet-go)" -- sleep 30
perf report
```

**Be realistic about how far that gets you.** `perf` can only name a frame if the
library it lives in carries symbols, and the inference libraries shipped with
BirdNET-Go are stripped. The TFLite library, for example, exports its ~216
`TfLite*` C API functions and nothing below them, so a report resolves the API
boundary the call entered through and shows raw hex addresses for the operator
kernels underneath. Frames in the BirdNET-Go binary itself come out as addresses
too, because the release build strips the ELF symbol table; that costs nothing in
practice, since `go tool pprof` names Go frames properly and is the right tool
for them. Going deeper into the backend with `perf` means running a build of that
backend with symbols, which is not what ships.

So `perf` answers "which backend entry point is hot", not "which operator is
slow". The better answer to the second question is per-operator timings from the
backends themselves (ONNX Runtime session profiling, OpenVINO `PERF_COUNT`),
which also answer whether more threads helped and whether an int8 model actually
selected an int8 kernel. Both backends expose the capability. BirdNET-Go does not
wire it up yet, so there is no flag for it at the moment.

**cgosymbolizer was evaluated and rejected.** It is the obvious-looking way to get
native frames into Go's own profiles, so the reasoning is recorded here to save
the next person the investigation. It runs libbacktrace from a `SIGPROF` signal
handler, and libbacktrace allocates and takes locks, so it is not
async-signal-safe: that is a deadlock risk in a long-running appliance, accepted
in exchange for diagnostics. Its traceback implementation carries a Linux-only
build constraint, so macOS and Windows fail to link and it would need a build tag
regardless. The native frames it would reveal are overwhelmingly inside TFLite,
the legacy backend being phased out in favour of ONNX and OpenVINO. And `perf`
already covers the rare case where native stacks are genuinely needed.

## Why there is no special profiling build

Stripped binaries look like they should break profiling. They do not, and there
is deliberately no separate unstripped build target.

Go binaries always retain the `pclntab`, the runtime line table `go tool pprof`
uses to symbolize Go frames. The release build's `-ldflags "-s -w"` strips DWARF
debug info and the ELF symbol table, which matter for delve and for tools that
read native symbols, but not for Go-level profiling. A release binary reports
fully qualified Go function names in every profile, generic instantiations
included.

An unstripped build would buy one thing: `perf` could name Go frames instead of
printing addresses for them. That is not worth a build target, because `go tool
pprof` already names those frames and does it better. It would not help with the
inference backend at all, which is a separate shared library with its own
stripping.

`-trimpath`, also used by the release build, is harmless for profiling. It
rewrites source paths to module-relative form
(`github.com/tphakala/birdnet-go/internal/...` rather than a path on the build
machine). The only thing it costs is that `list <function>` cannot find the
source unless you have a matching checkout; `top`, `web` and every aggregate view
are unaffected.

Building with `-gcflags=all=-N -l` to get better symbols would be actively
counterproductive: disabling optimization and inlining changes the performance of
the code you are trying to measure, so the profile no longer describes the binary
anyone runs.

## Best practices

1. **Production use.** Turn `diagnostics.profiling.enabled` on while diagnosing
   an issue and off again afterwards. Be especially sparing with `blockrate` and
   `mutexfraction`, which cost CPU continuously rather than only while a profile
   is being fetched.

2. **Memory profiles.** Take several heap profiles over time to find a leak.
   Unlike block and mutex profiles, heap profiles report live objects at the
   moment of the request, so a diff between two of them is meaningful:

   ```bash
   curl -o heap1.pprof "http://localhost:8080/debug/pprof/heap?token=$BIRDNET_PROFILING_TOKEN"
   # wait
   curl -o heap2.pprof "http://localhost:8080/debug/pprof/heap?token=$BIRDNET_PROFILING_TOKEN"
   go tool pprof -base heap1.pprof heap2.pprof
   ```

3. **CPU profiles.** Profile during a representative workload. A profile taken
   while the instance is idle mostly reports that it is idle.

4. **Rotate the token** if you have shared a config file with someone. Delete the
   `token:` line under `diagnostics.profiling`, restart, and a new one is
   generated. A support archive does not need this: the token is redacted from
   it.

## Security note

Since the endpoints moved to the web server, they sit behind whatever
authentication the instance has configured, or behind the generated token where
no authentication provider exists. They are no longer reachable unauthenticated
on the telemetry listener.

Two things are worth stating plainly rather than left implicit:

- **`security.allowsubnetbypass` applies to these routes too.** With it enabled,
  every host on the configured subnet reaches `/debug/pprof/` with no credential
  at all, and no token is minted to stand in the way, because an auth provider is
  configured. That is a wider grant than it first appears; see below for what it
  hands over.
- **Enabling profiling is not free even before anyone fetches a profile**, if you
  also set `blockrate` or `mutexfraction`. See the sampling section above.

What a Go profile actually exposes, since this is widely misunderstood in both
directions:

- A heap profile does **not** contain the contents of allocations. Audio buffers,
  passwords and API keys living in memory do not appear in it. What it contains
  is allocation call stacks, sizes and counts.
- It does expose **code structure**: fully qualified function names, the call
  graph, and source file paths. On a release build those paths are
  module-relative rather than paths on the build machine, because of `-trimpath`.
- `/debug/pprof/cmdline` returns the process command line verbatim, including any
  paths and flags it was started with.
- `/debug/pprof/goroutine?debug=2` returns full goroutine stacks, which include
  some function arguments as raw words.
- `/debug/pprof/profile?seconds=N` and `/debug/pprof/trace?seconds=N` make the
  process do measurable work for the duration requested, so they are a way to
  load the box for anyone who can reach them.

The accurate summary is code-structure and configuration disclosure, plus a way
to spend CPU. Real, but not a dump of your secrets. Never expose these endpoints
to an untrusted network without authentication, and turn profiling off again when
you are done with it.

## Troubleshooting

If the profiling endpoints are not behaving:

1. **`404`** - Profiling is disabled. Verify `diagnostics.profiling.enabled` is
   `true`. `debug: true` does not enable profiling; it only controls logging
   verbosity. If you set it in `config.yaml`, remember it applies at the next
   restart. If the key is set, the instance has been restarted, and you still get
   `404`, your build predates the feature; see Availability above.
2. **`403`** - You are on an instance with no authentication provider, and the
   `token=` parameter is missing or wrong. Read the value from `config.yaml`; the
   API returns that field as `**********` by design.
3. **`401`** - The opposite case: an authentication provider is configured, so the
   token is never consulted and a session or `Bearer` token is required instead.
   `go tool pprof` cannot supply either. Use the login-then-fetch recipe above.
4. **`302` to `/login`** - Same cause as `401`, but your client sent
   `Accept: text/html`, so it was redirected rather than refused.
5. **The token command prints nothing, or an empty string** - No token has been
   minted. Either an authentication provider is configured (in which case none is
   needed), or profiling was never enabled and restarted, or generation or
   persistence failed. The last case is usually a config file the process cannot
   write, and the startup log says so.
6. **`410`** - You are talking to the telemetry listener (default port 8090). The
   endpoints moved to the web server port (default 8080). The telemetry listener
   now serves Prometheus metrics only.
7. **Connection refused on 8090** - The same situation as `410`, on an instance
   where the telemetry listener is not enabled at all. Use the web server port.
8. **Connection refused on the web server port** - Check the web server is running
   and you have the right port. It is the same port as the web UI.
9. **Empty `block` or `mutex` profile, everything else working** - `blockrate`
   and `mutexfraction` are still 0. The endpoints answer `200` with a profile
   containing no samples, which reads as "nothing is contending" but means
   "nothing was recorded". `go tool pprof` shows this as
   `Showing nodes accounting for 0, 0% of 0 total`. The startup log says so too,
   if profiling was enabled at startup with both rates at 0. If you previously
   relied on `debug: true` switching sampling on, that coupling is gone; see
   Migrating above.
10. **A profile looks like it has stale or doubled data** - Block and mutex
    profiles are cumulative and cannot be reset; see the sampling section above.
11. **A `BIRDNET_GO_PROFILE` file that `go tool pprof` rejects** with
    `failed to fetch any source profiles` - the file is 0 bytes because the
    process was hard-killed before the profile was flushed. See the environment
    variable section above.
12. **`could not find file ... on path` from `list`** - expected against a release
    binary, which records module-relative paths. Pass
    `-trim_path=github.com/tphakala/birdnet-go` or `-source_path=<dir>`.

For more information about pprof, see the
[official Go documentation](https://pkg.go.dev/net/http/pprof).
