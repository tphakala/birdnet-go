# BirdNET-Go Profiling Guide

This guide explains how to use the built-in profiling capabilities in BirdNET-Go to diagnose performance issues and memory usage.

## Prerequisites

- BirdNET-Go running with debug mode enabled
- `go` tool installed on your system (for analyzing profiles)

## Enabling Debug Mode

To enable profiling endpoints, you need to run BirdNET-Go with debug mode enabled. Set `debug: true` in your config.yaml:

```yaml
debug: true
```

When debug mode is enabled:
- pprof HTTP endpoints are exposed at `/debug/pprof/`
- Mutex profiling is enabled to detect lock contention
- Block profiling is enabled to detect blocking operations

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

To analyze mutex contention:

```bash
go tool pprof http://localhost:8080/debug/pprof/mutex
```

### 5. Analyzing Blocking Operations

To find where goroutines are blocking:

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