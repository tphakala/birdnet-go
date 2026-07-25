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

The setting takes effect immediately, with no restart.

### Block and mutex sampling

The heap, goroutine, CPU and trace profiles need nothing beyond the setting
above. The block and mutex profiles are different: they require the Go runtime
to be sampling, which costs CPU on the audio path whether or not anyone ever
fetches a profile. They are therefore off by default and configured separately:

```yaml
diagnostics:
  profiling:
    blockrate: 10000      # ns of blocked time per sample; 0 disables
    mutexfraction: 100    # report 1 in N contention events; 0 disables
```

Note the two have different units. `blockrate` is nanoseconds of blocked time
per sample, so a larger number samples less. `mutexfraction` reports one in
every N contention events. Go's own documentation suggests 1 for both, which
records essentially every event; the values above are the recommended starting
points and keep enough signal to find a contention problem without recording
all of it. Both take effect immediately, with no restart.

One caveat when changing a rate on a running instance: lowering or zeroing a
rate does not discard samples already collected, so a profile taken shortly
after a change can mix rates.

## Available Profiling Endpoints

All profiling endpoints require authentication (if you have authentication enabled) and are available at:

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

1. **Production Use**: Only enable debug mode in production temporarily when diagnosing issues, as profiling has a performance overhead.

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

1. Verify debug mode is enabled in config
2. Check the logs for "pprof debugging endpoints enabled at /debug/pprof/"
3. Ensure you're authenticated if security is enabled
4. Check that the web server is running on the expected port

For more information about pprof, see the [official Go documentation](https://pkg.go.dev/net/http/pprof).
