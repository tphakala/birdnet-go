# BirdNET-Go Profiling Guide

This guide explains how to use the built-in profiling capabilities in BirdNET-Go to diagnose performance issues and memory usage.

## Prerequisites

- BirdNET-Go running with `diagnostics.profiling.enabled` set
- `go` tool installed on your system (for analyzing profiles)

## Enabling Profiling

Profiling is off by default and is not related to `debug: true`, which controls
logging verbosity only. Set it in your config.yaml:

```yaml
diagnostics:
  profiling:
    enabled: true
```

The endpoints are served by the main web server, behind whatever authentication
you have configured. On an instance with no authentication provider, a token is
generated into `diagnostics.profiling.token` when profiling is switched on, and
must be passed as a `token=` query parameter. `go tool pprof` preserves query
parameters, so the one-command workflow still works:

```bash
go tool pprof "http://localhost:8080/debug/pprof/heap?token=YOUR_TOKEN"
```

Changed in `config.yaml` this applies at the next restart, because the config
file is not watched. Changed through the settings API it applies immediately.

### Block and mutex sampling

The heap, goroutine, CPU and trace profiles need nothing beyond the setting
above. The block and mutex profiles are different: they require the Go runtime
to be sampling, which costs CPU on the audio path whether or not anyone ever
fetches a profile. They are therefore off by default and configured separately,
as two more keys in the same `profiling` block:

```yaml
diagnostics:
  profiling:
    enabled: true
    blockrate: 10000      # ns of blocked time per sample; 0 disables
    mutexfraction: 100    # report 1 in N contention events; 0 disables
```

Note the two have different units, though both sample less as the number grows.
`blockrate` is nanoseconds of blocked time per sample. `mutexfraction` reports
one sampled event per that many contention events. Go documents rate 1 for the
block profiler as the way to include every blocking event, and since the mutex
fraction reports one in N, rate 1 there records every contention event too. That
suits a benchmark; the values above are the recommended starting points for a
machine doing real-time audio around the clock, and keep enough signal to find a
contention problem without recording all of it.

One caveat when changing a rate on a running instance: lowering or zeroing a
rate does not discard samples already collected, so a profile taken shortly
after a change can mix rates.

## Available Profiling Endpoints

All profiling endpoints sit behind the web server's authentication. Where no
authentication provider is configured, the generated token described above is
required instead, so the `?token=...` suffix applies to every command on this
page even though the examples below omit it for brevity. The endpoints are:

- `/debug/pprof/` - Index page listing all available profiles
- `/debug/pprof/heap` - Heap memory profile
- `/debug/pprof/goroutine` - Current goroutines
- `/debug/pprof/allocs` - Memory allocations
- `/debug/pprof/threadcreate` - Thread creation profile
- `/debug/pprof/block` - Blocking profile (goroutines waiting on synchronization)
- `/debug/pprof/mutex` - Mutex contention profile
- `/debug/pprof/profile` - CPU profile (captures 30 seconds of CPU usage)
- `/debug/pprof/trace` - Execution trace

## Common Profiling Tasks

### 1. Analyzing Memory Usage

To capture and analyze a heap profile:

```bash
# Capture the heap profile
go tool pprof http://localhost:8080/debug/pprof/heap

# Or save it to a file
curl -o heap.pprof http://localhost:8080/debug/pprof/heap
go tool pprof heap.pprof
```

Useful commands in pprof interactive mode:

- `top` - Show top memory consumers
- `list <function>` - Show source code with memory allocations
- `web` - Open interactive graph in browser (requires graphviz)

### 2. CPU Profiling

To capture CPU profile (30 seconds):

```bash
# Capture CPU profile
go tool pprof http://localhost:8080/debug/pprof/profile

# Or save it
curl -o cpu.pprof http://localhost:8080/debug/pprof/profile?seconds=30
go tool pprof cpu.pprof
```

### 3. Analyzing Goroutine Leaks

To check for goroutine leaks:

```bash
# View current goroutines
curl http://localhost:8080/debug/pprof/goroutine?debug=1

# Or analyze with pprof
go tool pprof http://localhost:8080/debug/pprof/goroutine
```

### 4. Finding Lock Contention

To analyze mutex contention (requires `mutexfraction` to be set; see above, an
empty profile means sampling was never switched on):

```bash
go tool pprof http://localhost:8080/debug/pprof/mutex
```

### 5. Analyzing Blocking Operations

To find where goroutines are blocking (requires `blockrate` to be set; see
above, an empty profile means sampling was never switched on):

```bash
go tool pprof http://localhost:8080/debug/pprof/block
```

## Environment Variable CPU Profiling

For startup performance issues, you can enable CPU profiling via environment variable:

```bash
BIRDNET_GO_PROFILE=1 ./birdnet-go

# This creates a profile file: profile_YYYYMMDD_HHMMSS.pprof
```

## Best Practices

1. **Production Use**: Turn `diagnostics.profiling.enabled` on temporarily when diagnosing an issue and off again afterwards. Be especially sparing with `blockrate` and `mutexfraction`, which cost CPU continuously rather than only while a profile is being fetched.

2. **Memory Profiles**: Take multiple heap profiles over time to identify memory leaks:

   ```bash
   # Take baseline
   curl -o heap1.pprof http://localhost:8080/debug/pprof/heap
   # Wait some time...
   curl -o heap2.pprof http://localhost:8080/debug/pprof/heap
   # Compare
   go tool pprof -base heap1.pprof heap2.pprof
   ```

3. **CPU Profiles**: Run CPU profiling during typical workload for accurate results.

4. **Trace Analysis**: For detailed execution analysis:
   ```bash
   curl -o trace.out http://localhost:8080/debug/pprof/trace?seconds=5
   go tool trace trace.out
   ```

## Security Note

The profiling endpoints are protected by the same authentication mechanism as other admin endpoints. Never expose these endpoints publicly without authentication as they can reveal sensitive information about your application's internals.

## Troubleshooting

If profiling endpoints are not available:

1. Verify `diagnostics.profiling.enabled` is `true` in config. `debug: true` does not enable profiling; it only controls logging verbosity.
2. Where no authentication provider is configured, check you are passing `?token=` with the value from `diagnostics.profiling.token`. A missing or wrong token answers 403; a disabled feature answers 404.
3. Ensure you're authenticated if security is enabled
4. Check that the web server is running on the expected port, not the telemetry port. The endpoints moved off the telemetry listener and the old location answers 410.
5. An empty `block` or `mutex` profile with the endpoints otherwise working means `blockrate` / `mutexfraction` are still 0.

For more information about pprof, see the [official Go documentation](https://pkg.go.dev/net/http/pprof).
